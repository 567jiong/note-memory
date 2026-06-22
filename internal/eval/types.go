package eval

import "time"

// ── Recorder: captures agent execution data during a run ─────────────────────

// ToolCallRecord captures a single tool invocation during agent execution.
type ToolCallRecord struct {
	Step      int           `json:"step"`
	ToolName  string        `json:"tool_name"`
	Arguments string        `json:"arguments"`
	Result    string        `json:"result"`  // truncated
	Duration  time.Duration `json:"duration"`
	Error     string        `json:"error,omitempty"`
}

// RunRecord is the complete execution trace of a single agent run.
type RunRecord struct {
	Question    string            `json:"question"`
	NovelTitle  string            `json:"novel_title"`
	MaxChapter  int               `json:"max_chapter"`
	Thinking    []string          `json:"thinking"`
	ToolCalls   []ToolCallRecord  `json:"tool_calls"`
	FinalAnswer string            `json:"final_answer"`
	Error       string            `json:"error,omitempty"`
	Duration    time.Duration     `json:"duration"`
	StartedAt   time.Time         `json:"started_at"`
}

// Recorder is the interface that eval package uses to capture agent execution.
// The qa package calls these methods at key points during agent execution.
type Recorder interface {
	OnThinking(step int, content string)
	OnToolCall(step int, toolName string, args string)
	OnToolResult(toolName string, result string, toolErr string)
	OnFinalAnswer(answer string)
	OnError(err error)
	Record() *RunRecord
}

// ── Test case definition ────────────────────────────────────────────────────

// TestCase defines a single evaluation case.
type TestCase struct {
	ID          string   `json:"id" yaml:"id"`
	Description string   `json:"description" yaml:"description"`
	NovelID     int64    `json:"novel_id" yaml:"novel_id"`
	Progress    int      `json:"progress" yaml:"progress"`
	Question    string   `json:"question" yaml:"question"`

	// Expected behavior (for automated assertions)
	ExpectedTools  []string `json:"expected_tools,omitempty" yaml:"expected_tools,omitempty"`
	ForbiddenTools []string `json:"forbidden_tools,omitempty" yaml:"forbidden_tools,omitempty"`
	MustMention    []string `json:"must_mention,omitempty" yaml:"must_mention,omitempty"`
	MustNotMention []string `json:"must_not_mention,omitempty" yaml:"must_not_mention,omitempty"`
	MaxIterations  int      `json:"max_iterations,omitempty" yaml:"max_iterations,omitempty"`

	// Reference (for human review; used by LLM judge as ground truth context)
	ReferenceChapters string `json:"reference_chapters,omitempty" yaml:"reference_chapters,omitempty"`
}

// ── Judge scoring ───────────────────────────────────────────────────────────

// JudgeScores holds the multi-dimensional scores from the LLM judge.
type JudgeScores struct {
	Accuracy      float64 `json:"accuracy"`       // 1-5: 答案是否与原文一致
	Completeness  float64 `json:"completeness"`    // 1-5: 是否遗漏关键信息
	Conciseness   float64 `json:"conciseness"`     // 1-5: 是否冗余啰嗦
	ToolSelection float64 `json:"tool_selection"`  // 1-5: 工具选择与调用顺序是否合理
	Safety        float64 `json:"safety"`          // 1-5: 是否剧透、是否编造
	Overall       float64 `json:"overall"`         // 1-5: 综合评分
}

// JudgeResult is the full scoring output for a single test case.
type JudgeResult struct {
	CaseID    string      `json:"case_id"`
	Scores    JudgeScores `json:"scores"`
	Reasoning string      `json:"reasoning"`
	ModelName string      `json:"model_name,omitempty"`
}

// ── Assertion result (non-LLM automated checks) ─────────────────────────────

// AssertionResult holds the outcome of automated checks on a run.
type AssertionResult struct {
	Pass    bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
}

// ── Combined evaluation result ──────────────────────────────────────────────

// EvalResult combines assertion results and judge scores for a single test case.
type EvalResult struct {
	Case      *TestCase        `json:"case"`
	Record    *RunRecord       `json:"record"`
	Assertion *AssertionResult `json:"assertion"`
	Judge     *JudgeResult     `json:"judge,omitempty"`
}

// ── Evaluation report ───────────────────────────────────────────────────────

// Report summarizes all evaluation results.
type Report struct {
	Total      int               `json:"total"`
	Passed     int               `json:"passed"`
	Failed     int               `json:"failed"`
	AvgScores  JudgeScores       `json:"avg_scores"`
	Details    []*EvalResult     `json:"details"`
	StartedAt  time.Time         `json:"started_at"`
	Duration   time.Duration     `json:"duration"`
}
