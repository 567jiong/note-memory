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
	ragSvc      *RAGService
	searchSvc   *SearchService
	concurrency int
}

func NewChapterService(chapterRepo *repository.ChapterRepo, aiClient *ai.Client, ragSvc *RAGService, searchSvc *SearchService) *ChapterService {
	return &ChapterService{
		chapterRepo: chapterRepo,
		aiClient:    aiClient,
		ragSvc:      ragSvc,
		searchSvc:   searchSvc,
		concurrency: 3,
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
			log.Printf("[chapter] novel %d: summaries done, filling embeddings from content...", novelID)
			s.FillEmbeddings(ctx, novelID)
			// Aliases already written incrementally per-chapter; just refresh dict
			s.searchSvc.RefreshDictForNovel(novelID)
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
2. 提取本章出现的主要人物。只提取有明确姓名或固定称呼的角色，不要提取"黄脸修士""中年儒生""师兄"之类的外貌描述或泛称角色。以 JSON 数组格式返回：
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

	// Generate embedding for the summary
	if summary != "" {
		vec, err := s.aiClient.Embedding(ctx, summary)
		if err != nil {
			log.Printf("[chapter] embedding error for chapter %d: %v", ch.ChapterNumber, err)
		} else {
			if err := s.chapterRepo.UpdateEmbedding(ch.ID, vec); err != nil {
				log.Printf("[chapter] save embedding error: %v", err)
			}
		}
	}

	// Update full-text search index
	if err := s.searchSvc.UpdateSearchIndex(ch.ID, ch.NovelID, ch.Title, summary, charsJSON, eventsJSON); err != nil {
		log.Printf("[chapter] search index error for chapter %d: %v", ch.ChapterNumber, err)
	}

	// Incrementally write aliases (per-chapter, not batch-at-end)
	if err := s.searchSvc.UpsertChapterAliases(ch.NovelID, charsJSON); err != nil {
		log.Printf("[chapter] alias upsert error for chapter %d: %v", ch.ChapterNumber, err)
	}

	log.Printf("[chapter] novel %d chapter %d: summary + embedding + search index + alias done", ch.NovelID, ch.ChapterNumber)
}

// FillEmbeddings generates embeddings from chapter content for chapters that have
// content but no embedding vector yet. Unlike summarizeChapter, this does NOT call
// the LLM — it only calls the embedding API. Content is truncated to fit the model's
// token limit before sending.
func (s *ChapterService) FillEmbeddings(ctx context.Context, novelID int64) {
	for {
		chapters, err := s.chapterRepo.ListWithoutEmbedding(novelID, s.concurrency)
		if err != nil {
			log.Printf("[embedding] error listing chapters: %v", err)
			return
		}
		if len(chapters) == 0 {
			log.Printf("[embedding] novel %d: all embeddings filled", novelID)
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
				s.embedChapterContent(ctx, &c)
			}(ch)
		}
		wg.Wait()
	}
}

// embedChapterContent generates an embedding from the chapter's content text.
// The content is truncated to fit within the embedding model's token window.
// BAAI/bge-large-zh-v1.5 has a 512-token limit; we conservatively truncate to
// 400 runes (~200-300 tokens for Chinese) to stay safely under the cap.
func (s *ChapterService) embedChapterContent(ctx context.Context, ch *model.Chapter) {
	text := strings.TrimSpace(ch.Content)
	if text == "" {
		// Fall back to summary if content is empty
		text = strings.TrimSpace(ch.Summary)
		if text == "" {
			log.Printf("[embedding] chapter %d: no content or summary, skipping", ch.ChapterNumber)
			return
		}
	}

	text = truncateForEmbedding(text, 400)

	vec, err := s.aiClient.Embedding(ctx, text)
	if err != nil {
		log.Printf("[embedding] chapter %d embedding error: %v", ch.ChapterNumber, err)
		return
	}

	if err := s.chapterRepo.UpdateEmbedding(ch.ID, vec); err != nil {
		log.Printf("[embedding] chapter %d save error: %v", ch.ChapterNumber, err)
		return
	}

	log.Printf("[embedding] novel %d chapter %d: content embedding done (%d runes)",
		ch.NovelID, ch.ChapterNumber, len([]rune(text)))
}

// truncateForEmbedding truncates text to maxRunes, trying to break at a natural
// sentence boundary (。！？…\n) so the truncated text remains semantically coherent.
func truncateForEmbedding(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	// Search backwards from maxRunes for a sentence boundary
	searchStart := maxRunes - 1
	searchEnd := maxRunes - 80
	if searchEnd < 0 {
		searchEnd = 0
	}
	for i := searchStart; i >= searchEnd; i-- {
		switch runes[i] {
		case '。', '！', '？', '…', '\n':
			return string(runes[:i+1])
		}
	}
	return string(runes[:maxRunes])
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
