package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const judgeSystemPrompt = `你是一个小说阅读助手 Agent 的评测裁判。你需要根据以下信息来评估 Agent 的回答质量。

## 评分维度（每项 1-5 分，精确到 0.5）

1. **accuracy（准确度）**：答案是否与原文参考内容一致？是否有事实错误？
   - 5：与原文完全一致，无任何错误
   - 3：大体正确，但有 1-2 处小偏差
   - 1：大量错误或完全不一致

2. **completeness（完整性）**：是否遗漏了关键信息？
   - 5：涵盖了所有关键信息
   - 3：覆盖了主要信息，但有遗漏
   - 1：遗漏了大量关键信息

3. **conciseness（简洁度）**：回答是否简洁直接，没有冗余？
   - 5：非常简洁，没有废话
   - 3：基本简洁，但有些冗余
   - 1：大量废话，没有直接回答问题

4. **tool_selection（工具选择）**：Agent 是否选择了合适的工具？调用顺序是否合理？
   - 5：工具选择精准，顺序合理
   - 3：基本正确，但不够优化
   - 1：明显选错了工具或顺序混乱

5. **safety（安全性）**：是否严格遵守了"不剧透"规则？
   - 5：完全没有剧透，严格限定在阅读进度内
   - 3：没有明显剧透，但可能暗示了后续内容
   - 1：引用了阅读进度之后的内容（严重违规）

6. **overall（综合）**：整体回答质量。
   - 5：优秀，可以直接用于回答用户
   - 3：合格，但需要改进
   - 1：不合格

## 输出格式

你必须严格输出以下 JSON 格式，不要加任何其他文字：

{
  "accuracy": 4.5,
  "completeness": 4.0,
  "conciseness": 3.5,
  "tool_selection": 5.0,
  "safety": 5.0,
  "overall": 4.5,
  "reasoning": "简要说明评分理由，每个维度一句话"
}
`

// judgeInput is the data passed to the LLM judge for scoring.
type judgeInput struct {
	Question       string
	Progress       int
	ReferenceChapters string // ground-truth content for accuracy check
	FinalAnswer    string
	ToolCalls      string // formatted tool call log
	Thinking       string // agent's reasoning steps
}

// Judge evaluates an agent run using an LLM as the judge.
type Judge struct {
	model einomodel.ToolCallingChatModel
}

// NewJudge creates a new LLM judge.
func NewJudge(model einomodel.ToolCallingChatModel) *Judge {
	return &Judge{model: model}
}

// Score sends the run record to the LLM judge and returns multi-dimensional scores.
func (j *Judge) Score(ctx context.Context, tc *TestCase, record *RunRecord) (*JudgeResult, error) {
	input := buildJudgeInput(tc, record)

	userMsg := fmt.Sprintf(`## 用户问题
%s

## 阅读进度边界
第 1 ~ %d 章（绝对不能引用进度之后的内容）

## 原文参考（相关章节）
%s

## Agent 的最终回答
%s

## Agent 的工具调用日志
%s

## Agent 的思考过程
%s

请根据以上信息进行多维度评分。`,
		input.Question,
		input.Progress,
		truncateForJudge(input.ReferenceChapters, 3000),
		input.FinalAnswer,
		input.ToolCalls,
		input.Thinking,
	)

	messages := []*schema.Message{
		schema.SystemMessage(judgeSystemPrompt),
		schema.UserMessage(userMsg),
	}

	resp, err := j.model.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("judge generate: %w", err)
	}

	return parseJudgeResponse(resp.Content)
}


// ── helpers ─────────────────────────────────────────────────────────────────

func buildJudgeInput(tc *TestCase, record *RunRecord) *judgeInput {
	// Format tool calls as readable text
	var toolLog strings.Builder
	for _, tc := range record.ToolCalls {
		toolLog.WriteString(fmt.Sprintf(
			"[Step %d] %s(%s) → %s",
			tc.Step, tc.ToolName, tc.Arguments, truncate(tc.Result, 300),
		))
		if tc.Error != "" {
			toolLog.WriteString(fmt.Sprintf(" [错误: %s]", tc.Error))
		}
		toolLog.WriteString("\n")
	}

	// Format thinking
	thinking := strings.Join(record.Thinking, "\n---\n")

	return &judgeInput{
		Question:          tc.Question,
		Progress:          tc.Progress,
		ReferenceChapters: tc.ReferenceChapters,
		FinalAnswer:       record.FinalAnswer,
		ToolCalls:         toolLog.String(),
		Thinking:          thinking,
	}
}

func parseJudgeResponse(content string) (*JudgeResult, error) {
	// Extract JSON from response (LLM may wrap it in markdown)
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		// Strip markdown code fences
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}

	var scores JudgeScores
	if err := json.Unmarshal([]byte(content), &scores); err != nil {
		// Try to find JSON object in the content
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(content[start:end+1]), &scores); err2 != nil {
				return nil, fmt.Errorf("parse judge response: %w (raw: %s)", err, truncate(content, 200))
			}
		} else {
			return nil, fmt.Errorf("parse judge response: %w (raw: %s)", err, truncate(content, 200))
		}
	}

	// Extract reasoning separately
	var wrapper struct {
		Reasoning string `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil {
		return &JudgeResult{Scores: scores, Reasoning: wrapper.Reasoning}, nil
	}
	// Fallback: try extracting from the partial JSON
	_ = json.Unmarshal([]byte(content), &wrapper)

	return &JudgeResult{Scores: scores, Reasoning: wrapper.Reasoning}, nil
}

func truncateForJudge(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "\n… (已截断)"
}
