package recap

import (
	"context"
	"fmt"
	"note-memory/internal/repository"
	"note-memory/internal/service/search"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Service generates reading recovery recaps.
type Service struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	recapRepo    *repository.RecapRepo
	chatModel    model.ToolCallingChatModel
	ragSvc       *search.RAGService
}

func NewService(
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	recapRepo *repository.RecapRepo,
	chatModel model.ToolCallingChatModel,
	ragSvc *search.RAGService,
) *Service {
	return &Service{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		recapRepo:    recapRepo,
		chatModel:    chatModel,
		ragSvc:       ragSvc,
	}
}

// GenerateRecap generates or retrieves a cached reading recovery recap.
// Uses Agentic RAG: multi-step semantic search with LLM verification.
func (s *Service) GenerateRecap(ctx context.Context, novelID int64) (string, error) {
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return "", fmt.Errorf("get novel: %w", err)
	}

	progress, err := s.progressRepo.GetByNovel(novelID)
	if err != nil {
		return "", fmt.Errorf("get progress: %w", err)
	}

	currentChapter := progress.CurrentChapter

	// Check cache first
	cached, err := s.recapRepo.GetByNovelAndChapter(novelID, currentChapter)
	if err == nil && cached != nil {
		return cached.RecapContent, nil
	}

	// Agentic RAG: multi-step retrieval for relevant context
	recapQuery := "主角当前状态 主线任务 最近关键事件 人物关系 伏笔 重要转折"
	result, err := s.ragSvc.AgenticRetrieve(ctx, recapQuery, novelID, currentChapter, novel.Title)

	var retrievedCtx string
	if err != nil {
		fmt.Printf("[recap] Agentic RAG failed: %v\n", err)
		retrievedCtx = fmt.Sprintf("小说《%s》\n用户阅读进度：第 %d 章 / 共 %d 章\n\n（检索失败: %v）",
			novel.Title, currentChapter, novel.TotalChapters, err)
	} else {
		fmt.Printf("[recap] Agentic RAG completed, verified=%v\n", result.Verified)
		retrievedCtx = fmt.Sprintf("小说《%s》\n用户阅读进度：第 %d 章 / 共 %d 章\n\n%s",
			novel.Title, currentChapter, novel.TotalChapters, result.Context)
	}

	sysPrompt := fmt.Sprintf(`你是一个阅读恢复助手。用户正在阅读小说《%s》，当前读到第 %d 章。

你的任务是根据用户当前的阅读进度，生成一份"阅读恢复回顾"，帮助用户在长时间中断后快速恢复阅读状态。

## 严格规则（极其重要）
你只能使用下面提供的上下文信息（均来自第 1~%d 章）。绝对禁止引用第 %d 章及以后的内容。
如果某个信息在第 %d 章时尚未揭晓，你必须基于第 %d 章时的状态来描述，不要透露后续发展。

## 输出格式
请生成以下两部分：

### 📖 30 秒速览版（100 字以内）
主角当前是谁、在做什么、目标是什么。要简洁。

### 📚 3 分钟详细版（500 字以内）
1. 主角当前身份/状态
2. 当前主线目标
3. 最近关键事件
4. 重要人物及其当前状态
5. 仍在进行中的伏笔（只列在第 1~%d 章已埋下、尚未揭晓的）

请严格按照以上格式输出。`, novel.Title, currentChapter, currentChapter, currentChapter+1, currentChapter, currentChapter, currentChapter)

	msg, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(sysPrompt),
		schema.UserMessage(retrievedCtx),
	}, model.WithTemperature(0.7), model.WithMaxTokens(2000))
	if err != nil {
		return "", fmt.Errorf("generate recap: %w", err)
	}

	resp := msg.Content

	// Cache the result
	if err := s.recapRepo.Upsert(novelID, currentChapter, resp); err != nil {
		fmt.Printf("[recap] cache error: %v\n", err)
	}

	return resp, nil
}

// GetCachedRecap returns a previously generated recap.
func (s *Service) GetCachedRecap(novelID int64, chapter int) (string, error) {
	recap, err := s.recapRepo.GetByNovelAndChapter(novelID, chapter)
	if err != nil {
		return "", err
	}
	return recap.RecapContent, nil
}
