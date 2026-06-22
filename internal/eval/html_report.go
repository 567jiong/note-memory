package eval

import (
	"fmt"
	"html"
	"os"
	"strings"
	"time"
)

// SaveHTMLReport generates a self-contained HTML evaluation report and writes it to path.
func SaveHTMLReport(r *Report, path string) error {
	html := buildHTMLReport(r)
	return os.WriteFile(path, []byte(html), 0644)
}

func buildHTMLReport(r *Report) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Agent 评测报告</title>
<style>
  :root { --pass: #22c55e; --fail: #ef4444; --warn: #f59e0b; --bg: #f8fafc; --card: #fff; --text: #1e293b; --muted: #64748b; --border: #e2e8f0; --blue: #3b82f6; }
  * { margin:0; padding:0; box-sizing:border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: var(--bg); color: var(--text); line-height: 1.6; padding: 24px; }
  .container { max-width: 1100px; margin: 0 auto; }
  h1 { font-size: 28px; margin-bottom: 4px; }
  .subtitle { color: var(--muted); margin-bottom: 24px; }
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 16px; margin-bottom: 32px; }
  .stat-card { background: var(--card); border-radius: 12px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); text-align: center; }
  .stat-card .value { font-size: 32px; font-weight: 700; }
  .stat-card .label { font-size: 13px; color: var(--muted); margin-top: 4px; }
  .stat-card.pass .value { color: var(--pass); }
  .stat-card.fail .value { color: var(--fail); }

  .scores-section { background: var(--card); border-radius: 12px; padding: 24px; margin-bottom: 32px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  .scores-section h2 { font-size: 18px; margin-bottom: 20px; }
  .score-bars { display: flex; flex-direction: column; gap: 12px; }
  .score-row { display: grid; grid-template-columns: 120px 1fr 50px; align-items: center; gap: 12px; }
  .score-label { font-size: 14px; font-weight: 500; }
  .bar-track { background: #f1f5f9; border-radius: 6px; height: 20px; overflow: hidden; }
  .bar-fill { height: 100%; border-radius: 6px; transition: width .3s; }
  .bar-fill.high { background: var(--pass); }
  .bar-fill.mid { background: var(--warn); }
  .bar-fill.low { background: var(--fail); }
  .score-val { font-weight: 700; font-size: 15px; text-align: right; }

  .case-card { background: var(--card); border-radius: 12px; margin-bottom: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); overflow: hidden; }
  .case-header { padding: 16px 20px; display: flex; align-items: center; gap: 12px; cursor: pointer; user-select: none; border-bottom: 1px solid transparent; }
  .case-header:hover { background: #fafafa; }
  .case-card.open .case-header { border-bottom-color: var(--border); }
  .case-status { width: 28px; height: 28px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 16px; flex-shrink: 0; }
  .case-status.pass { background: #dcfce7; }
  .case-status.fail { background: #fee2e2; }
  .case-id { font-size: 11px; color: var(--muted); font-weight: 600; letter-spacing: .5px; }
  .case-title { font-weight: 600; flex: 1; }
  .case-scores { display: flex; gap: 14px; font-size: 13px; }
  .case-scores span { white-space: nowrap; }
  .case-arrow { color: var(--muted); transition: transform .2s; }
  .case-card.open .case-arrow { transform: rotate(180deg); }
  .case-body { display: none; padding: 0 20px 20px; }
  .case-card.open .case-body { display: block; }

  .detail-row { margin-bottom: 16px; }
  .detail-label { font-size: 12px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: .5px; margin-bottom: 6px; }
  .tool-table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .tool-table th { background: #f8fafc; text-align: left; padding: 8px 12px; font-weight: 600; font-size: 11px; color: var(--muted); text-transform: uppercase; }
  .tool-table td { padding: 8px 12px; border-top: 1px solid var(--border); vertical-align: top; }
  .tool-table .tool-name { font-family: "SF Mono", "Fira Code", monospace; font-size: 12px; font-weight: 600; color: var(--blue); }
  .tool-table .tool-args { font-family: "SF Mono", "Fira Code", monospace; font-size: 12px; max-width: 280px; word-break: break-all; color: var(--muted); }
  .tool-table .tool-result { max-width: 320px; word-break: break-all; color: var(--muted); }
  .tool-table .tool-dur { white-space: nowrap; color: var(--muted); font-size: 12px; }
  .tool-err { color: var(--fail); font-size: 12px; }

  .thinking-block { background: #fffbeb; border: 1px solid #fde68a; border-radius: 8px; padding: 12px 16px; margin-bottom: 8px; font-size: 13px; max-height: 200px; overflow-y: auto; white-space: pre-wrap; }
  .answer-block { background: #f0fdf4; border: 1px solid #bbf7d0; border-radius: 8px; padding: 12px 16px; font-size: 14px; max-height: 300px; overflow-y: auto; white-space: pre-wrap; line-height: 1.7; }

  .failures { margin-bottom: 12px; }
  .failure { background: #fef2f2; border-left: 3px solid var(--fail); padding: 6px 12px; margin-bottom: 4px; font-size: 13px; border-radius: 0 6px 6px 0; }

  .judge-reasoning { background: #eff6ff; border: 1px solid #bfdbfe; border-radius: 8px; padding: 12px 16px; font-size: 13px; color: #1e40af; margin-top: 8px; }

  .footer { text-align: center; color: var(--muted); font-size: 12px; margin-top: 40px; padding: 20px 0; }

  @media (max-width: 640px) {
    body { padding: 12px; }
    .summary-grid { grid-template-columns: repeat(2, 1fr); }
    .score-row { grid-template-columns: 80px 1fr 40px; }
    .case-scores { display: none; }
  }
</style>
</head>
<body>
<div class="container">
`)
	// ── Header ──
	b.WriteString(fmt.Sprintf(`<h1>📊 Agent 评测报告</h1>
<div class="subtitle">%s · 耗时 %s · %d 条用例</div>
`, r.StartedAt.Format("2006-01-02 15:04:05"), r.Duration.Round(100*time.Millisecond), r.Total))

	// ── Summary grid ──
	b.WriteString(`<div class="summary-grid">`)
	writeStatCard(&b, "总计", fmt.Sprintf("%d", r.Total), "")
	writeStatCard(&b, "✅ 通过", fmt.Sprintf("%d", r.Passed), "pass")
	writeStatCard(&b, "❌ 失败", fmt.Sprintf("%d", r.Failed), "fail")
	writeStatCard(&b, "综合评分", fmt.Sprintf("%.1f", r.AvgScores.Overall), "")
	b.WriteString(`</div>`)

	// ── Score bars ──
	b.WriteString(`<div class="scores-section">
<h2>📈 维度均分</h2>
<div class="score-bars">`)
	writeScoreBar(&b, "准确度 (Accuracy)", r.AvgScores.Accuracy)
	writeScoreBar(&b, "完整性 (Completeness)", r.AvgScores.Completeness)
	writeScoreBar(&b, "简洁度 (Conciseness)", r.AvgScores.Conciseness)
	writeScoreBar(&b, "工具选择 (Tool Selection)", r.AvgScores.ToolSelection)
	writeScoreBar(&b, "安全性 (Safety)", r.AvgScores.Safety)
	writeScoreBar(&b, "综合 (Overall)", r.AvgScores.Overall)
	b.WriteString(`</div></div>`)

	// ── Per-case details ──
	b.WriteString(`<h2 style="font-size:18px;margin-bottom:16px;">📋 用例详情</h2>`)
	for _, d := range r.Details {
		writeCaseCard(&b, d)
	}

	// ── Footer ──
	b.WriteString(`<div class="footer">Generated by note-memory eval framework</div>
</div>
<script>
document.querySelectorAll('.case-header').forEach(h => {
  h.addEventListener('click', () => h.parentElement.classList.toggle('open'));
});
</script>
</body></html>`)

	return b.String()
}

// ── helpers ──

func writeStatCard(b *strings.Builder, label, value, cls string) {
	b.WriteString(fmt.Sprintf(`<div class="stat-card %s"><div class="value">%s</div><div class="label">%s</div></div>`, cls, value, label))
}

func writeScoreBar(b *strings.Builder, label string, score float64) {
	pct := score / 5.0 * 100
	cls := "low"
	if pct >= 70 {
		cls = "high"
	} else if pct >= 40 {
		cls = "mid"
	}
	b.WriteString(fmt.Sprintf(`
<div class="score-row">
  <span class="score-label">%s</span>
  <div class="bar-track"><div class="bar-fill %s" style="width:%.0f%%"></div></div>
  <span class="score-val">%.1f</span>
</div>`, label, cls, pct, score))
}

func writeCaseCard(b *strings.Builder, d *EvalResult) {
	pass := d.Assertion != nil && d.Assertion.Pass
	statusCls := "pass"
	statusIcon := "✓"
	if !pass {
		statusCls = "fail"
		statusIcon = "✗"
	}

	b.WriteString(fmt.Sprintf(`
<div class="case-card">
<div class="case-header">
  <div class="case-status %s">%s</div>
  <div>
    <div class="case-id">%s</div>
    <div class="case-title">%s</div>
  </div>`, statusCls, statusIcon, html.EscapeString(d.Case.ID), html.EscapeString(d.Case.Description)))

	// Mini scores in header
	if d.Judge != nil {
		b.WriteString(fmt.Sprintf(`
  <div class="case-scores">
    <span>准确:%.1f</span><span>完整:%.1f</span><span>简洁:%.1f</span><span>工具:%.1f</span><span>安全:%.1f</span><span style="font-weight:700">综合:%.1f</span>
  </div>`,
			d.Judge.Scores.Accuracy, d.Judge.Scores.Completeness, d.Judge.Scores.Conciseness,
			d.Judge.Scores.ToolSelection, d.Judge.Scores.Safety, d.Judge.Scores.Overall))
	}

	b.WriteString(`<span class="case-arrow">▼</span></div><div class="case-body">`)

	// Question
	b.WriteString(fmt.Sprintf(`<div class="detail-row"><div class="detail-label">问题</div><div>%s</div></div>`, html.EscapeString(d.Case.Question)))

	// Assertion failures
	if d.Assertion != nil && len(d.Assertion.Failures) > 0 {
		b.WriteString(`<div class="failures">`)
		for _, f := range d.Assertion.Failures {
			b.WriteString(fmt.Sprintf(`<div class="failure">⚠️ %s</div>`, html.EscapeString(f)))
		}
		b.WriteString(`</div>`)
	}

	// Tool calls table
	if d.Record != nil && len(d.Record.ToolCalls) > 0 {
		b.WriteString(`<div class="detail-row"><div class="detail-label">工具调用 (`)
		b.WriteString(fmt.Sprintf("%d 次)</div>", len(d.Record.ToolCalls)))
		b.WriteString(`<table class="tool-table"><tr><th>步骤</th><th>工具</th><th>参数</th><th>结果</th><th>耗时</th></tr>`)
		for _, tc := range d.Record.ToolCalls {
			errHTML := ""
			if tc.Error != "" {
				errHTML = fmt.Sprintf(`<br><span class="tool-err">错误: %s</span>`, html.EscapeString(tc.Error))
			}
			b.WriteString(fmt.Sprintf(`<tr>
				<td>%d</td>
				<td class="tool-name">%s</td>
				<td class="tool-args">%s</td>
				<td class="tool-result">%s%s</td>
				<td class="tool-dur">%.0fms</td>
			</tr>`, tc.Step, html.EscapeString(tc.ToolName),
				html.EscapeString(truncate(tc.Arguments, 120)),
				html.EscapeString(truncate(tc.Result, 200)),
				errHTML,
				float64(tc.Duration.Microseconds())/1000))
		}
		b.WriteString(`</table></div>`)
	}

	// Thinking blocks
	if d.Record != nil && len(d.Record.Thinking) > 0 {
		b.WriteString(fmt.Sprintf(`<div class="detail-row"><div class="detail-label">思考过程 (%d 段)</div>`, len(d.Record.Thinking)))
		for i, t := range d.Record.Thinking {
			if t == "" {
				continue
			}
			b.WriteString(fmt.Sprintf(`<div class="thinking-block">[%d] %s</div>`, i+1, html.EscapeString(t)))
		}
		b.WriteString(`</div>`)
	}

	// Final answer
	if d.Record != nil && d.Record.FinalAnswer != "" {
		b.WriteString(fmt.Sprintf(`<div class="detail-row"><div class="detail-label">最终回答 (%d 字)</div><div class="answer-block">%s</div></div>`,
			len([]rune(d.Record.FinalAnswer)), html.EscapeString(d.Record.FinalAnswer)))
	}

	// Judge reasoning
	if d.Judge != nil && d.Judge.Reasoning != "" {
		b.WriteString(fmt.Sprintf(`<div class="judge-reasoning"><strong>🧠 裁判评语：</strong>%s</div>`, html.EscapeString(d.Judge.Reasoning)))
	}

	b.WriteString(`</div></div>`)
}
