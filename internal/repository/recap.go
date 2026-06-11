package repository

import (
	"note-memory/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RecapRepo struct {
	db *gorm.DB
}

func NewRecapRepo(db *gorm.DB) *RecapRepo {
	return &RecapRepo{db: db}
}

func (r *RecapRepo) Upsert(novelID int64, chapter int, content string) error {
	recap := model.Recap{
		NovelID:        novelID,
		CurrentChapter: chapter,
		RecapContent:   content,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "novel_id"}, {Name: "current_chapter"}},
		DoUpdates: clause.AssignmentColumns([]string{"recap_content", "created_at"}),
	}).Create(&recap).Error
}

func (r *RecapRepo) GetByNovelAndChapter(novelID int64, chapter int) (*model.Recap, error) {
	var recap model.Recap
	err := r.db.Where("novel_id = ? AND current_chapter = ?", novelID, chapter).First(&recap).Error
	if err != nil {
		return nil, err
	}
	return &recap, nil
}
