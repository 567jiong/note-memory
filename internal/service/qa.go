package service

import (
	"context"
	"fmt"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"strings"
)

// QAService handles spoiler-free question answering.
type QAService struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	ragSvc       *RAGService
	aiClient     *ai.Client
	searchSvc    *SearchService
}

func NewQAService(
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	ragSvc *RAGService,
	aiClient *ai.Client,
	searchSvc *SearchService,
) *QAService {
	return &QAService{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		ragSvc:       ragSvc,
		aiClient:     aiClient,
		searchSvc:    searchSvc,
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

	// Hybrid RAG retrieval: semantic + full-text with alias expansion
	retrievedCtx := s.buildQARetrievalContext(ctx, novel, question, currentChapter)

	// Override the context's title with the actual novel title
	retrievedCtx = strings.Replace(retrievedCtx, "小说《》", fmt.Sprintf("小说《%s》", novel.Title), 1)
	retrievedCtx = fmt.Sprintf("小说《%s》\n用户阅读进度：第 %d 章\n%s", novel.Title, currentChapter, retrievedCtx)

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

// buildQARetrievalContext builds retrieval context using hybrid search for Q&A.
func (s *QAService) buildQARetrievalContext(ctx context.Context, novel *model.Novel, question string, maxChapter int) string {
	// Try hybrid search first (semantic + full-text + alias)
	results, err := s.searchSvc.HybridSearch(ctx, question, novel.ID, maxChapter, 10)
	if err != nil {
		// Fallback to semantic-only
		ragResults, err2 := s.ragSvc.Search(ctx, question, novel.ID, maxChapter, 10)
		if err2 != nil {
			return fmt.Sprintf("（检索失败: %v）", err)
		}
		return s.ragSvc.BuildContext(novel.Title, maxChapter, ragResults)
	}

	// Convert hybrid results to RAG context format
	var ragResults []SearchResult
	for _, r := range results {
		ragResults = append(ragResults, SearchResult{
			Chapter: r.Chapter,
			Score:   r.FinalScore,
		})
	}
	return s.ragSvc.BuildContext(novel.Title, maxChapter, ragResults)
}
