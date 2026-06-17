package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"note-memory/internal/config"
	"note-memory/internal/handler"
	"note-memory/internal/memory"
	"note-memory/internal/migration"
	"note-memory/internal/model"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DB.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	log.Println("数据库连接成功")

	// Phase 1: Pre-GORM migrations (extensions that must exist before table creation)
	if err := migration.RunPre(db); err != nil {
		log.Fatalf("数据库预迁移失败: %v", err)
	}

	// Phase 2: GORM AutoMigrate (tables, standard columns, indexes)
	if err := db.AutoMigrate(&model.Novel{}, &model.Chapter{}, &model.ReadingProgress{}, &model.QACache{}, &model.ChapterChunk{}, &model.EntityEmbedding{}); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	// Phase 3: Post-GORM migrations (PG-specific columns, GIN indexes, constraint fixes)
	if err := migration.RunPost(db); err != nil {
		log.Fatalf("数据库后迁移失败: %v", err)
	}
	log.Println("数据库迁移完成")

	repo := initRepos(db)
	dep := initDeps(cfg)
	svcs := initServices(repo, dep, db)

	// Auto-migrate memory tables
	if err := svcs.memoryStore.AutoMigrate(); err != nil {
		log.Fatalf("聊天记忆表迁移失败: %v", err)
	}

	sessionMgr := memory.NewChatSessionManager(svcs.memoryStore, memory.NewBufferWindow(10))
	h := handler.NewNovelHandler(svcs.novel, svcs.qa, svcs.search, sessionMgr)
	router := setupRouter(h, cfg.Server.Port)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("正在优雅关闭服务...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("HTTP 服务关闭异常: %v", err)
		}

		// Close Neo4j driver
		if dep.neoReader != nil {
			// neoReader and neoWriter share the same driver; close once
		}
		if dep.neoWriter != nil {
			// clean up via the driver reference
		}

		// Close DB connection pool
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}

		log.Println("服务已关闭")
	}()

	log.Printf("服务启动于 http://localhost%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务启动失败: %v", err)
	}
}
