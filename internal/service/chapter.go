package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"strings"
	"sync"
)

// ChapterService handles AI-powered chapter analysis.
type ChapterService struct {
	chapterRepo *repository.ChapterRepo
	aiClient    *ai.Client
	concurrency int
}

func NewChapterService(chapterRepo *repository.ChapterRepo, aiClient *ai.Client) *ChapterService {
	return &ChapterService{
		chapterRepo: chapterRepo,
		aiClient:    aiClient,
		concurrency: 3, // Max concurrent AI requests
	}
}

// ParseAllChapters summarizes all unprocessed chapters for a novel.
func (s *ChapterService) ParseAllChapters(ctx context.Context, novelID int64) {
	for {
		chapters, err := s.chapterRepo.ListUnprocessed(novelID, s.concurrency)
		if err != nil {
			log.Printf("[chapter] error listing unprocessed: %v", err)
			return
		}
		if len(chapters) == 0 {
			log.Printf("[chapter] novel %d: all chapters processed", novelID)
			return
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, s.concurrency)

		for i := range chapters {
			ch := chapters[i]
			wg.Add(1)
			sem <- struct{}{}
			go func(c model.Chapter) {
				defer wg.Done()
				defer func() { <-sem }()
				s.summarizeChapter(ctx, &c)
			}(ch)
		}
		wg.Wait()
	}
}

// summarizeChapter sends a chapter to AI for summarization and extraction.
func (s *ChapterService) summarizeChapter(ctx context.Context, ch *model.Chapter) {
	sysPrompt := `你是一个小说分析助手。请根据提供的章节内容完成以下任务：

1. 用 2-3 句话总结本章主要情节。
2. 提取本章出现的主要人物（只提取在本章中有实质性出场的人物），以 JSON 数组格式返回：
   [{"name":"人物名","aliases":["别名"],"status":"本章中的状态或变化","first_appearance":章节号}]
3. 提取本章的关键事件，以 JSON 数组格式返回：
   [{"title":"事件名","participants":["人物名"],"summary":"事件简述","impact":"影响","chapter_num":章节号}]

请严格按照以下 XML 格式输出：
<summary>总结内容</summary>
<characters>人物JSON数组</characters>
<events>事件JSON数组</events>`

	userPrompt := fmt.Sprintf("章节标题：%s\n\n章节内容：\n%s", ch.Title, ch.Content)

	resp, err := s.aiClient.ChatSimple(ctx, sysPrompt, userPrompt)
	if err != nil {
		log.Printf("[chapter] AI summarize error for novel %d chapter %d: %v", ch.NovelID, ch.ChapterNumber, err)
		return
	}

	summary, charsJSON, eventsJSON := parseAIResponse(resp)

	chars, _ := model.MarshalCharacters(charsJSON)
	events, _ := model.MarshalEvents(eventsJSON)

	if err := s.chapterRepo.UpdateSummary(ch.ID, summary, chars, events); err != nil {
		log.Printf("[chapter] update summary error: %v", err)
		return
	}

	log.Printf("[chapter] novel %d chapter %d summarized", ch.NovelID, ch.ChapterNumber)
}

// parseAIResponse extracts XML sections from the AI response.
func parseAIResponse(resp string) (summary string, chars []model.CharacterInfo, events []model.EventInfo) {
	summary = extractXML(resp, "summary")
	charsJSON := extractXML(resp, "characters")
	eventsJSON := extractXML(resp, "events")

	if charsJSON != "" {
		json.Unmarshal([]byte(charsJSON), &chars)
	}
	if eventsJSON != "" {
		json.Unmarshal([]byte(eventsJSON), &events)
	}

	if summary == "" {
		// Fallback: use the whole response as summary
		summary = resp
	}
	return
}

// extractXML extracts content between XML tags.
func extractXML(s, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(s, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(s[start:], close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(s[start : start+end])
}
