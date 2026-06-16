package search

import (
	"note-memory/internal/model"
	"testing"
)

func TestApplyRRF_BothRankings(t *testing.T) {
	// Chapters ranked #1 in both lists should get the highest RRF score.
	ch1 := model.Chapter{ID: 1, ChapterNumber: 1}
	ch2 := model.Chapter{ID: 2, ChapterNumber: 2}
	ch3 := model.Chapter{ID: 3, ChapterNumber: 3}

	semChapters := []model.Chapter{ch1, ch2, ch3}
	semScores := []float64{0.95, 0.85, 0.75}

	ftResults := []model.HybridSearchResult{
		{Chapter: ch1, FinalScore: 0.9},
		{Chapter: ch2, FinalScore: 0.8},
		{Chapter: ch3, FinalScore: 0.7},
	}

	results := applyRRF(semChapters, semScores, ftResults, 60)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// ch1 should be #1 (top in both lists)
	if results[0].Chapter.ID != 1 {
		t.Errorf("expected chapter 1 first, got chapter %d", results[0].Chapter.ID)
	}
	// ch2 #2
	if results[1].Chapter.ID != 2 {
		t.Errorf("expected chapter 2 second, got chapter %d", results[1].Chapter.ID)
	}
	// ch3 #3
	if results[2].Chapter.ID != 3 {
		t.Errorf("expected chapter 3 third, got chapter %d", results[2].Chapter.ID)
	}

	// Verify RRF score for ch1 (rank 1 in both): 1/61 + 1/61 ≈ 0.03279
	expected := 1.0/61.0 + 1.0/61.0
	if abs(results[0].FinalScore-expected) > 0.0001 {
		t.Errorf("ch1 RRF = %f, want %f", results[0].FinalScore, expected)
	}
}

func TestApplyRRF_DisjointRankings(t *testing.T) {
	// Chapter 1 only in semantic, chapter 2 only in full-text.
	ch1 := model.Chapter{ID: 1, ChapterNumber: 1}
	ch2 := model.Chapter{ID: 2, ChapterNumber: 2}

	semChapters := []model.Chapter{ch1}
	semScores := []float64{0.95}

	ftResults := []model.HybridSearchResult{
		{Chapter: ch2, FinalScore: 0.9},
	}

	results := applyRRF(semChapters, semScores, ftResults, 60)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Both should have the same RRF score (both ranked #1 in one list, absent in the other).
	if abs(results[0].FinalScore-results[1].FinalScore) > 0.0001 {
		t.Errorf("expected equal scores for disjoint #1 ranks, got %f and %f",
			results[0].FinalScore, results[1].FinalScore)
	}

	// ch1: semContrib = 1/61, textContrib = 0
	if results[0].Chapter.ID == 1 {
		if abs(results[0].SemScore-1.0/61.0) > 0.0001 {
			t.Errorf("ch1 semContrib = %f, want %f", results[0].SemScore, 1.0/61.0)
		}
		if results[0].TextScore != 0 {
			t.Errorf("ch1 textContrib = %f, want 0", results[0].TextScore)
		}
	}
}

func TestApplyRRF_RankDelta(t *testing.T) {
	// Chapter ranked #1 in semantic but #10 in full-text should rank
	// behind a chapter ranked #2 in both.
	ch1 := model.Chapter{ID: 1, ChapterNumber: 1} // rank 1 sem, rank 10 text
	ch2 := model.Chapter{ID: 2, ChapterNumber: 2} // rank 2 sem, rank 2 text

	semChapters := []model.Chapter{ch1, ch2}
	semScores := []float64{0.95, 0.85}

	ftResults := []model.HybridSearchResult{
		{Chapter: ch2, FinalScore: 0.9},           // rank 1
		{Chapter: model.Chapter{ID: 99}, FinalScore: 0.1}, // rank 2 (dummy, not in sem)
		{Chapter: model.Chapter{ID: 100}, FinalScore: 0.1}, // rank 3
		{Chapter: model.Chapter{ID: 101}, FinalScore: 0.1}, // rank 4
		{Chapter: model.Chapter{ID: 102}, FinalScore: 0.1}, // rank 5
		{Chapter: model.Chapter{ID: 103}, FinalScore: 0.1}, // rank 6
		{Chapter: model.Chapter{ID: 104}, FinalScore: 0.1}, // rank 7
		{Chapter: model.Chapter{ID: 105}, FinalScore: 0.1}, // rank 8
		{Chapter: model.Chapter{ID: 106}, FinalScore: 0.1}, // rank 9
		{Chapter: ch1, FinalScore: 0.01},                    // rank 10
	}

	results := applyRRF(semChapters, semScores, ftResults, 60)

	// ch2 (rank 2 + rank 1): 1/62 + 1/61 ≈ 0.01613 + 0.01639 = 0.03252
	// ch1 (rank 1 + rank 10): 1/61 + 1/70 ≈ 0.01639 + 0.01429 = 0.03068
	// ch2 should rank higher.
	if results[0].Chapter.ID != 2 {
		t.Errorf("expected chapter 2 first, got chapter %d (score=%f)", results[0].Chapter.ID, results[0].FinalScore)
	}
	if results[1].Chapter.ID != 1 {
		t.Errorf("expected chapter 1 second, got chapter %d (score=%f)", results[1].Chapter.ID, results[1].FinalScore)
	}
}

func TestApplyRRF_EmptyInputs(t *testing.T) {
	// Both empty → empty result.
	results := applyRRF(nil, nil, nil, 60)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	// Only semantic results.
	ch1 := model.Chapter{ID: 1, ChapterNumber: 1}
	semChapters := []model.Chapter{ch1}
	semScores := []float64{0.95}

	results = applyRRF(semChapters, semScores, nil, 60)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FinalScore != 1.0/61.0 {
		t.Errorf("expected %f, got %f", 1.0/61.0, results[0].FinalScore)
	}

	// Only full-text results.
	ftResults := []model.HybridSearchResult{
		{Chapter: ch1, FinalScore: 0.9},
	}
	results = applyRRF(nil, nil, ftResults, 60)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].FinalScore != 1.0/61.0 {
		t.Errorf("expected %f, got %f", 1.0/61.0, results[0].FinalScore)
	}
}

func TestApplyRRF_KValueEffect(t *testing.T) {
	// Larger k reduces the impact of rank differences.
	ch1 := model.Chapter{ID: 1, ChapterNumber: 1}
	ch2 := model.Chapter{ID: 2, ChapterNumber: 2}

	semChapters := []model.Chapter{ch1, ch2}
	semScores := []float64{0.95, 0.85}

	ftResults := []model.HybridSearchResult{
		{Chapter: ch1, FinalScore: 0.9},
		{Chapter: ch2, FinalScore: 0.8},
	}

	// With k=0, rank differences are maximal.
	resultsK0 := applyRRF(semChapters, semScores, ftResults, 0)
	ratio0 := resultsK0[0].FinalScore / resultsK0[1].FinalScore

	// With k=1000, scores are almost equal.
	resultsK1000 := applyRRF(semChapters, semScores, ftResults, 1000)
	ratio1000 := resultsK1000[0].FinalScore / resultsK1000[1].FinalScore

	if ratio0 <= ratio1000 {
		t.Errorf("k=0 ratio (%f) should be > k=1000 ratio (%f)", ratio0, ratio1000)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
