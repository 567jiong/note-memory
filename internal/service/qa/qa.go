package qa

import (
	"context"
	"fmt"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/search"
	"note-memory/internal/service/tools"

	einomodel "github.com/cloudwego/eino/components/model"
)

// Service handles spoiler-free question answering via the Reading Memory ADK agent.
// Agent creation is self-contained — no external wiring needed.
type Service struct {
	novelRepo    *repository.NovelRepo
	progressRepo *repository.ProgressRepo
	chatModel    einomodel.ToolCallingChatModel
	searchSvc    *search.Service
	graphReader  *graph.GraphReader
	entitySvc    *entity.Service
}

// NewService creates a fully-wired QA service.
// All agent dependencies are injected at construction; the agent is created
// per-request with request-scoped parameters (novelID, maxChapter).
func NewService(
	novelRepo *repository.NovelRepo,
	progressRepo *repository.ProgressRepo,
	chatModel einomodel.ToolCallingChatModel,
	searchSvc *search.Service,
	graphReader *graph.GraphReader,
	entitySvc *entity.Service,
) *Service {
	return &Service{
		novelRepo:    novelRepo,
		progressRepo: progressRepo,
		chatModel:    chatModel,
		searchSvc:    searchSvc,
		graphReader:  graphReader,
		entitySvc:    entitySvc,
	}
}

// AskQuestion answers a user question using the Eino ADK Reading Memory Agent.
func (s *Service) AskQuestion(ctx context.Context, novelID int64, question string) (string, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return "", fmt.Errorf("get novel: %w", err)
	}

	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return "", fmt.Errorf("get progress: %w", err)
	}

	maxChapter := progress.CurrentChapter
	novelTitle := novel.Title

	readingAgent, err := newReadingAgent(ctx, readingAgentConfig{
		ChatModel: s.chatModel,
		ToolDeps: tools.Deps{
			NovelID:       novelID,
			MaxChapter:    maxChapter,
			SearchFunc:    s.searchSvc.SearchTool(),
			TimelineFunc:  s.graphReader.TimelineTool(),
			RelationsFunc: s.graphReader.RelationsTool(),
			EntityFunc:    s.entitySvc.EntityTool(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	return runReadingAgent(ctx, readingAgent, novelTitle, maxChapter, question)
}

// SearchChapters performs semantic search and returns formatted results.
func (s *Service) SearchChapters(ctx context.Context, novelID int64, query string) ([]model.Chapter, []float64, error) {
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
