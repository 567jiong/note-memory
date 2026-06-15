package service

import (
	"context"
	"fmt"
	"note-memory/internal/model"
	"note-memory/internal/repository"
)

// QAService handles spoiler-free question answering.
type QAService struct {
	novelRepo    *repository.NovelRepo
	progressRepo *repository.ProgressRepo
	searchSvc    *SearchService

	// askFunc is the agent entry point, wired by main.go.
	// It abstracts the Eino ADK agent behind a plain function to avoid import cycles.
	askFunc func(ctx context.Context, novelID int64, maxChapter int, novelTitle, question string) (string, error)
}

func NewQAService(
	novelRepo *repository.NovelRepo,
	progressRepo *repository.ProgressRepo,
	searchSvc *SearchService,
) *QAService {
	return &QAService{
		novelRepo:    novelRepo,
		progressRepo: progressRepo,
		searchSvc:    searchSvc,
	}
}

// SetAskFunc injects the agent entry function (wired from main.go).
func (s *QAService) SetAskFunc(fn func(ctx context.Context, novelID int64, maxChapter int, novelTitle, question string) (string, error)) {
	s.askFunc = fn
}

// AskQuestion answers a user question using the Eino ADK Reading Memory Agent.
func (s *QAService) AskQuestion(ctx context.Context, novelID int64, question string) (string, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return "", fmt.Errorf("get novel: %w", err)
	}

	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return "", fmt.Errorf("get progress: %w", err)
	}

	if s.askFunc == nil {
		return "", fmt.Errorf("agent not configured")
	}

	return s.askFunc(ctx, novelID, progress.CurrentChapter, novel.Title, question)
}

// SearchChapters performs semantic search and returns formatted results.
func (s *QAService) SearchChapters(ctx context.Context, novelID int64, query string) ([]model.Chapter, []float64, error) {
	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return nil, nil, fmt.Errorf("get progress: %w", err)
	}

	results, err := s.searchSvc.HybridSearch(ctx, query, novelID, progress.CurrentChapter, 10)
	if err != nil {
		return nil, nil, err
	}

	chapters := make([]model.Chapter, 0, len(results))
	scores := make([]float64, 0, len(results))
	for _, r := range results {
		chapters = append(chapters, r.Chapter)
		scores = append(scores, r.FinalScore)
	}
	return chapters, scores, nil
}
