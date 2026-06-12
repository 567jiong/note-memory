package service

import (
	"note-memory/internal/model"
	"testing"
)

// ---- parseVerdict tests ----

func TestParseVerdict_ValidJSON(t *testing.T) {
	raw := `{"sufficient": true, "reasoning": "信息充足", "missing": "", "rewritten_query": ""}`
	v, err := parseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Sufficient {
		t.Error("expected sufficient=true")
	}
	if v.Reasoning != "信息充足" {
		t.Errorf("expected reasoning='信息充足', got '%s'", v.Reasoning)
	}
}

func TestParseVerdict_InsufficientWithRewrite(t *testing.T) {
	raw := `{"sufficient": false, "reasoning": "缺少主角境界信息", "missing": "主角当前修为境界", "rewritten_query": "主角 境界 修为 突破"}`
	v, err := parseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Sufficient {
		t.Error("expected sufficient=false")
	}
	if v.RewrittenQuery != "主角 境界 修为 突破" {
		t.Errorf("unexpected rewritten_query: %s", v.RewrittenQuery)
	}
	if v.Missing != "主角当前修为境界" {
		t.Errorf("unexpected missing: %s", v.Missing)
	}
}

func TestParseVerdict_JSONWithFences(t *testing.T) {
	raw := "```json\n{\"sufficient\": true, \"reasoning\": \"ok\", \"missing\": \"\", \"rewritten_query\": \"\"}\n```"
	v, err := parseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Sufficient {
		t.Error("expected sufficient=true with json fences")
	}
}

func TestParseVerdict_JSONWithPlainFences(t *testing.T) {
	raw := "```\n{\"sufficient\": false, \"reasoning\": \"no\", \"missing\": \"x\", \"rewritten_query\": \"y\"}\n```"
	v, err := parseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Sufficient {
		t.Error("expected sufficient=false with plain fences")
	}
}

func TestParseVerdict_EmbeddedJSON(t *testing.T) {
	raw := "根据分析，检索结果不足。\n{\"sufficient\": false, \"reasoning\": \"信息不足\", \"missing\": \"关键事件\", \"rewritten_query\": \"关键事件 转折\"}\n建议重新检索。"
	v, err := parseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Sufficient {
		t.Error("expected sufficient=false for embedded JSON")
	}
	if v.RewrittenQuery != "关键事件 转折" {
		t.Errorf("unexpected rewritten_query: %s", v.RewrittenQuery)
	}
}

func TestParseVerdict_InvalidJSON(t *testing.T) {
	raw := "这不是合法的 JSON 格式"
	_, err := parseVerdict(raw)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseVerdict_EmptyString(t *testing.T) {
	_, err := parseVerdict("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

// ---- trimContextByScore tests ----

func makeResults(chapterNums []int, scores []float64, summaryLens []int) []SearchResult {
	var results []SearchResult
	for i, num := range chapterNums {
		// Build a summary of summaryLens[i] Chinese characters
		summary := ""
		for j := 0; j < summaryLens[i]; j++ {
			summary += "章"
		}
		results = append(results, SearchResult{
			Chapter: model.Chapter{
				ChapterNumber: num,
				Summary:       summary,
			},
			Score: scores[i],
		})
	}
	return results
}

func TestTrimContextByScore_UnderLimit(t *testing.T) {
	results := makeResults(
		[]int{1, 2, 3},
		[]float64{0.9, 0.8, 0.7},
		[]int{50, 50, 50}, // 150 total chars
	)
	trimmed := trimContextByScore(results, 300)
	if len(trimmed) != 3 {
		t.Errorf("expected 3 results (under limit), got %d", len(trimmed))
	}
}

func TestTrimContextByScore_OverLimit(t *testing.T) {
	results := makeResults(
		[]int{1, 2, 3, 4, 5},
		[]float64{0.5, 0.9, 0.3, 0.8, 0.1},
		[]int{100, 100, 100, 100, 100}, // 500 total chars
	)
	trimmed := trimContextByScore(results, 250)
	if len(trimmed) >= 5 {
		t.Errorf("expected fewer than 5 results (over limit), got %d", len(trimmed))
	}
	if len(trimmed) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(trimmed))
	}
	// Highest score chapters should be included: scores 0.9 and 0.8 (chapters 2 and 4)
	found := make(map[int]bool)
	for _, r := range trimmed {
		found[r.Chapter.ChapterNumber] = true
	}
	if !found[2] {
		t.Error("expected chapter 2 (highest score 0.9) to be included")
	}
	if !found[4] {
		t.Error("expected chapter 4 (second highest score 0.8) to be included")
	}
}

func TestTrimContextByScore_SortedByChapter(t *testing.T) {
	results := makeResults(
		[]int{5, 1, 3, 2, 4},
		[]float64{0.9, 0.8, 0.7, 0.6, 0.5},
		[]int{100, 100, 100, 100, 100},
	)
	trimmed := trimContextByScore(results, 200)
	// Results should be sorted by chapter number
	for i := 1; i < len(trimmed); i++ {
		if trimmed[i].Chapter.ChapterNumber < trimmed[i-1].Chapter.ChapterNumber {
			t.Errorf("results not sorted by chapter number at index %d: %d < %d",
				i, trimmed[i].Chapter.ChapterNumber, trimmed[i-1].Chapter.ChapterNumber)
		}
	}
}

func TestTrimContextByScore_EmptyResults(t *testing.T) {
	trimmed := trimContextByScore([]SearchResult{}, 100)
	if len(trimmed) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(trimmed))
	}
}

func TestTrimContextByScore_ZeroMaxChars(t *testing.T) {
	results := makeResults(
		[]int{1, 2},
		[]float64{0.9, 0.8},
		[]int{50, 50},
	)
	trimmed := trimContextByScore(results, 0)
	// Should still return something (at least 1 result to fill the cap)
	if len(trimmed) == 0 {
		t.Error("expected at least 1 result even with 0 maxChars")
	}
}

// ---- Round-trip: convert and back ----

func TestConvertSearchResults_RoundTrip(t *testing.T) {
	original := []SearchResult{
		{Chapter: model.Chapter{ChapterNumber: 1, Summary: "测试"}, Score: 0.95},
		{Chapter: model.Chapter{ChapterNumber: 2, Summary: "测试2"}, Score: 0.85},
	}
	converted := convertSearchResults(original)
	if len(converted) != 2 {
		t.Fatalf("expected 2 results, got %d", len(converted))
	}
	if converted[0].FinalScore != 0.95 {
		t.Errorf("expected score 0.95, got %f", converted[0].FinalScore)
	}
	if converted[1].Chapter.ChapterNumber != 2 {
		t.Errorf("expected chapter 2, got %d", converted[1].Chapter.ChapterNumber)
	}
}
