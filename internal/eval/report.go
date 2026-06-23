package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// PrintConsoleSummary prints a compact console summary of an evaluation report.
func PrintConsoleSummary(r *Report) {
	fmt.Println()
	fmt.Println(strings.Repeat("═", 72))
	fmt.Println("📊 汇总")
	fmt.Println(strings.Repeat("═", 72))

	if r.Total == 0 {
		fmt.Println("  无测试用例")
		return
	}

	pct := float64(r.Passed) / float64(r.Total) * 100
	fmt.Printf("  通过: %d/%d (%.0f%%)\n", r.Passed, r.Total, pct)
	fmt.Printf("  总耗时: %v\n", r.Duration.Round(time.Millisecond))

	var totalTools, totalChars int
	for _, d := range r.Details {
		if d.Record != nil {
			totalTools += len(d.Record.ToolCalls)
			totalChars += len([]rune(d.Record.FinalAnswer))
		}
	}
	avgTools := float64(totalTools) / float64(r.Total)
	avgChars := float64(totalChars) / float64(r.Total)
	fmt.Printf("  工具调用总数: %d (均 %.1f/题)\n", totalTools, avgTools)
	fmt.Printf("  答案总字数: %d (均 %.0f/题)\n", totalChars, avgChars)

	if r.AvgScores.Overall > 0 {
		fmt.Println()
		fmt.Printf("  LLM裁判均分: 准确:%.1f 完整:%.1f 简洁:%.1f 工具:%.1f 安全:%.1f → 综合:%.1f\n",
			r.AvgScores.Accuracy, r.AvgScores.Completeness, r.AvgScores.Conciseness,
			r.AvgScores.ToolSelection, r.AvgScores.Safety, r.AvgScores.Overall)
	}

	fmt.Println(strings.Repeat("═", 72))

	// Detail table
	fmt.Println()
	fmt.Printf("%-12s %-30s %6s %8s %5s %s\n", "ID", "描述", "工具数", "耗时", "断言", "期望工具")
	fmt.Println(strings.Repeat("─", 90))
	for _, d := range r.Details {
		status := "✅"
		if d.Assertion == nil || !d.Assertion.Pass {
			status = "❌"
		}
		expectStr := ""
		if len(d.Case.ExpectedTools) > 0 {
			expectStr = strings.Join(d.Case.ExpectedTools, ",")
		} else if len(d.Case.ForbiddenTools) > 0 {
			expectStr = "禁:" + strings.Join(d.Case.ForbiddenTools, ",")
		}
		desc := d.Case.Description
		descRunes := []rune(desc)
		if len(descRunes) > 28 {
			desc = string(descRunes[:28]) + "…"
		}
		toolN := 0
		dur := time.Duration(0)
		if d.Record != nil {
			toolN = len(d.Record.ToolCalls)
			dur = d.Record.Duration
		}
		fmt.Printf("%-12s %-30s %6d %8s %5s %s\n",
			d.Case.ID, desc,
			toolN,
			dur.Round(time.Millisecond).String(),
			status, expectStr,
		)
	}
}

// SaveReport writes an evaluation report to a JSON file.
func SaveReport(r *Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, b, 0644)
}

