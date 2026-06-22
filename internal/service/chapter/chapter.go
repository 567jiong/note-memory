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
	"sync/atomic"
	"time"

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
	searchSvc   *search.Service
	graphWriter *graph.GraphWriter
	entitySvc   *entity.Service
	concurrency int
}

func NewService(chapterRepo *repository.ChapterRepo, novelRepo *repository.NovelRepo, chatModel einomodel.ToolCallingChatModel, embedder embedding.Embedder, searchSvc *search.Service, graphWriter *graph.GraphWriter, entitySvc *entity.Service) *Service {
	return &Service{
		chapterRepo: chapterRepo,
		novelRepo:   novelRepo,
		chatModel:   chatModel,
		embedder:    embedder,
		searchSvc:   searchSvc,
		graphWriter: graphWriter,
		entitySvc:   entitySvc,
		concurrency: 8,
	}
}

// ParseAllChapters summarizes all unprocessed chapters for a novel.
// Processes chapters concurrently and logs progress at each step.
func (s *Service) ParseAllChapters(ctx context.Context, novelID int64) {
	// Get novel for total chapter count
	novel, err := s.novelRepo.GetByID(novelID)
	if err != nil {
		log.Printf("[chapter] ❌ 获取小说信息失败 novel=%d: %v", novelID, err)
		return
	}
	totalChapters := novel.TotalChapters

	// Count already processed chapters to calculate starting point
	processedCount := totalChapters
	remaining, _ := s.chapterRepo.CountUnprocessed(novelID)
	if remaining >= 0 {
		processedCount = totalChapters - remaining
	}

	log.Printf("[chapter] ═══════════════════════════════════════════")
	log.Printf("[chapter] 📖 开始解析小说《%s》(ID=%d)", novel.Title, novelID)
	log.Printf("[chapter]    总章节: %d | 已处理: %d | 待处理: %d | 并发: %d",
		totalChapters, processedCount, remaining, s.concurrency)
	log.Printf("[chapter] ═══════════════════════════════════════════")

	var completed int64
	startTime := time.Now()
	lastLog := time.Now()

	for {
		chapters, err := s.chapterRepo.ListUnprocessed(novelID, s.concurrency)
		if err != nil {
			log.Printf("[chapter] ❌ 查询待处理章节失败: %v", err)
			return
		}
		if len(chapters) == 0 {
			elapsed := time.Since(startTime).Round(time.Second)
			log.Printf("[chapter] ═══════════════════════════════════════════")
			log.Printf("[chapter] ✅ 《%s》全部章节处理完成！共 %d 章，耗时 %s",
				novel.Title, totalChapters, elapsed)
			log.Printf("[chapter] ═══════════════════════════════════════════")
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

				// Progress: log every 5% or every 30 seconds
				done := atomic.AddInt64(&completed, 1)
				currentTotal := processedCount + int(done)
				pct := float64(currentTotal) / float64(totalChapters) * 100
				if int(pct)%5 == 0 || time.Since(lastLog) > 30*time.Second {
					elapsed := time.Since(startTime).Round(time.Second)
					eta := time.Duration(0)
					if done > 0 {
						eta = time.Duration(float64(elapsed)/float64(done)*float64(int64(remaining)-done)) * time.Nanosecond
					}
					log.Printf("[chapter] 📊 进度: %d/%d (%.0f%%) | 耗时: %s | 预计剩余: %s",
						currentTotal, totalChapters, pct, elapsed, eta.Round(time.Second))
					lastLog = time.Now()
				}
			}(ch)
		}
		wg.Wait()
	}
}

// summarizeChapter sends a chapter to AI for summarization, then chunks the content
// and generates chunk-level embeddings for semantic search.
// Each major step is logged with timing for visibility into the processing pipeline.
func (s *Service) summarizeChapter(ctx context.Context, ch *model.Chapter) {
	tStart := time.Now()

	// ── Step 1: AI summary ──
	t0 := time.Now()
	sr, err := newSummarizerAgent(ctx, s.chatModel)
	if err != nil {
		log.Printf("[chapter] ❌ 第%d章 创建摘要Agent失败: %v", ch.ChapterNumber, err)
		return
	}

	resp, err := runSummarizer(ctx, sr, ch.Title, ch.Content)
	if err != nil {
		log.Printf("[chapter] ❌ 第%d章 AI摘要失败: %v", ch.ChapterNumber, err)
		return
	}

	summary, charsParsed, eventsParsed, relationsParsed, techniquesParsed := parseAIResponse(resp)

	chars, _ := model.MarshalCharacters(charsParsed)
	events, _ := model.MarshalEvents(eventsParsed)
	relationsJSON, _ := model.MarshalRelations(relationsParsed)
	techniquesJSON, _ := model.MarshalTechniques(techniquesParsed)

	if err := s.chapterRepo.UpdateSummary(ch.ID, summary, chars, events, relationsJSON, techniquesJSON); err != nil {
		log.Printf("[chapter] ❌ 第%d章 保存摘要失败: %v", ch.ChapterNumber, err)
		return
	}

	log.Printf("[chapter]   ├─ 第%d章 AI摘要完成 (角色:%d 事件:%d 关系:%d 功法:%d) [%v]",
		ch.ChapterNumber, len(charsParsed), len(eventsParsed), len(relationsParsed), len(techniquesParsed),
		time.Since(t0).Round(time.Millisecond))

	// ── Step 2: Full-text search index ──
	t0 = time.Now()
	if err := s.searchSvc.UpdateSearchIndex(ch.ID, ch.NovelID, ch.Title, summary, charsParsed, eventsParsed); err != nil {
		log.Printf("[chapter]   ├─ 第%d章 全文索引失败: %v", ch.ChapterNumber, err)
	} else {
		log.Printf("[chapter]   ├─ 第%d章 全文索引完成 [%v]", ch.ChapterNumber, time.Since(t0).Round(time.Millisecond))
	}

	// ── Step 3: Chunk + embedding ──
	t0 = time.Now()
	s.chunkAndEmbedChapter(ctx, ch)
	log.Printf("[chapter]   ├─ 第%d章 分块+向量嵌入 (%d 块) [%v]",
		ch.ChapterNumber, countChunks(ch.Content), time.Since(t0).Round(time.Millisecond))

	// ── Step 4: Neo4j knowledge graph ──
	if s.graphWriter != nil && s.graphWriter.IsEnabled() {
		t0 = time.Now()
		novel, err := s.novelRepo.GetByID(ch.NovelID)
		if err != nil {
			log.Printf("[chapter]   ├─ 第%d章 图谱同步: 获取小说失败: %v", ch.ChapterNumber, err)
		} else if err := s.graphWriter.SyncChapter(ctx, novel, ch, charsParsed, eventsParsed, relationsParsed, techniquesParsed); err != nil {
			log.Printf("[chapter]   ├─ 第%d章 图谱同步失败: %v", ch.ChapterNumber, err)
		} else {
			log.Printf("[chapter]   ├─ 第%d章 图谱同步完成 [%v]", ch.ChapterNumber, time.Since(t0).Round(time.Millisecond))
		}
	}

	// ── Step 5: Entity embeddings ──
	if s.entitySvc != nil && len(charsParsed) > 0 {
		t0 = time.Now()
		for _, c := range charsParsed {
			if err := s.entitySvc.UpsertEntityFromChapter(ctx, ch.NovelID, c); err != nil {
				log.Printf("[chapter]   ├─ 第%d章 实体嵌入(%s)失败: %v", ch.ChapterNumber, c.Name, err)
			}
		}
		log.Printf("[chapter]   ├─ 第%d章 实体嵌入完成 (%d个角色) [%v]",
			ch.ChapterNumber, len(charsParsed), time.Since(t0).Round(time.Millisecond))
	}

	log.Printf("[chapter]   └─ 第%d章 ✅ 全部完成 (总耗时: %v)",
		ch.ChapterNumber, time.Since(tStart).Round(time.Millisecond))
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
