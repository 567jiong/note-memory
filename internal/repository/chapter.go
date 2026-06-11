package repository

import (
	"fmt"
	"note-memory/internal/model"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChapterRepo struct {
	db *gorm.DB
}

func NewChapterRepo(db *gorm.DB) *ChapterRepo {
	return &ChapterRepo{db: db}
}

// BatchCreate inserts chapters in batches, ignoring conflicts.
func (r *ChapterRepo) BatchCreate(chapters []model.Chapter) error {
	if len(chapters) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(chapters, 100).Error
}

func (r *ChapterRepo) GetByNovelAndNumber(novelID int64, chapterNumber int) (*model.Chapter, error) {
	var ch model.Chapter
	err := r.db.Where("novel_id = ? AND chapter_number = ?", novelID, chapterNumber).First(&ch).Error
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// ListByNovel returns chapters for a novel, ordered by chapter number.
func (r *ChapterRepo) ListByNovel(novelID int64) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("novel_id = ?", novelID).Order("chapter_number ASC").Find(&chapters).Error
	return chapters, err
}

// ListUpToChapter returns chapters from chapter 1 up to maxChapter (spoiler-free boundary).
func (r *ChapterRepo) ListUpToChapter(novelID int64, maxChapter int) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("novel_id = ? AND chapter_number <= ?", novelID, maxChapter).
		Order("chapter_number ASC").Find(&chapters).Error
	return chapters, err
}

// ListRecentChapters returns the last N chapters up to maxChapter.
func (r *ChapterRepo) ListRecentChapters(novelID int64, maxChapter int, n int) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("novel_id = ? AND chapter_number <= ?", novelID, maxChapter).
		Order("chapter_number DESC").Limit(n).Find(&chapters).Error
	if err != nil {
		return nil, err
	}
	// Reverse to chronological order
	for i, j := 0, len(chapters)-1; i < j; i, j = i+1, j-1 {
		chapters[i], chapters[j] = chapters[j], chapters[i]
	}
	return chapters, nil
}

// UpdateSummary updates the summary and extracted info for a chapter.
func (r *ChapterRepo) UpdateSummary(chapterID int64, summary string, characters model.JSONB, events model.JSONB) error {
	return r.db.Model(&model.Chapter{}).Where("id = ?", chapterID).Updates(map[string]interface{}{
		"summary":    summary,
		"characters": characters,
		"events":     events,
	}).Error
}

// CountByNovel returns the total number of chapters for a novel.
func (r *ChapterRepo) CountByNovel(novelID int64) (int64, error) {
	var count int64
	err := r.db.Model(&model.Chapter{}).Where("novel_id = ?", novelID).Count(&count).Error
	return count, err
}

// ListUnprocessed returns chapters that haven't been summarized yet.
func (r *ChapterRepo) ListUnprocessed(novelID int64, limit int) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("novel_id = ? AND summary = ''", novelID).
		Order("chapter_number ASC").Limit(limit).Find(&chapters).Error
	return chapters, err
}

// SearchSimilar performs cosine similarity search using pgvector.
// maxChapter enforces spoiler-free boundary. Returns chapters and similarity scores.
func (r *ChapterRepo) SearchSimilar(novelID int64, maxChapter int, queryVec pgvector.Vector, topK int) ([]model.Chapter, []float64, error) {
	type result struct {
		model.Chapter
		Score float64
	}

	var results []result
	err := r.db.Raw(`
		SELECT c.*, 1 - (c.embedding <=> ?) AS score
		FROM chapters c
		WHERE c.novel_id = ?
		  AND c.chapter_number <= ?
		  AND c.embedding IS NOT NULL
		ORDER BY c.embedding <=> ?
		LIMIT ?
	`, queryVec, novelID, maxChapter, queryVec, topK).Scan(&results).Error

	if err != nil {
		return nil, nil, fmt.Errorf("vector search: %w", err)
	}

	chapters := make([]model.Chapter, 0, len(results))
	scores := make([]float64, 0, len(results))
	for _, r := range results {
		chapters = append(chapters, r.Chapter)
		scores = append(scores, r.Score)
	}
	return chapters, scores, nil
}

// UpdateEmbedding updates a single chapter's embedding vector.
func (r *ChapterRepo) UpdateEmbedding(chapterID int64, vec []float32) error {
	return r.db.Model(&model.Chapter{}).Where("id = ?", chapterID).
		Update("embedding", pgvector.NewVector(vec)).Error
}

// BatchUpdateEmbedding updates embeddings for multiple chapters at once.
func (r *ChapterRepo) BatchUpdateEmbedding(chapterIDs []int64, embeddings []pgvector.Vector) error {
	if len(chapterIDs) != len(embeddings) {
		return fmt.Errorf("chapterIDs and embeddings length mismatch: %d vs %d", len(chapterIDs), len(embeddings))
	}
	for i, id := range chapterIDs {
		if err := r.db.Model(&model.Chapter{}).Where("id = ?", id).
			Update("embedding", embeddings[i]).Error; err != nil {
			return fmt.Errorf("update embedding for chapter %d: %w", id, err)
		}
	}
	return nil
}

// ListWithoutEmbedding returns chapters that have summaries but no embeddings.
func (r *ChapterRepo) ListWithoutEmbedding(novelID int64, limit int) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := r.db.Where("novel_id = ? AND summary != '' AND embedding IS NULL", novelID).
		Order("chapter_number ASC").Limit(limit).Find(&chapters).Error
	return chapters, err
}

// HybridSearch combines pgvector + tsvector search with weighted scoring.
// semanticWeight: 0.0~1.0, text weight = 1 - semanticWeight.
func (r *ChapterRepo) HybridSearch(novelID int64, maxChapter int, queryVec pgvector.Vector, tsQuery string, topK int) ([]model.HybridSearchResult, error) {
	const semWeight = 0.7

	type rawRow struct {
		model.Chapter
		SemScore  float64
		TextScore float64
	}

	var rows []rawRow

	// Use COALESCE to handle NULL tsv columns gracefully
	err := r.db.Raw(`
		SELECT c.*,
		       1 - (c.embedding <=> ?) AS sem_score,
		       COALESCE(ts_rank(c.tsv, to_tsquery('simple', ?)), 0) AS text_score
		FROM chapters c
		WHERE c.novel_id = ?
		  AND c.chapter_number <= ?
		  AND c.embedding IS NOT NULL
		ORDER BY (? * (1 - (c.embedding <=> ?)) + ? * COALESCE(ts_rank(c.tsv, to_tsquery('simple', ?)), 0)) DESC
		LIMIT ?
	`, queryVec, tsQuery, novelID, maxChapter, semWeight, queryVec, 1-semWeight, tsQuery, topK).Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	results := make([]model.HybridSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, model.HybridSearchResult{
			Chapter:    row.Chapter,
			SemScore:   row.SemScore,
			TextScore:  row.TextScore,
			FinalScore: semWeight*row.SemScore + (1-semWeight)*row.TextScore,
		})
	}
	return results, nil
}

// FullTextSearch performs pure tsvector full-text search (no embedding required).
// Used as fallback when embeddings are unavailable.
func (r *ChapterRepo) FullTextSearch(novelID int64, maxChapter int, tsQuery string, topK int) ([]model.HybridSearchResult, error) {
	type rawRow struct {
		model.Chapter
		TextScore float64
	}

	var rows []rawRow
	err := r.db.Raw(`
		SELECT c.*,
		       COALESCE(ts_rank(c.tsv, to_tsquery('simple', ?)), 0) AS text_score
		FROM chapters c
		WHERE c.novel_id = ?
		  AND c.chapter_number <= ?
		  AND c.tsv IS NOT NULL
		ORDER BY text_score DESC
		LIMIT ?
	`, tsQuery, novelID, maxChapter, topK).Scan(&rows).Error

	if err != nil {
		return nil, fmt.Errorf("fulltext search: %w", err)
	}

	results := make([]model.HybridSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, model.HybridSearchResult{
			Chapter:    row.Chapter,
			TextScore:  row.TextScore,
			FinalScore: row.TextScore,
		})
	}
	return results, nil
}

// UpdateSearchText updates the search_text and regenerates the tsvector.
func (r *ChapterRepo) UpdateSearchText(chapterID int64, searchText string) error {
	return r.db.Exec(`
		UPDATE chapters
		SET search_text = ?, tsv = to_tsvector('simple', ?)
		WHERE id = ?
	`, searchText, searchText, chapterID).Error
}

// ListAliases returns all canonical-name→aliases mappings for a novel.
func (r *ChapterRepo) ListAliases(novelID int64) ([]model.AliasInfo, error) {
	var items []model.EntityAlias
	err := r.db.Where("novel_id = ?", novelID).Find(&items).Error
	if err != nil {
		return nil, err
	}

	// Group by canonical name
	grouped := make(map[string]*model.AliasInfo)
	for _, item := range items {
		if _, ok := grouped[item.CanonicalName]; !ok {
			grouped[item.CanonicalName] = &model.AliasInfo{Name: item.CanonicalName}
		}
		info := grouped[item.CanonicalName]
		if item.Alias != item.CanonicalName {
			info.Aliases = append(info.Aliases, item.Alias)
		}
	}

	var result []model.AliasInfo
	for _, v := range grouped {
		result = append(result, *v)
	}
	return result, nil
}

// UpsertAliases inserts or updates entity aliases in bulk.
func (r *ChapterRepo) UpsertAliases(entries []model.EntityAlias) error {
	if len(entries) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "novel_id"}, {Name: "alias"}},
		DoNothing: true,
	}).CreateInBatches(entries, 200).Error
}
