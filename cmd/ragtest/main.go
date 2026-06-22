package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

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

type runResult struct {
	tc      *eval.TestCase
	record  *eval.RunRecord
	assert  *eval.AssertionResult
	elapsed time.Duration
	err     error
}

func main() {
	novelOverride := flag.Int64("novel", 0, "覆盖所有用例的小说 ID（0=使用文件中的 novel_id）")
	singleQ := flag.String("q", "", "单个测试问题（无需文件）")
	filePath := flag.String("file", "", "测试用例 JSON 文件（格式同 eval.TestCase）")
	outDir := flag.String("out", "eval_output", "HTML 报告输出目录")
	flag.Parse()

	// ── Load test cases ──
	var cases []*eval.TestCase
	if *singleQ != "" {
		if *novelOverride <= 0 {
			log.Fatal("单题模式需要 -novel <ID>")
		}
		cases = []*eval.TestCase{{
			ID:       "ad-hoc",
			NovelID:  *novelOverride,
			Question: *singleQ,
		}}
	} else if *filePath != "" {
		loaded, err := eval.LoadTestCases(*filePath)
		if err != nil {
			log.Fatalf("加载测试用例失败: %v", err)
		}
		cases = loaded
		if len(cases) == 0 {
			log.Fatal("测试用例文件为空")
		}
		// Apply novel ID override
		if *novelOverride > 0 {
			for _, tc := range cases {
				tc.NovelID = *novelOverride
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "用法:")
		fmt.Fprintln(os.Stderr, "  单题: go run ./cmd/ragtest -novel <ID> -q \"<问题>\"")
		fmt.Fprintln(os.Stderr, "  批量: go run ./cmd/ragtest -file <cases.json> [-novel <覆盖ID>]")
		os.Exit(1)
	}

	// ── Init ──
	_ = godotenv.Load()
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DB.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	_ = migration.RunPre(db)
	_ = db.AutoMigrate(&model.Novel{}, &model.Chapter{}, &model.ReadingProgress{}, &model.QACache{}, &model.ChapterChunk{}, &model.EntityEmbedding{})
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
	qaSvc := qa.NewService(novelRepo, progressRepo, chatModel, searchSvc, neoReader, entitySvc)

	ctx := context.Background()

	// ── Run ──
	type result struct {
		tc      *eval.TestCase
		record  *eval.RunRecord
		assert  *eval.AssertionResult
		elapsed time.Duration
		err     error
	}
	results := make([]runResult, 0, len(cases))

	fmt.Println(strings.Repeat("━", 72))
	fmt.Printf("🔍 RAG 测试 | 共 %d 题\n", len(cases))
	if *filePath != "" {
		fmt.Printf("   文件: %s\n", *filePath)
	}
	fmt.Println(strings.Repeat("━", 72))

	totalStart := time.Now()
	for i, tc := range cases {
		fmt.Printf("\n[%d/%d] [%s] %s\n", i+1, len(cases), tc.ID, tc.Description)
		fmt.Printf("  Q: %s\n", tc.Question)

		// Ensure progress is set (so spoiler-free boundary works)
		if tc.Progress <= 0 {
			tc.Progress = 9999 // default: read everything
		}
		progressRepo.Upsert(tc.NovelID, tc.Progress)

		rec := eval.NewRecorder(tc.Question, "", tc.Progress)
		start := time.Now()
		answer, err := qaSvc.AskQuestionWithRecorder(ctx, tc.NovelID, nil, tc.Question, rec)
		elapsed := time.Since(start)

		if err != nil {
			rec.OnError(err)
			fmt.Printf("  ❌ 运行错误: %v\n", err)
		}
		record := rec.Record()
		if answer != "" && record.FinalAnswer == "" {
			record.FinalAnswer = answer
		}

		// Run assertions
		assert := eval.RunAssertions(tc, record)

		results = append(results, runResult{tc: tc, record: record, assert: assert, elapsed: elapsed, err: err})

		// Per-question output
		passIcon := "✅"
		if !(err == nil && assert.Pass) {
			passIcon = "❌"
		}
		fmt.Printf("  %s 工具:%d 思考:%d段 答案:%d字 %v\n",
			passIcon, len(record.ToolCalls), len(record.Thinking),
			len([]rune(record.FinalAnswer)), elapsed.Round(time.Millisecond))
		if len(assert.Failures) > 0 {
			for _, f := range assert.Failures {
				fmt.Printf("     ⚠️  %s\n", f)
			}
		}
	}
	totalElapsed := time.Since(totalStart)

	// ── Summary ──
	fmt.Println()
	fmt.Println(strings.Repeat("═", 72))
	fmt.Println("📊 汇总")
	fmt.Println(strings.Repeat("═", 72))

	passed := 0
	for _, r := range results {
		if r.err == nil && r.assert.Pass {
			passed++
		}
	}
	pct := float64(passed) / float64(len(results)) * 100
	fmt.Printf("  通过: %d/%d (%.0f%%)\n", passed, len(results), pct)
	fmt.Printf("  总耗时: %v\n", totalElapsed.Round(time.Millisecond))

	var totalTools, totalChars int
	for _, r := range results {
		totalTools += len(r.record.ToolCalls)
		totalChars += len([]rune(r.record.FinalAnswer))
	}
	fmt.Printf("  工具调用总数: %d (均 %.1f/题)\n", totalTools, float64(totalTools)/float64(len(results)))
	fmt.Printf("  答案总字数: %d (均 %.0f/题)\n", totalChars, float64(totalChars)/float64(len(results)))
	fmt.Println(strings.Repeat("═", 72))

	// ── Detail table ──
	fmt.Println()
	fmt.Printf("%-12s %-30s %6s %8s %5s %s\n", "ID", "描述", "工具数", "耗时", "断言", "期望工具")
	fmt.Println(strings.Repeat("─", 90))
	for _, r := range results {
		status := "✅"
		if r.err != nil || !r.assert.Pass {
			status = "❌"
		}
		expectStr := ""
		if len(r.tc.ExpectedTools) > 0 {
			expectStr = strings.Join(r.tc.ExpectedTools, ",")
		} else if len(r.tc.ForbiddenTools) > 0 {
			expectStr = "禁:" + strings.Join(r.tc.ForbiddenTools, ",")
		}
		fmt.Printf("%-12s %-30s %6d %8s %5s %s\n",
			r.tc.ID, truncate(r.tc.Description, 28),
			len(r.record.ToolCalls),
			r.elapsed.Round(time.Millisecond).String(),
			status, expectStr)
	}

	// ── HTML Report ──
	report := buildReport(results, totalStart, totalElapsed)
	os.MkdirAll(*outDir, 0755)
	htmlPath := fmt.Sprintf("%s/rag_report.html", *outDir)
	if err := eval.SaveHTMLReport(report, htmlPath); err != nil {
		fmt.Printf("\n⚠️  HTML 报告生成失败: %v\n", err)
	} else {
		fmt.Printf("\n📄 HTML 报告已保存: %s\n", htmlPath)
	}
}

// buildReport converts internal results to eval.Report for HTML output.
func buildReport(results []runResult, startedAt time.Time, duration time.Duration) *eval.Report {
	report := &eval.Report{
		Total:     len(results),
		StartedAt: startedAt,
		Duration:  duration,
	}

	for _, r := range results {
		pass := r.err == nil && r.assert.Pass
		if pass {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Details = append(report.Details, &eval.EvalResult{
			Case:      r.tc,
			Record:    r.record,
			Assertion: r.assert,
		})
	}
	return report
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
