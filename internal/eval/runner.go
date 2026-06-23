package eval

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"note-memory/internal/service/qa"
)

// ProgressFunc sets the reading progress for a novel before running a test case.
// Called with (novelID, chapterNumber). A chapterNumber of 0 means "read all".
type ProgressFunc func(novelID int64, chapter int) error

// Runner orchestrates the evaluation: run agent → record → assert → judge → report.
type Runner struct {
	qaService  *qa.Service
	judge      *Judge
	setProgress ProgressFunc

	// OutputDir is where run records and reports are saved.
	OutputDir string
}

// NewRunner creates a new evaluation runner.
// qaService must be fully initialized (with models, repos, etc.).
// judgeModel is the LLM used for scoring (can be the same as the agent's model).
// setProgress may be nil (progress is then skipped — caller manages it externally).
func NewRunner(qaService *qa.Service, judge *Judge, outputDir string, setProgress ProgressFunc) *Runner {
	return &Runner{
		qaService:   qaService,
		judge:       judge,
		OutputDir:   outputDir,
		setProgress: setProgress,
	}
}

// RunSingle executes a single test case and returns the evaluation result.
func (r *Runner) RunSingle(ctx context.Context, tc *TestCase) (*EvalResult, error) {
	log.Printf("[eval] ── 开始评测: [%s] %s ──", tc.ID, tc.Description)
	log.Printf("[eval] 问题: %s", tc.Question)

	// 1. Set progress (0 means read all, passed as a large sentinel)
	if r.setProgress != nil {
		ch := tc.Progress
		if ch <= 0 {
			ch = 1<<31 - 1 // sentinel: read everything
		}
		if err := r.setProgress(tc.NovelID, ch); err != nil {
			log.Printf("[eval] ⚠️ 设置进度失败: %v", err)
		}
	}

	// 2. Create recorder
	rec := NewRecorder(tc.Question, "", tc.Progress)

	// 3. Run agent with recording
	startTime := time.Now()
	answer, err := r.qaService.AskQuestionWithRecorder(ctx, tc.NovelID, nil, tc.Question, rec)
	runDuration := time.Since(startTime)

	if err != nil {
		log.Printf("[eval] ❌ Agent 运行失败: %v", err)
		rec.OnError(err)
	}

	record := rec.Record()
	if answer != "" && record.FinalAnswer == "" {
		record.FinalAnswer = answer
	}
	record.Duration = runDuration

	// 4. Save run record
	if r.OutputDir != "" {
		path := fmt.Sprintf("%s/%s_record.json", r.OutputDir, tc.ID)
		if err := SaveRunRecord(record, path); err != nil {
			log.Printf("[eval] ⚠️ 保存记录失败: %v", err)
		}
	}

	// 5. Run automated assertions
	assertion := RunAssertions(tc, record)

	// 6. Run LLM judge
	result := &EvalResult{
		Case:      tc,
		Record:    record,
		Assertion: assertion,
	}

	if r.judge != nil {
		judgeResult, err := r.judge.Score(ctx, tc, record)
		if err != nil {
			log.Printf("[eval] ⚠️ 裁判评分失败: %v", err)
		} else {
			result.Judge = judgeResult
		}
	}

	return result, nil
}

// RunBatch executes multiple test cases sequentially, saves records/reports,
// and prints live progress to stderr. Returns the final report.
func (r *Runner) RunBatch(ctx context.Context, cases []*TestCase) (*Report, error) {
	os.MkdirAll(r.OutputDir, 0755)

	report := &Report{
		Total:     len(cases),
		StartedAt: time.Now(),
		Details:   make([]*EvalResult, 0, len(cases)),
	}

	fmt.Fprintf(os.Stderr, "\n%s\n", strings.Repeat("━", 72))
	fmt.Fprintf(os.Stderr, "🔍 RAG 测试 | 共 %d 题\n", len(cases))
	fmt.Fprintf(os.Stderr, "%s\n\n", strings.Repeat("━", 72))

	for i, tc := range cases {
		fmt.Fprintf(os.Stderr, "[%d/%d] [%s] %s\n", i+1, len(cases), tc.ID, tc.Description)
		fmt.Fprintf(os.Stderr, "  Q: %s\n", tc.Question)

		result, err := r.RunSingle(ctx, tc)
		if err != nil {
			log.Printf("[eval] ❌ 用例 [%s] 执行失败: %v", tc.ID, err)
			result = &EvalResult{
				Case: tc,
				Assertion: &AssertionResult{
					Pass:     false,
					Failures: []string{fmt.Sprintf("执行失败: %v", err)},
				},
			}
		}
		report.Details = append(report.Details, result)

		// Per-case one-line status
		rec := result.Record
		passIcon := "✅"
		if rec == nil || (result.Assertion != nil && !result.Assertion.Pass) {
			passIcon = "❌"
		}
		toolN, thinkN, ansLen := 0, 0, 0
		if rec != nil {
			toolN = len(rec.ToolCalls)
			thinkN = len(rec.Thinking)
			ansLen = len([]rune(rec.FinalAnswer))
		}
		dur := time.Duration(0)
		if rec != nil {
			dur = rec.Duration
		}
		fmt.Fprintf(os.Stderr, "  %s 工具:%d 思考:%d段 答案:%d字 %v\n",
			passIcon, toolN, thinkN, ansLen, dur.Round(time.Millisecond))
		if result.Assertion != nil {
			for _, f := range result.Assertion.Failures {
				fmt.Fprintf(os.Stderr, "     ⚠️  %s\n", f)
			}
		}
	}

	report.Duration = time.Since(report.StartedAt)

	// Count pass/fail
	for _, res := range report.Details {
		if res.Assertion != nil && res.Assertion.Pass {
			report.Passed++
		} else {
			report.Failed++
		}
	}

	// Compute average judge scores
	if r.judge != nil {
		var count int
		for _, res := range report.Details {
			if res.Judge != nil {
				report.AvgScores.Accuracy += res.Judge.Scores.Accuracy
				report.AvgScores.Completeness += res.Judge.Scores.Completeness
				report.AvgScores.Conciseness += res.Judge.Scores.Conciseness
				report.AvgScores.ToolSelection += res.Judge.Scores.ToolSelection
				report.AvgScores.Safety += res.Judge.Scores.Safety
				report.AvgScores.Overall += res.Judge.Scores.Overall
				count++
			}
		}
		if count > 0 {
			report.AvgScores.Accuracy /= float64(count)
			report.AvgScores.Completeness /= float64(count)
			report.AvgScores.Conciseness /= float64(count)
			report.AvgScores.ToolSelection /= float64(count)
			report.AvgScores.Safety /= float64(count)
			report.AvgScores.Overall /= float64(count)
		}
	}

	// Save JSON + HTML reports
	jsonPath := fmt.Sprintf("%s/report.json", r.OutputDir)
	if err := SaveReport(report, jsonPath); err != nil {
		log.Printf("[eval] ⚠️ 保存 JSON 报告失败: %v", err)
	}
	htmlPath := fmt.Sprintf("%s/report.html", r.OutputDir)
	if err := SaveHTMLReport(report, htmlPath); err != nil {
		log.Printf("[eval] ⚠️ 保存 HTML 报告失败: %v", err)
	} else {
		log.Printf("[eval] 📄 HTML 报告已保存: %s", htmlPath)
	}

	// Print console summary
	PrintConsoleSummary(report)

	return report, nil
}

// runAssertions performs automated checks on tool selection, keywords, etc.
// RunAssertions performs automated checks on tool selection, keywords, etc.
func RunAssertions(tc *TestCase, record *RunRecord) *AssertionResult {
	var failures []string

	// 1. Expected tools check
	usedTools := make(map[string]bool)
	for _, t := range record.ToolCalls {
		usedTools[t.ToolName] = true
	}
	for _, exp := range tc.ExpectedTools {
		if !usedTools[exp] {
			failures = append(failures, fmt.Sprintf("期望使用工具 %s 但未调用", exp))
		}
	}

	// 2. Forbidden tools check
	for _, fb := range tc.ForbiddenTools {
		if usedTools[fb] {
			failures = append(failures, fmt.Sprintf("不应使用工具 %s 但实际调用了", fb))
		}
	}

	// 3. Must mention check
	for _, kw := range tc.MustMention {
		if !containsRune(record.FinalAnswer, kw) {
			failures = append(failures, fmt.Sprintf("答案中缺少关键词: %s", kw))
		}
	}

	// 4. Must not mention check
	for _, kw := range tc.MustNotMention {
		if containsRune(record.FinalAnswer, kw) {
			failures = append(failures, fmt.Sprintf("答案中包含禁止关键词: %s", kw))
		}
	}

	// 5. Max iterations check
	if tc.MaxIterations > 0 && len(record.ToolCalls) > tc.MaxIterations {
		failures = append(failures, fmt.Sprintf(
			"工具调用次数 %d 超过上限 %d", len(record.ToolCalls), tc.MaxIterations,
		))
	}

	return &AssertionResult{
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}

func containsRune(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		len([]rune(s)) >= len([]rune(substr)) &&
		containsString(s, substr)
}

func containsString(s, substr string) bool {
	for i := 0; i <= len([]rune(s))-len([]rune(substr)); i++ {
		if string([]rune(s)[i:i+len([]rune(substr))]) == substr {
			return true
		}
	}
	return false
}
