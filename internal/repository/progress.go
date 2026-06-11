package repository

import (
	"note-memory/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProgressRepo struct {
	db *gorm.DB
}

func NewProgressRepo(db *gorm.DB) *ProgressRepo {
	return &ProgressRepo{db: db}
}

func (r *ProgressRepo) Upsert(novelID int64, chapter int) error {
	p := model.ReadingProgress{
		NovelID:        novelID,
		CurrentChapter: chapter,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "novel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"current_chapter", "updated_at"}),
	}).Create(&p).Error
}

func (r *ProgressRepo) GetByNovel(novelID int64) (*model.ReadingProgress, error) {
	var p model.ReadingProgress
	err := r.db.Where("novel_id = ?", novelID).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}
