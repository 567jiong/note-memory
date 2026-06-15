package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"note-memory/internal/agent/chat"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/search"

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

	readingAgent, err := chat.New(ctx, chat.Config{
		ChatModel: s.chatModel,
		Tools: chat.ToolDeps{
			NovelID:    novelID,
			MaxChapter: maxChapter,

			SearchFunc: func(ctx context.Context, query string, nid int64, maxCh, topK int) (string, error) {
				results, err := s.searchSvc.HybridSearch(ctx, query, nid, maxCh, topK)
				if err != nil {
					return "", err
				}
				type r struct {
					ChapterNum int     `json:"chapter_num"`
					Score      float64 `json:"score"`
					Summary    string  `json:"summary"`
				}
				var out []r
				for _, item := range results {
					if len(out) >= topK {
						break
					}
					out = append(out, r{ChapterNum: item.Chapter.ChapterNumber, Score: item.FinalScore, Summary: item.Chapter.Summary})
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			},

			TimelineFunc: func(ctx context.Context, nid int64, name string, maxCh int) (string, error) {
				if s.graphReader == nil || !s.graphReader.IsEnabled() {
					return `[]`, nil
				}
				entries, err := s.graphReader.RealmTimeline(ctx, nid, name, maxCh)
				if err != nil {
					return "", err
				}
				var out []chat.TimelineEntry
				for _, e := range entries {
					out = append(out, chat.TimelineEntry{Realm: e.Realm, Chapter: e.Chapter, Age: e.Age})
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			},

			RelationsFunc: func(ctx context.Context, nid int64, name string, maxCh int) (string, error) {
				if s.graphReader == nil || !s.graphReader.IsEnabled() {
					return `[]`, nil
				}
				entries, err := s.graphReader.CharacterRelations(ctx, nid, name, maxCh)
				if err != nil {
					return "", err
				}
				var out []chat.RelationEntry
				for _, e := range entries {
					out = append(out, chat.RelationEntry{From: e.FromName, To: e.ToName, RelType: e.RelationType, Since: e.SinceChapter, Ended: e.EndedChapter})
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			},

			EntityFunc: func(ctx context.Context, query string, nid int64, topK int) (string, error) {
				if s.entitySvc == nil {
					return `{"matched_names":[]}`, nil
				}
				names, err := s.entitySvc.SearchEntities(ctx, query, nid, topK)
				if err != nil {
					return "", err
				}
				type out struct {
					Names []string `json:"matched_names"`
				}
				b, _ := json.Marshal(out{Names: names})
				return string(b), nil
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create agent: %w", err)
	}
	return chat.Run(ctx, readingAgent, novelTitle, maxChapter, question)
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
