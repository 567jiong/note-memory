package chapter

import (
	"context"
	"encoding/json"
	"log"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/search"
	"strings"
	"sync"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"
)

// Service handles AI-powered chapter analysis.
type Service struct {
	chapterRepo *repository.ChapterRepo
	chatModel   einomodel.ToolCallingChatModel
	embedder    embedding.Embedder
	ragSvc      *search.RAGService
	searchSvc   *search.Service
	graphWriter *graph.GraphWriter
	entitySvc   *entity.Service
	concurrency int
}

func NewService(chapterRepo *repository.ChapterRepo, chatModel einomodel.ToolCallingChatModel, embedder embedding.Embedder, ragSvc *search.RAGService, searchSvc *search.Service, graphWriter *graph.GraphWriter, entitySvc *entity.Service) *Service {
	return &Service{
		chapterRepo: chapterRepo,
		chatModel:   chatModel,
		embedder:    embedder,
		ragSvc:      ragSvc,
		searchSvc:   searchSvc,
		graphWriter: graphWriter,
		entitySvc:   entitySvc,
		concurrency: 3,
	}
}

// ParseAllChapters summarizes all unprocessed chapters for a novel.
func (s *Service) ParseAllChapters(ctx context.Context, novelID int64) {
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
func (s *Service) summarizeChapter(ctx context.Context, ch *model.Chapter) {
	sr, err := newSummarizerAgent(ctx, s.chatModel)
	if err != nil {
		log.Printf("[chapter] create summarizer agent error for novel %d chapter %d: %v", ch.NovelID, ch.ChapterNumber, err)
		return
	}

	resp, err := runSummarizer(ctx, sr, ch.Title, ch.Content)
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

	// Sync to Neo4j knowledge graph (novel title/author set by first sync)
	if s.graphWriter != nil && s.graphWriter.IsEnabled() {
		_ = s.graphWriter.SyncChapter(ctx, &model.Novel{ID: ch.NovelID}, ch, charsJSON, eventsJSON)
	}

	// Generate/update entity embeddings for semantic alias matching
	if s.entitySvc != nil {
		for _, c := range charsJSON {
			if err := s.entitySvc.UpsertEntityFromChapter(ctx, ch.NovelID, c); err != nil {
				log.Printf("[chapter] entity embedding error for %s: %v", c.Name, err)
			}
		}
	}

	log.Printf("[chapter] novel %d chapter %d: summary + search index + alias + %d chunks done",
		ch.NovelID, ch.ChapterNumber, countChunks(ch.Content))
}

// chunkAndEmbedChapter splits chapter content into overlapping chunks and generates embeddings.
func (s *Service) chunkAndEmbedChapter(ctx context.Context, ch *model.Chapter) {
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
		vecs, err := s.embedder.EmbedStrings(ctx, []string{records[i].Content})
		if err != nil || len(vecs) == 0 {
			log.Printf("[chunk] embedding error for chapter %d chunk %d: %v", ch.ChapterNumber, i+1, err)
			continue
		}
		vec := make([]float32, len(vecs[0]))
		for j, v := range vecs[0] {
			vec[j] = float32(v)
		}
		if err := s.chapterRepo.BatchUpdateChunkEmbedding(
			[]int64{records[i].ID}, []pgvector.Vector{pgvector.NewVector(vec)},
		); err != nil {
			log.Printf("[chunk] save embedding error for chapter %d chunk %d: %v", ch.ChapterNumber, i+1, err)
		}
	}
}

// FillChunkEmbeddings backfills missing chunk-level embeddings.
func (s *Service) FillChunkEmbeddings(ctx context.Context, novelID int64) {
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
				vecs, err := s.embedder.EmbedStrings(ctx, []string{c.Content})
				if err != nil || len(vecs) == 0 {
					log.Printf("[chunk] embedding error for chunk %d: %v", c.ID, err)
					return
				}
				vec := make([]float32, len(vecs[0]))
				for j, v := range vecs[0] {
					vec[j] = float32(v)
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
