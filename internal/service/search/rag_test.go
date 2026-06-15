package search

import (
	"note-memory/internal/model"
	"testing"
)

// ---- convertSearchResults tests ----

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
