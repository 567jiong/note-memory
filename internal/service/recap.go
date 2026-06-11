package service

import (
	"context"
	"fmt"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"strings"
)

// RecapService generates reading recovery recaps.
type RecapService struct {
	novelRepo    *repository.NovelRepo
	chapterRepo  *repository.ChapterRepo
	progressRepo *repository.ProgressRepo
	recapRepo    *repository.RecapRepo
	aiClient     *ai.Client
}

func NewRecapService(
	novelRepo *repository.NovelRepo,
	chapterRepo *repository.ChapterRepo,
	progressRepo *repository.ProgressRepo,
	recapRepo *repository.RecapRepo,
	aiClient *ai.Client,
) *RecapService {
	return &RecapService{
		novelRepo:    novelRepo,
		chapterRepo:  chapterRepo,
		progressRepo: progressRepo,
		recapRepo:    recapRepo,
		aiClient:     aiClient,
	}
}

// GenerateRecap generates or retrieves a cached reading recovery recap.
func (s *RecapService) GenerateRecap(ctx context.Context, novelID int64) (string, error) {
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

	// Build context: recent chapters (last 20) + all character/event summaries
	recentChapters, err := s.chapterRepo.ListRecentChapters(novelID, currentChapter, 20)
	if err != nil {
		return "", fmt.Errorf("get recent chapters: %w", err)
	}

	// Get characters and events from broader range (last 50 chapters)
	allUpToProgress, err := s.chapterRepo.ListRecentChapters(novelID, currentChapter, 50)
	if err != nil {
		return "", fmt.Errorf("get all chapters up to progress: %w", err)
	}

	// Build the prompt context
	var contextBuilder strings.Builder
	contextBuilder.WriteString(fmt.Sprintf("小说《%s》\n", novel.Title))
	contextBuilder.WriteString(fmt.Sprintf("总章节数：%d，用户当前读到第 %d 章\n\n", novel.TotalChapters, currentChapter))
	contextBuilder.WriteString("=== 最近章节摘要 ===\n")

	// Collect all characters and events
	allCharacters := make(map[string]model.CharacterInfo)
	allEvents := make([]model.EventInfo, 0)

	for _, ch := range allUpToProgress {
		chars, _ := model.UnmarshalCharacters(ch.Characters)
		for _, c := range chars {
			// Keep the latest status for each character
			if existing, ok := allCharacters[c.Name]; ok {
				if c.Status != "" {
					existing.Status = c.Status
				}
				allCharacters[c.Name] = existing
			} else {
				allCharacters[c.Name] = c
			}
		}

		events, _ := model.UnmarshalEvents(ch.Events)
		allEvents = append(allEvents, events...)
	}

	// Add recent chapter summaries
	for _, ch := range recentChapters {
		if ch.Summary != "" {
			contextBuilder.WriteString(fmt.Sprintf("第%d章 %s: %s\n", ch.ChapterNumber, ch.Title, ch.Summary))
		}
	}

	// Add character list
	contextBuilder.WriteString("\n=== 已知人物（截至第 ")
	contextBuilder.WriteString(fmt.Sprintf("%d", currentChapter))
	contextBuilder.WriteString(" 章） ===\n")
	for name, char := range allCharacters {
		contextBuilder.WriteString(fmt.Sprintf("- %s", name))
		if char.Status != "" {
			contextBuilder.WriteString(fmt.Sprintf("（%s）", char.Status))
		}
		contextBuilder.WriteString("\n")
	}

	// Add event list
	contextBuilder.WriteString("\n=== 已知事件 ===\n")
	for _, evt := range allEvents {
		contextBuilder.WriteString(fmt.Sprintf("- [第%d章] %s: %s\n", evt.ChapterNum, evt.Title, evt.Summary))
	}

	sysPrompt := fmt.Sprintf(`你是一个阅读恢复助手。用户正在阅读小说《%s》，当前读到第 %d 章。

你的任务是根据用户当前的阅读进度，生成一份"阅读恢复回顾"，帮助用户在长时间中断后快速恢复阅读状态。

## 严格规则（极其重要）
你只能使用第 1~%d 章的信息。绝对禁止引用第 %d 章及以后的内容。
如果某个信息在第 %d 章时尚未揭晓，你必须基于第 %d 章时的状态来描述，不要透露后续发展。

## 输出格式
请生成以下两部分：

### 📖 30 秒速览版（100 字以内）
主角当前是谁、在做什么、目标是什么。要简洁。

### 📚 3 分钟详细版（500 字以内）
1. 主角当前身份/状态
2. 当前主线目标
3. 最近关键事件（最近10-20章）
4. 重要人物及其当前状态
5. 仍在进行中的伏笔（只列在第 1~%d 章已埋下、尚未揭晓的）

请严格按照以上格式输出。`, novel.Title, currentChapter, currentChapter, currentChapter+1, currentChapter, currentChapter, currentChapter)

	userPrompt := contextBuilder.String()

	// Limit context to avoid token overflow
	if len(userPrompt) > 12000 {
		runes := []rune(userPrompt)
		userPrompt = string(runes[:12000]) + "\n\n... [内容已截断以适配上下文长度]"
	}

	resp, err := s.aiClient.ChatSimple(ctx, sysPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("generate recap: %w", err)
	}

	// Cache the result
	if err := s.recapRepo.Upsert(novelID, currentChapter, resp); err != nil {
		// Non-fatal: cache failure shouldn't block the user
		fmt.Printf("[recap] cache error: %v\n", err)
	}

	return resp, nil
}

// GetCachedRecap returns a previously generated recap.
func (s *RecapService) GetCachedRecap(novelID int64, chapter int) (string, error) {
	recap, err := s.recapRepo.GetByNovelAndChapter(novelID, chapter)
	if err != nil {
		return "", err
	}
	return recap.RecapContent, nil
}
