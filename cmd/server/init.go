package main

import (
	"context"
	"log"
	"note-memory/internal/config"
	"note-memory/internal/graph"
	"note-memory/internal/memory"
	"note-memory/internal/repository"
	"note-memory/internal/service/chapter"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/novel"
	"note-memory/internal/service/qa"
	"note-memory/internal/service/search"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
	"gorm.io/gorm"
)

// --- Repos ---

type repos struct {
	novel    *repository.NovelRepo
	chapter  *repository.ChapterRepo
	progress *repository.ProgressRepo
}

func initRepos(db *gorm.DB) repos {
	return repos{
		novel:    repository.NewNovelRepo(db),
		chapter:  repository.NewChapterRepo(db),
		progress: repository.NewProgressRepo(db),
	}
}

// --- Deps ---

type deps struct {
	chat      einomodel.ToolCallingChatModel
	embedder  embedding.Embedder
	reranker  search.Reranker
	neoWriter *graph.GraphWriter
	neoReader *graph.GraphReader
}

func initDeps(cfg *config.Config) deps {
	chatModel, emb, rnk := newChatModel(cfg.OpenAI), newEmbedder(cfg.OpenAI), newReranker(cfg.Rerank)

	neoWriter, neoReader := (*graph.GraphWriter)(nil), (*graph.GraphReader)(nil)
	driver, err := graph.NewDriver(cfg.Neo4j)
	if err != nil {
		log.Printf("Neo4j 连接失败（知识图谱功能不可用）: %v", err)
	} else if driver != nil {
		graph.InitSchema(context.Background(), driver)
		neoWriter, neoReader = graph.NewGraphWriter(driver), graph.NewGraphReader(driver)
	}

	return deps{chat: chatModel, embedder: emb, reranker: rnk, neoWriter: neoWriter, neoReader: neoReader}
}

// --- Services ---

type services struct {
	entity      *entity.Service
	search      *search.Service
	chapter     *chapter.Service
	novel       *novel.Service
	qa          *qa.Service
	memoryStore *memory.PostgresStore
}

func initServices(r repos, d deps, db *gorm.DB) services {
	entitySvc := entity.NewService(r.chapter, d.chat, d.embedder)
	searchSvc := search.NewService(r.chapter, d.embedder, entitySvc, d.reranker)
	chapterSvc := chapter.NewService(r.chapter, r.novel, d.chat, d.embedder, searchSvc, d.neoWriter, entitySvc)
	novelSvc := novel.NewService(nil, r.novel, r.chapter, r.progress, chapterSvc, d.chat)
	qaSvc := qa.NewService(r.novel, r.progress, d.chat, searchSvc, d.neoReader, entitySvc)

	memStore := memory.NewPostgresStore(db)

	return services{entity: entitySvc, search: searchSvc, chapter: chapterSvc, novel: novelSvc, qa: qaSvc, memoryStore: memStore}
}
