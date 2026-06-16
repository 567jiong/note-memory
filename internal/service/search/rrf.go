package search

import (
	"note-memory/internal/model"
	"sort"
)

// applyRRF combines semantic and full-text chapter rankings using Reciprocal Rank Fusion.
//
// k is the smoothing constant. Standard practice uses k=60, which gives the top-ranked
// document ~0.016 and the 60th-ranked document ~0.008 — mid-ranked documents still
// contribute meaningfully while preventing any single ranking from dominating.
//
// Formula: RRF_score(d) = Σ over each ranking r: 1 / (k + rank_r(d))
//
// If a chapter appears in only one ranking, its contribution from the other is 0.
func applyRRF(
	semChapters []model.Chapter,
	semScores []float64,
	ftResults []model.HybridSearchResult,
	k int,
) []model.HybridSearchResult {
	// Build rank maps from sorted input lists (1-indexed).
	semRank := make(map[int64]int, len(semChapters))
	for i, ch := range semChapters {
		semRank[ch.ID] = i + 1
	}
	textRank := make(map[int64]int, len(ftResults))
	for i, r := range ftResults {
		textRank[r.Chapter.ID] = i + 1
	}

	// Build lookup: chapterID → chapter + original scores
	semScoreByID := make(map[int64]float64, len(semChapters))
	for i, ch := range semChapters {
		semScoreByID[ch.ID] = semScores[i]
	}
	textScoreByID := make(map[int64]float64, len(ftResults))
	chapterByID := make(map[int64]model.Chapter, len(semChapters)+len(ftResults))
	for _, r := range ftResults {
		textScoreByID[r.Chapter.ID] = r.FinalScore
		chapterByID[r.Chapter.ID] = r.Chapter
	}
	for _, ch := range semChapters {
		if _, ok := chapterByID[ch.ID]; !ok {
			chapterByID[ch.ID] = ch
		}
	}

	// Compute RRF scores for the union of both ranked lists.
	type rrfEntry struct {
		chapterID   int64
		rrfScore    float64
		semContrib  float64
		textContrib float64
	}
	entries := make([]rrfEntry, 0, len(chapterByID))

	kf := float64(k)
	for chID := range chapterByID {
		var sc, tc float64
		if rank, ok := semRank[chID]; ok {
			sc = 1.0 / (kf + float64(rank))
		}
		if rank, ok := textRank[chID]; ok {
			tc = 1.0 / (kf + float64(rank))
		}
		entries = append(entries, rrfEntry{
			chapterID:   chID,
			rrfScore:    sc + tc,
			semContrib:  sc,
			textContrib: tc,
		})
	}

	// Sort by RRF score descending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rrfScore > entries[j].rrfScore
	})

	// Convert to HybridSearchResult.
	results := make([]model.HybridSearchResult, 0, len(entries))
	for _, e := range entries {
		results = append(results, model.HybridSearchResult{
			Chapter:    chapterByID[e.chapterID],
			SemScore:   e.semContrib,
			TextScore:  e.textContrib,
			FinalScore: e.rrfScore,
		})
	}
	return results
}
