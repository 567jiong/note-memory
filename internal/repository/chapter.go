package repository

import (
	"note-memory/internal/model"

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
