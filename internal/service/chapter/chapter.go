package chapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/search"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/embedding"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/pgvector/pgvector-go"
)

// Service handles AI-powered chapter analysis.
type Service struct {
	chapterRepo *repository.ChapterRepo
	novelRepo   *repository.NovelRepo
	chatModel   einomodel.ToolCallingChatModel
	embedder    embedding.Embedder
	ragSvc      *search.RAGService
	searchSvc   *search.Service
	graphWriter *graph.GraphWriter
	entitySvc   *entity.Service
	concurrency int
}

func NewService(chapterRepo *repository.ChapterRepo, novelRepo *repository.NovelRepo, chatModel einomodel.ToolCallingChatModel, embedder embedding.Embedder, ragSvc *search.RAGService, searchSvc *search.Service, graphWriter *graph.GraphWriter, entitySvc *entity.Service) *Service {
	return &Service{
		chapterRepo: chapterRepo,
		novelRepo:   novelRepo,
		chatModel:   chatModel,
		embedder:    embedder,
		ragSvc:      ragSvc,
		searchSvc:   searchSvc,
		graphWriter: graphWriter,
		entitySvc:   entitySvc,
		concurrency: 8,
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
			// log.Printf("[chapter] novel %d: all summaries done, backfilling chunk embeddings...", novelID)
			// s.FillChunkEmbeddings(ctx, novelID)
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

	summary, charsParsed, eventsParsed, relationsParsed, techniquesParsed := parseAIResponse(resp)

	chars, _ := model.MarshalCharacters(charsParsed)
	events, _ := model.MarshalEvents(eventsParsed)
	relationsJSON, _ := model.MarshalRelations(relationsParsed)
	techniquesJSON, _ := model.MarshalTechniques(techniquesParsed)

	if err := s.chapterRepo.UpdateSummary(ch.ID, summary, chars, events, relationsJSON, techniquesJSON); err != nil {
		log.Printf("[chapter] update summary error: %v", err)
		return
	}

	// Update full-text search index
	if err := s.searchSvc.UpdateSearchIndex(ch.ID, ch.NovelID, ch.Title, summary, charsParsed, eventsParsed); err != nil {
		log.Printf("[chapter] search index error for chapter %d: %v", ch.ChapterNumber, err)
	}

	// Chunk content into overlapping segments and generate chunk embeddings
	s.chunkAndEmbedChapter(ctx, ch)

	// Sync to Neo4j knowledge graph
	if s.graphWriter != nil && s.graphWriter.IsEnabled() {
		novel, err := s.novelRepo.GetByID(ch.NovelID)
		if err != nil {
			log.Printf("[chapter] graph sync: get novel %d: %v", ch.NovelID, err)
		} else if err := s.graphWriter.SyncChapter(ctx, novel, ch, charsParsed, eventsParsed, relationsParsed, techniquesParsed); err != nil {
			log.Printf("[chapter] graph sync error for chapter %d: %v", ch.ChapterNumber, err)
		}
	}

	// Generate/update entity embeddings for semantic alias matching
	if s.entitySvc != nil {
		for _, c := range charsParsed {
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

	// Generate embeddings in batch
	contents := make([]string, len(records))
	for i := range records {
		contents[i] = records[i].Content
	}

	vecs, err := s.embedder.EmbedStrings(ctx, contents)
	if err != nil {
		log.Printf("[chunk] batch embedding error for chapter %d: %v", ch.ChapterNumber, err)
		return
	}
	if len(vecs) != len(records) {
		log.Printf("[chunk] embedding count mismatch for chapter %d: got %d, want %d", ch.ChapterNumber, len(vecs), len(records))
		return
	}

	chunkIDs := make([]int64, len(records))
	embeddings := make([]pgvector.Vector, len(records))
	for i := range records {
		vec := make([]float32, len(vecs[i]))
		for j, v := range vecs[i] {
			vec[j] = float32(v)
		}
		chunkIDs[i] = records[i].ID
		embeddings[i] = pgvector.NewVector(vec)
	}

	if err := s.chapterRepo.BatchUpdateChunkEmbedding(chunkIDs, embeddings); err != nil {
		log.Printf("[chunk] batch save embedding error for chapter %d: %v", ch.ChapterNumber, err)
	}
}

// FillChunkEmbeddings backfills missing chunk-level embeddings in batch.
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

		// Batch generate embeddings for all chunks in one API call
		contents := make([]string, len(chunks))
		for i := range chunks {
			contents[i] = chunks[i].Content
		}

		vecs, err := s.embedder.EmbedStrings(ctx, contents)
		if err != nil {
			log.Printf("[chunk] batch embedding error: %v", err)
			return
		}
		if len(vecs) != len(chunks) {
			log.Printf("[chunk] embedding count mismatch: got %d, want %d", len(vecs), len(chunks))
			return
		}

		// Build vector records
		chunkIDs := make([]int64, len(chunks))
		embeddings := make([]pgvector.Vector, len(chunks))
		for i := range chunks {
			vec := make([]float32, len(vecs[i]))
			for j, v := range vecs[i] {
				vec[j] = float32(v)
			}
			chunkIDs[i] = chunks[i].ID
			embeddings[i] = pgvector.NewVector(vec)
		}

		// Concurrently save embeddings in sub-batches (DB writes are I/O bound)
		var wg sync.WaitGroup
		sem := make(chan struct{}, s.concurrency)
		batchSize := (len(chunks) + s.concurrency - 1) / s.concurrency
		if batchSize < 1 {
			batchSize = 1
		}
		for start := 0; start < len(chunks); start += batchSize {
			end := start + batchSize
			if end > len(chunks) {
				end = len(chunks)
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(ids []int64, embs []pgvector.Vector) {
				defer wg.Done()
				defer func() { <-sem }()
				if err := s.chapterRepo.BatchUpdateChunkEmbedding(ids, embs); err != nil {
					log.Printf("[chunk] batch save embedding error: %v", err)
				}
			}(chunkIDs[start:end], embeddings[start:end])
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

// ResyncGraph re-syncs all processed chapters to Neo4j using existing extracted data.
// This is useful after Neo4j schema changes — it re-creates relationships and
// technique nodes without re-running AI summarization.
func (s *Service) ResyncGraph(ctx context.Context, novelID int64) error {
	if s.graphWriter == nil || !s.graphWriter.IsEnabled() {
		return nil
	}

	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		return fmt.Errorf("get novel: %w", err)
	}

	chapters, err := s.chapterRepo.ListAll(novelID)
	if err != nil {
		return fmt.Errorf("list chapters: %w", err)
	}

	synced := 0
	for _, ch := range chapters {
		if ch.Summary == "" {
			continue
		}
		chars, _ := model.UnmarshalCharacters(ch.Characters)
		events, _ := model.UnmarshalEvents(ch.Events)
		relations, _ := model.UnmarshalRelations(ch.Relations)
		techniques, _ := model.UnmarshalTechniques(ch.Techniques)

		if err := s.graphWriter.SyncChapter(ctx, novel, &ch, chars, events, relations, techniques); err != nil {
			log.Printf("[chapter] resync error for chapter %d: %v", ch.ChapterNumber, err)
			continue
		}
		synced++
	}

	log.Printf("[chapter] resync complete: %d/%d chapters synced to Neo4j", synced, len(chapters))
	return nil
}

// parseAIResponse extracts XML sections from the AI response.
func parseAIResponse(resp string) (summary string, chars []model.CharacterInfo, events []model.EventInfo, relations []model.CharacterRelation, techniques []model.TechniqueInfo) {
	summary = extractXML(resp, "summary")
	charsXML := extractXML(resp, "characters")
	eventsXML := extractXML(resp, "events")
	relationsXML := extractXML(resp, "relations")
	techniquesXML := extractXML(resp, "techniques")

	if charsXML != "" {
		json.Unmarshal([]byte(charsXML), &chars)
	}
	if eventsXML != "" {
		json.Unmarshal([]byte(eventsXML), &events)
	}
	if relationsXML != "" {
		json.Unmarshal([]byte(relationsXML), &relations)
	}
	if techniquesXML != "" {
		json.Unmarshal([]byte(techniquesXML), &techniques)
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
