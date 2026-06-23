package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"note-memory/internal/config"
	"note-memory/internal/eval"
	"note-memory/internal/graph"
	"note-memory/internal/migration"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/service/chapter"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/novel"
	"note-memory/internal/service/qa"
	"note-memory/internal/service/search"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	novelOverride := flag.Int64("novel", 0, "覆盖所有用例的小说 ID（0=使用文件中的 novel_id）")
	singleQ := flag.String("q", "", "单个测试问题（无需文件）")
	filePath := flag.String("file", "", "测试用例 JSON 文件（格式同 eval.TestCase）")
	outDir := flag.String("out", "eval_output", "HTML 报告输出目录")
	flag.Parse()

	// ── Load test cases ──
	cases := loadCases(*singleQ, *filePath, *novelOverride)

	// ── Init services ──
	qaSvc, progressRepo := initServices()

	ctx := context.Background()
	runner := eval.NewRunner(
		qaSvc,
		nil, // judge disabled by default; pass eval.NewJudge(chatModel) to enable
		*outDir,
		progressRepo.Upsert,
	)

	if _, err := runner.RunBatch(ctx, cases); err != nil {
		log.Fatalf("评测运行失败: %v", err)
	}
}

// loadCases loads test cases from CLI flags.
func loadCases(singleQ, filePath string, novelOverride int64) []*eval.TestCase {
	if singleQ != "" {
		if novelOverride <= 0 {
			log.Fatal("单题模式需要 -novel <ID>")
		}
		return []*eval.TestCase{{
			ID:       "ad-hoc",
			NovelID:  novelOverride,
			Question: singleQ,
		}}
	}
	if filePath != "" {
		cases, err := eval.LoadTestCases(filePath)
		if err != nil {
			log.Fatalf("加载测试用例失败: %v", err)
		}
		if len(cases) == 0 {
			log.Fatal("测试用例文件为空")
		}
		if novelOverride > 0 {
			for _, tc := range cases {
				tc.NovelID = novelOverride
			}
		}
		return cases
	}
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  单题: go run ./cmd/ragtest -novel <ID> -q \"<问题>\"")
	fmt.Fprintln(os.Stderr, "  批量: go run ./cmd/ragtest -file <cases.json> [-novel <覆盖ID>]")
	os.Exit(1)
	return nil
}

// initServices initializes all infrastructure and returns the QA service + progress repo.
func initServices() (*qa.Service, *repository.ProgressRepo) {
	_ = godotenv.Load()
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DB.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	_ = migration.RunPre(db)
	_ = db.AutoMigrate(&model.Novel{}, &model.Chapter{}, &model.ReadingProgress{},
		&model.QACache{}, &model.ChapterChunk{}, &model.EntityEmbedding{})
	_ = migration.RunPost(db)

	novelRepo := repository.NewNovelRepo(db)
	chapterRepo := repository.NewChapterRepo(db)
	progressRepo := repository.NewProgressRepo(db)

	chatModel, emb, rnk := newChatModel(cfg.OpenAI), newEmbedder(cfg.OpenAI), newReranker(cfg.Rerank)

	neoWriter, neoReader := (*graph.GraphWriter)(nil), (*graph.GraphReader)(nil)
	driver, err := graph.NewDriver(cfg.Neo4j)
	if err != nil {
		log.Printf("⚠️  Neo4j 不可用: %v", err)
	} else if driver != nil {
		graph.InitSchema(context.Background(), driver)
		neoWriter, neoReader = graph.NewGraphWriter(driver), graph.NewGraphReader(driver)
	}

	entitySvc := entity.NewService(chapterRepo, chatModel, emb)
	searchSvc := search.NewService(chapterRepo, emb, entitySvc, rnk)
	chapterSvc := chapter.NewService(chapterRepo, novelRepo, chatModel, emb, searchSvc, neoWriter, entitySvc)
	_ = novel.NewService(nil, novelRepo, chapterRepo, progressRepo, chapterSvc, chatModel)

	return qa.NewService(novelRepo, progressRepo, chatModel, searchSvc, neoReader, entitySvc), progressRepo
}
