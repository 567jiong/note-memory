package repository

import (
	"note-memory/internal/model"

	"gorm.io/gorm"
)

type NovelRepo struct {
	db *gorm.DB
}

func NewNovelRepo(db *gorm.DB) *NovelRepo {
	return &NovelRepo{db: db}
}

func (r *NovelRepo) Create(novel *model.Novel) error {
	return r.db.Create(novel).Error
}

func (r *NovelRepo) Update(novel *model.Novel) error {
	return r.db.Save(novel).Error
}

func (r *NovelRepo) GetByID(id int64) (*model.Novel, error) {
	var novel model.Novel
	err := r.db.First(&novel, id).Error
	if err != nil {
		return nil, err
	}
	return &novel, nil
}

func (r *NovelRepo) List() ([]model.Novel, error) {
	var novels []model.Novel
	err := r.db.Order("updated_at DESC").Find(&novels).Error
	return novels, err
}

func (r *NovelRepo) Delete(id int64) error {
	return r.db.Delete(&model.Novel{}, id).Error
}
