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

	"github.com/pgvector/pgvector-go"
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
// Semantic search uses chunk-level embeddings (chapter_chunks), so chapter-level
// embedding is no longer generated.
func (s *ChapterService) ParseAllChapters(ctx context.Context, novelID int64) {
	for {
		chapters, err := s.chapterRepo.ListUnprocessed(novelID, s.concurrency)
		if err != nil {
			log.Printf("[chapter] error listing unprocessed: %v", err)
			return
		}
		if len(chapters) == 0 {
			log.Printf("[chapter] novel %d: all summaries done, backfilling chunk embeddings...", novelID)
			s.FillChunkEmbeddings(ctx, novelID)
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

// summarizeChapter sends a chapter to AI for summarization, then chunks the content
// and generates chunk-level embeddings for semantic search.
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

	// Update full-text search index
	if err := s.searchSvc.UpdateSearchIndex(ch.ID, ch.NovelID, ch.Title, summary, charsJSON, eventsJSON); err != nil {
		log.Printf("[chapter] search index error for chapter %d: %v", ch.ChapterNumber, err)
	}

	// Incrementally write aliases (per-chapter, not batch-at-end)
	if err := s.searchSvc.UpsertChapterAliases(ch.NovelID, charsJSON); err != nil {
		log.Printf("[chapter] alias upsert error for chapter %d: %v", ch.ChapterNumber, err)
	}

	// Chunk content into overlapping segments and generate chunk embeddings
	s.chunkAndEmbedChapter(ctx, ch)

	log.Printf("[chapter] novel %d chapter %d: summary + search index + alias + %d chunks done",
		ch.NovelID, ch.ChapterNumber, countChunks(ch.Content))
}

// chunkAndEmbedChapter splits chapter content into overlapping chunks and generates embeddings.
func (s *ChapterService) chunkAndEmbedChapter(ctx context.Context, ch *model.Chapter) {
	content := strings.TrimSpace(ch.Content)
	if content == "" {
		return
	}

	chunks := ChunkContent(content)
	if len(chunks) == 0 {
		return
	}

	records := make([]model.ChapterChunk, 0, len(chunks))
	for i, ck := range chunks {
		records = append(records, model.ChapterChunk{
			NovelID:    ch.NovelID,
			ChapterID:  ch.ID,
			ChunkIndex: i + 1,
			Content:    ck.Content,
			CharStart:  ck.CharStart,
			CharEnd:    ck.CharEnd,
		})
	}

	if err := s.chapterRepo.BatchCreateChunks(records); err != nil {
		log.Printf("[chunk] batch create error for chapter %d: %v", ch.ChapterNumber, err)
		return
	}

	for i := range records {
		vec, err := s.aiClient.Embedding(ctx, records[i].Content)
		if err != nil {
			log.Printf("[chunk] embedding error for chapter %d chunk %d: %v", ch.ChapterNumber, i+1, err)
			continue
		}
		if err := s.chapterRepo.BatchUpdateChunkEmbedding(
			[]int64{records[i].ID}, []pgvector.Vector{pgvector.NewVector(vec)},
		); err != nil {
			log.Printf("[chunk] save embedding error for chapter %d chunk %d: %v", ch.ChapterNumber, i+1, err)
		}
	}
}

// FillChunkEmbeddings backfills missing chunk-level embeddings.
func (s *ChapterService) FillChunkEmbeddings(ctx context.Context, novelID int64) {
	for {
		chunks, err := s.chapterRepo.ListChunksWithoutEmbedding(novelID, s.concurrency*3)
		if err != nil {
			log.Printf("[chunk] error listing chunks: %v", err)
			return
		}
		if len(chunks) == 0 {
			log.Printf("[chunk] novel %d: all chunk embeddings filled", novelID)
			return
		}

		var wg sync.WaitGroup
		sem := make(chan struct{}, s.concurrency)

		for i := range chunks {
			ck := chunks[i]
			wg.Add(1)
			sem <- struct{}{}
			go func(c model.ChapterChunk) {
				defer wg.Done()
				defer func() { <-sem }()
				vec, err := s.aiClient.Embedding(ctx, c.Content)
				if err != nil {
					log.Printf("[chunk] embedding error for chunk %d: %v", c.ID, err)
					return
				}
				if err := s.chapterRepo.BatchUpdateChunkEmbedding(
					[]int64{c.ID}, []pgvector.Vector{pgvector.NewVector(vec)},
				); err != nil {
					log.Printf("[chunk] save embedding error for chunk %d: %v", c.ID, err)
				}
			}(ck)
		}
		wg.Wait()
	}
}

func countChunks(content string) int {
	if content == "" {
		return 0
	}
	return (len([]rune(content)) / 300) + 1
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
