package qa

import (
	"context"
	"fmt"
	"note-memory/internal/graph"
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

// buildAgentConfig creates the agent configuration for a given novel/progress context.
func (s *Service) buildAgentConfig(novelID int64) (readingAgentConfig, string, int, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return readingAgentConfig{}, "", 0, fmt.Errorf("get novel: %w", err)
	}

	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return readingAgentConfig{}, "", 0, fmt.Errorf("get progress: %w", err)
	}

	maxChapter := progress.CurrentChapter
	novelTitle := novel.Title

	cfg := readingAgentConfig{
		ChatModel: s.chatModel,
		ToolDeps: tools.Deps{
			NovelID:           novelID,
			MaxChapter:        maxChapter,
			SearchFunc:        s.searchSvc.SearchTool(),
			TimelineFunc:      s.graphReader.TimelineTool(),
			RelationsFunc:     s.graphReader.RelationsTool(),
			EntityFunc:        s.entitySvc.EntityTool(),
			ChaptersFunc:      s.searchSvc.ChaptersTool(),
			TechniqueFunc:     s.graphReader.TechniqueTool(),
			AllTechniquesFunc: s.graphReader.AllTechniquesTool(),
		},
	}
	return cfg, novelTitle, maxChapter, nil
}

// AskQuestion answers a user question via the Eino ADK Reading Memory Agent with streaming.
// Each StreamEvent is pushed through onEvent as the agent runs, enabling SSE delivery.
// Returns the final assembled answer for caching/logging purposes.
func (s *Service) AskQuestion(ctx context.Context, novelID int64, question string, onEvent func(StreamEvent)) (string, error) {
	cfg, novelTitle, maxChapter, err := s.buildAgentConfig(novelID)
	if err != nil {
		return "", err
	}

	readingAgent, err := newReadingAgent(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	return runReadingAgentStream(ctx, readingAgent, novelTitle, maxChapter, question, onEvent)
}
