package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/parser"
	"note-memory/internal/repository"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"gorm.io/gorm"
)

type NovelService struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	chapterSvc   *ChapterService
	aiClient     *ai.Client
}

func NewNovelService(
	db *gorm.DB,
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	chapterSvc *ChapterService,
	aiClient *ai.Client,
) *NovelService {
	return &NovelService{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		chapterSvc:   chapterSvc,
		aiClient:     aiClient,
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

	// Auto-detect GBK encoding (common in Chinese novels) and convert to UTF-8
	content := detectAndDecode(contentBytes)

	// LLM-first meta extraction with regex fallback
	title, author := llmExtractMeta(ctx, s.aiClient, content)
	if title == "" && filename != "" {
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
		Author:        author,
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
			Content:       truncateContent(pc.Content, 50000), // Store up to 50k chars per chapter
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

// FillEmbeddings triggers async embedding generation from chapter content.
func (s *NovelService) FillEmbeddings(novelID int64) {
	go s.chapterSvc.FillEmbeddings(context.Background(), novelID)
}

// detectAndDecode auto-detects GBK/GB18030 encoding and converts to UTF-8.
// Chinese novel TXT files are predominantly GBK-encoded.
func detectAndDecode(data []byte) string {
	// If already valid UTF-8, return as-is
	if utf8Valid(data) {
		return string(data)
	}
	// Try GBK → UTF-8
	reader := transform.NewReader(bytes.NewReader(data), simplifiedchinese.GBK.NewDecoder())
	decoded, err := io.ReadAll(reader)
	if err != nil {
		// Fallback: treat as raw bytes
		return string(data)
	}
	return string(decoded)
}

func utf8Valid(data []byte) bool {
	for i := 0; i < len(data); {
		r, size := decodeRune(data[i:])
		if r == 0xFFFD && size == 1 {
			return false
		}
		if size == 0 {
			return false
		}
		i += size
	}
	return true
}

func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0, 0
	}
	if b[0] < 0x80 {
		return rune(b[0]), 1
	}
	if b[0] < 0xC0 {
		return 0xFFFD, 1
	}
	if len(b) < 2 {
		return 0xFFFD, 0
	}
	if b[0] < 0xE0 {
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F), 2
	}
	if len(b) < 3 {
		return 0xFFFD, 0
	}
	if b[0] < 0xF0 {
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F), 3
	}
	if len(b) < 4 {
		return 0xFFFD, 0
	}
	return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F), 4
}

func truncateContent(content string, maxLen int) string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return string(runes[:maxLen]) + "\n\n... [内容已截断]"
}
