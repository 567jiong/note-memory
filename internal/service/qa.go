package service

import (
	"context"
	"fmt"
	"note-memory/internal/ai"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/repository"
)

// QAService handles spoiler-free question answering.
type QAService struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	ragSvc       *RAGService
	aiClient     *ai.Client
	searchSvc    *SearchService
	graphReader  *graph.GraphReader
}

func NewQAService(
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	ragSvc *RAGService,
	aiClient *ai.Client,
	searchSvc *SearchService,
	graphReader *graph.GraphReader,
) *QAService {
	return &QAService{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		ragSvc:       ragSvc,
		aiClient:     aiClient,
		searchSvc:    searchSvc,
		graphReader:  graphReader,
	}
}

// AskQuestion answers a user question about the novel, strictly within the spoiler-free boundary.
func (s *QAService) AskQuestion(ctx context.Context, novelID int64, question string) (string, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return "", fmt.Errorf("get novel: %w", err)
	}

	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return "", fmt.Errorf("get progress: %w", err)
	}

	currentChapter := progress.CurrentChapter

	// Agentic RAG: multi-step retrieval → verify → rewrite → re-retrieve
	result, err := s.ragSvc.AgenticRetrieve(ctx, question, novelID, currentChapter, novel.Title)
	if err != nil {
		return "", fmt.Errorf("agentic retrieve: %w", err)
	}

	fmt.Printf("[qa] Agentic RAG: %d iterations, verified=%v\n", result.Iterations, result.Verified)

	// Enrich context with Neo4j knowledge graph data
	graphCtx, qClass := graph.RouteQuery(ctx, s.graphReader, question, novelID, "主角", currentChapter)
	fmt.Printf("[qa] query class: %v, graph enriched: timeline=%d, relations=%d\n",
		qClass, len(graphCtx.RealmTimeline), len(graphCtx.Relations))

	// Assemble final context
	retrievedCtx := result.Context
	if graphCtx.RealmTimeline != "" {
		retrievedCtx += "\n" + graphCtx.RealmTimeline
	}
	if graphCtx.Relations != "" {
		retrievedCtx += "\n" + graphCtx.Relations
	}
	if graphCtx.StatusChanges != "" {
		retrievedCtx += "\n" + graphCtx.StatusChanges
	}

	sysPrompt := fmt.Sprintf(`你是一个阅读助手，帮助用户回忆小说《%s》的剧情。

## 严格规则（极其重要）
- 你只能使用下面提供的上下文信息来回答问题
- 所有上下文信息都来自第 1~%d 章，绝不能引用第 %d 章及以后的内容
- 如果上下文中没有足够信息回答问题，请如实告知"根据当前的阅读进度，这个信息尚未揭示"，不要编造
- 回答要简洁、准确

## 上下文信息
%s`, novel.Title, currentChapter, currentChapter+1, retrievedCtx)

	userPrompt := fmt.Sprintf("用户提问：%s\n\n请根据上下文回答。如果信息不足，请明确说明。", question)

	answer, err := s.aiClient.Chat(ctx, []ai.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userPrompt},
	}, 0.5, 1000)
	if err != nil {
		return "", fmt.Errorf("generate answer: %w", err)
	}

	return answer, nil
}

// SearchChapters performs semantic search and returns formatted results.
func (s *QAService) SearchChapters(ctx context.Context, novelID int64, query string) ([]model.Chapter, []float64, error) {
	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return nil, nil, fmt.Errorf("get progress: %w", err)
	}

	results, err := s.ragSvc.Search(ctx, query, novelID, progress.CurrentChapter, 10)
	if err != nil {
		return nil, nil, err
	}

	chapters := make([]model.Chapter, 0, len(results))
	scores := make([]float64, 0, len(results))
	for _, r := range results {
		chapters = append(chapters, r.Chapter)
		scores = append(scores, r.Score)
	}
	return chapters, scores, nil
}
