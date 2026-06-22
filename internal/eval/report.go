package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// SaveReport writes an evaluation report to a JSON file.
func SaveReport(r *Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, b, 0644)
}

// FormatReport returns a human-readable Markdown summary of the evaluation.
func FormatReport(r *Report) string {
	var sb strings.Builder

	sb.WriteString("# Agent 评测报告\n\n")
	sb.WriteString(fmt.Sprintf("**评测时间**: %s  \n", r.StartedAt.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**耗时**: %s  \n", r.Duration.Round(100*time.Millisecond)))
	sb.WriteString(fmt.Sprintf("**总计**: %d 条 | ✅ 通过: %d | ❌ 失败: %d  \n\n",
		r.Total, r.Passed, r.Failed))

	// Average scores
	sb.WriteString("## 📊 综合评分\n\n")
	sb.WriteString("| 维度 | 平均分 |\n")
	sb.WriteString("|------|--------|\n")
	sb.WriteString(fmt.Sprintf("| 准确度 (Accuracy) | %.1f |\n", r.AvgScores.Accuracy))
	sb.WriteString(fmt.Sprintf("| 完整性 (Completeness) | %.1f |\n", r.AvgScores.Completeness))
	sb.WriteString(fmt.Sprintf("| 简洁度 (Conciseness) | %.1f |\n", r.AvgScores.Conciseness))
	sb.WriteString(fmt.Sprintf("| 工具选择 (Tool Selection) | %.1f |\n", r.AvgScores.ToolSelection))
	sb.WriteString(fmt.Sprintf("| 安全性 (Safety) | %.1f |\n", r.AvgScores.Safety))
	sb.WriteString(fmt.Sprintf("| **综合 (Overall)** | **%.1f** |\n\n", r.AvgScores.Overall))

	// Per-case details
	sb.WriteString("## 📋 用例详情\n\n")
	for _, detail := range r.Details {
		status := "✅"
		if detail.Assertion != nil && !detail.Assertion.Pass {
			status = "❌"
		}
		sb.WriteString(fmt.Sprintf("### %s [%s] %s\n\n", status, detail.Case.ID, detail.Case.Description))
		sb.WriteString(fmt.Sprintf("- **问题**: %s\n", detail.Case.Question))

		if detail.Record != nil {
			sb.WriteString(fmt.Sprintf("- **工具调用**: %d 次\n", len(detail.Record.ToolCalls)))
			for _, tc := range detail.Record.ToolCalls {
				errTag := ""
				if tc.Error != "" {
					errTag = fmt.Sprintf(" [错误: %s]", tc.Error)
				}
				sb.WriteString(fmt.Sprintf("  - Step %d: `%s(%s)` → %d 字%s (%.0fms)\n",
					tc.Step, tc.ToolName, truncate(tc.Arguments, 60),
					len([]rune(tc.Result)), errTag,
					float64(tc.Duration.Microseconds())/1000))
			}
			sb.WriteString(fmt.Sprintf("- **最终回答**: %d 字\n", len([]rune(detail.Record.FinalAnswer))))
			if detail.Record.Error != "" {
				sb.WriteString(fmt.Sprintf("- **运行错误**: %s\n", detail.Record.Error))
			}
		}

		if detail.Assertion != nil && len(detail.Assertion.Failures) > 0 {
			sb.WriteString("- **断言失败**:\n")
			for _, f := range detail.Assertion.Failures {
				sb.WriteString(fmt.Sprintf("  - ⚠️ %s\n", f))
			}
		}

		if detail.Judge != nil {
			sb.WriteString(fmt.Sprintf("- **评分**: 准确:%.1f 完整:%.1f 简洁:%.1f 工具:%.1f 安全:%.1f → **综合:%.1f**\n",
				detail.Judge.Scores.Accuracy,
				detail.Judge.Scores.Completeness,
				detail.Judge.Scores.Conciseness,
				detail.Judge.Scores.ToolSelection,
				detail.Judge.Scores.Safety,
				detail.Judge.Scores.Overall,
			))
			if detail.Judge.Reasoning != "" {
				sb.WriteString(fmt.Sprintf("- **裁判评语**: %s\n", detail.Judge.Reasoning))
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}
