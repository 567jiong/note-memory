package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"note-memory/internal/model"
	"note-memory/internal/parser"
	"note-memory/internal/repository"

	"gorm.io/gorm"
)

type NovelService struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	chapterSvc   *ChapterService
}

func NewNovelService(
	db *gorm.DB,
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	chapterSvc *ChapterService,
) *NovelService {
	return &NovelService{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		chapterSvc:   chapterSvc,
	}
}

// UploadResult contains the result of uploading and parsing a novel.
type UploadResult struct {
	Novel    *model.Novel
	Chapters []model.Chapter
}

// Upload parses a TXT file, creates the novel record and saves chapters.
func (s *NovelService) Upload(ctx context.Context, file multipart.File, filename string) (*UploadResult, error) {
	contentBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	content := string(contentBytes)

	title := parser.DetectNovelTitle(content)
	if title == "未命名小说" && filename != "" {
		// Use filename without extension as fallback
		name := filename
		for i := len(name) - 1; i >= 0; i-- {
			if name[i] == '.' {
				name = name[:i]
				break
			}
		}
		if name != "" {
			title = name
		}
	}

	parsedChapters := parser.Parse(content)

	novel := &model.Novel{
		Title:         title,
		TotalChapters: len(parsedChapters),
	}
	if err := s.novelRepo.Create(novel); err != nil {
		return nil, fmt.Errorf("create novel: %w", err)
	}

	// Build chapter models
	chapters := make([]model.Chapter, 0, len(parsedChapters))
	for _, pc := range parsedChapters {
		if pc.Number <= 0 {
			continue // skip preamble
		}
		chapters = append(chapters, model.Chapter{
			NovelID:       novel.ID,
			ChapterNumber: pc.Number,
			Title:         pc.Title,
			Content:       truncateContent(pc.Content, 5000), // Store up to 5k chars per chapter
		})
	}

	if err := s.chapterRepo.BatchCreate(chapters); err != nil {
		return nil, fmt.Errorf("save chapters: %w", err)
	}

	// Set initial progress to chapter 1
	_ = s.progressRepo.Upsert(novel.ID, 1)

	novel.TotalChapters = len(chapters)
	_ = s.novelRepo.Update(novel)

	return &UploadResult{
		Novel:    novel,
		Chapters: chapters,
	}, nil
}

// GetNovel returns a novel with its chapter list.
func (s *NovelService) GetNovel(novelID int64) (*model.Novel, []model.Chapter, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return nil, nil, err
	}
	chapters, err := s.chapterRepo.ListByNovel(novelID)
	if err != nil {
		return nil, nil, err
	}
	return novel, chapters, nil
}

// ListNovels returns all novels.
func (s *NovelService) ListNovels() ([]model.Novel, error) {
	return s.novelRepo.List()
}

// UpdateProgress sets the reading progress for a novel.
func (s *NovelService) UpdateProgress(novelID int64, chapter int) error {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return err
	}
	if chapter < 1 || chapter > novel.TotalChapters {
		return fmt.Errorf("chapter %d out of range [1, %d]", chapter, novel.TotalChapters)
	}
	return s.progressRepo.Upsert(novelID, chapter)
}

// GetProgress returns the reading progress for a novel.
func (s *NovelService) GetProgress(novelID int64) (*model.ReadingProgress, error) {
	return s.progressRepo.GetByNovel(novelID)
}

// StartParse triggers async AI parsing for all unprocessed chapters.
func (s *NovelService) StartParse(novelID int64) {
	go s.chapterSvc.ParseAllChapters(context.Background(), novelID)
}

func truncateContent(content string, maxLen int) string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "\n\n... [内容已截断]"
}
