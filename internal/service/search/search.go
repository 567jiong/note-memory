package search

import (
	"context"
	"fmt"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"encoding/json"
	"log"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/tools"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/go-ego/gse"
	"github.com/pgvector/pgvector-go"
)

// Service provides hybrid search with jieba tokenization.
type Service struct {
	chapterRepo *repository.ChapterRepo
	embedder    embedding.Embedder
	entitySvc   *entity.Service

	// jieba segmenter (lazy init, thread-safe after first use)
	segmenter *gse.Segmenter
	segOnce   sync.Once
}

func NewService(chapterRepo *repository.ChapterRepo, embedder embedding.Embedder, entitySvc *entity.Service) *Service {
	return &Service{
		chapterRepo: chapterRepo,
		embedder:    embedder,
		entitySvc:   entitySvc,
	}
}

// getSegmenter returns the jieba segmenter (lazy init).
func (s *Service) getSegmenter() *gse.Segmenter {
	s.segOnce.Do(func() {
		seg := new(gse.Segmenter)
		// Use gse's built-in dictionary (jieba compatible)
		seg.LoadDict()
		s.segmenter = seg
	})
	return s.segmenter
}

// ---- Tokenization ----

// tokenizeText tokenizes text using jieba + bigram fallback.
// Returns space-separated tokens for tsvector indexing.
func (s *Service) tokenizeText(text string, novelID int64) string {
	if text == "" {
		return ""
	}

	seg := s.getSegmenter()

	// Step 1: Jieba tokenization
	jiebaTokens := seg.Cut(text, true) // use HMM

	// Step 2: Bigram fallback — extract all 2-grams as backup tokens
	// This ensures unknown compound terms can still partially match
	bigrams := extractBigrams(text)

	// Step 3: Merge — deduplicate jieba tokens + bigrams
	seen := make(map[string]struct{})
	var result []string
	for _, t := range jiebaTokens {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) < 1 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}
	for _, b := range bigrams {
		if _, ok := seen[b]; ok {
			continue
		}
		seen[b] = struct{}{}
		result = append(result, b)
	}

	return strings.Join(result, " ")
}

// tokenizeForQuery tokenizes a search query for tsquery.
// Uses jieba tokens only (no bigram fallback needed — query terms should be precise).
func (s *Service) tokenizeForQuery(query string, novelID int64) string {
	if query == "" {
		return ""
	}

	seg := s.getSegmenter()

	tokens := seg.Cut(query, true)

	// Deduplicate and filter
	seen := make(map[string]struct{})
	var parts []string
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) < 1 {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		parts = append(parts, t)
	}

	// Add single char fallback for short queries
	if len([]rune(query)) <= 3 {
		for _, r := range query {
			ch := string(r)
			if !isCJK(r) {
				continue
			}
			if _, ok := seen[ch]; !ok {
				parts = append(parts, ch)
			}
		}
	}

	if len(parts) == 0 {
		return strings.ReplaceAll(query, " ", " & ")
	}
	return strings.Join(parts, " | ")
}

// extractBigrams generates all consecutive 2-character bigrams from text.
// Used as fallback to ensure partial matches for out-of-vocabulary terms.
func extractBigrams(text string) []string {
	runes := []rune(text)
	if len(runes) < 2 {
		return nil
	}
	var result []string
	for i := 0; i < len(runes)-1; i++ {
		if isCJK(runes[i]) && isCJK(runes[i+1]) {
			result = append(result, string([]rune{runes[i], runes[i+1]}))
		}
	}
	return result
}

// ---- Search Index Management ----

// BuildSearchText generates tokenized search text from a chapter's data.
func (s *Service) BuildSearchText(novelID int64, chapterTitle, summary string, characters []model.CharacterInfo, events []model.EventInfo) string {
	var parts []string

	// Chapter title
	if chapterTitle != "" {
		parts = append(parts, s.tokenizeText(chapterTitle, novelID))
	}

	// Summary
	if summary != "" {
		parts = append(parts, s.tokenizeText(summary, novelID))
	}

	// Character names + aliases (preserved as whole words — added AFTER tokenization)
	for _, c := range characters {
		parts = append(parts, c.Name)
		for _, a := range c.Aliases {
			parts = append(parts, a)
		}
	}

	// Event titles (preserved as whole words)
	for _, e := range events {
		parts = append(parts, e.Title)
	}

	return strings.Join(parts, " ")
}

// UpdateSearchIndex updates search_text + tsv for a chapter.
func (s *Service) UpdateSearchIndex(chapterID int64, novelID int64, chapterTitle, summary string, characters []model.CharacterInfo, events []model.EventInfo) error {
	searchText := s.BuildSearchText(novelID, chapterTitle, summary, characters, events)
	return s.chapterRepo.UpdateSearchText(chapterID, searchText)
}

// ---- Hybrid Search ----

// HybridSearch combines chunk-level semantic search with tsvector full-text search.
// Weights: semantic 0.7 + full-text 0.3
// Semantic branch searches chapter_chunks.embedding and aggregates by chapter.
// Falls back to pure full-text search if embeddings are unavailable.
func (s *Service) HybridSearch(ctx context.Context, query string, novelID int64, maxChapter int, topK int) ([]model.HybridSearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	// 1. Expand query with entity embedding semantic match
	if s.entitySvc != nil {
		entities, err := s.entitySvc.SearchEntities(ctx, query, novelID, 3)
		if err != nil {
			log.Printf("[search] entity expansion warning: %v", err)
		} else if len(entities) > 0 {
			// Append matched entity names to the query for broader recall
			query = query + " " + strings.Join(entities, " ")
		}
	}

	// 2. Tokenize query for full-text search (jieba)
	tsQuery := s.tokenizeForQuery(query, novelID)

	// 3. Try to generate query embedding; fall back to full-text if unavailable
	vecs, err := s.embedder.EmbedStrings(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return s.chapterRepo.FullTextSearch(novelID, maxChapter, tsQuery, topK)
	}
	queryVec := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		queryVec[i] = float32(v)
	}

	vec := pgvector.NewVector(queryVec)

	// 4. Chunk-level semantic search (aggregated by chapter)
	semChapters, semScores, semErr := s.chapterRepo.SearchChunks(novelID, maxChapter, vec, topK*2)
	if semErr != nil {
		return nil, fmt.Errorf("chunk semantic search: %w", semErr)
	}

	// 5. Full-text search
	ftResults, ftErr := s.chapterRepo.FullTextSearch(novelID, maxChapter, tsQuery, topK*2)
	if ftErr != nil {
		return nil, fmt.Errorf("fulltext search: %w", ftErr)
	}

	// 6. Merge semantic + full-text with weighted scoring
	return mergeHybridResults(semChapters, semScores, ftResults), nil
}

// mergeHybridResults combines semantic and full-text results with weights 0.7/0.3.
func mergeHybridResults(semChapters []model.Chapter, semScores []float64, ftResults []model.HybridSearchResult) []model.HybridSearchResult {
	const semWeight = 0.7
	const textWeight = 0.3

	// Build lookup: chapterID → best scores
	type merged struct {
		chapter   model.Chapter
		semScore  float64
		textScore float64
	}
	byID := make(map[int64]*merged)

	for i, ch := range semChapters {
		byID[ch.ID] = &merged{chapter: ch, semScore: semScores[i]}
	}
	for _, r := range ftResults {
		if m, ok := byID[r.Chapter.ID]; ok {
			m.textScore = r.FinalScore
		} else {
			byID[r.Chapter.ID] = &merged{chapter: r.Chapter, textScore: r.FinalScore}
		}
	}

	// Calculate final scores and collect
	var results []model.HybridSearchResult
	for _, m := range byID {
		results = append(results, model.HybridSearchResult{
			Chapter:    m.chapter,
			SemScore:   m.semScore,
			TextScore:  m.textScore,
			FinalScore: semWeight*m.semScore + textWeight*m.textScore,
		})
	}

	// Sort by final score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].FinalScore > results[j].FinalScore
	})
	return results
}

// isCJK checks if a rune is CJK.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}

// ---- Tool factory for ADK agents ----

// SearchTool returns a closure matching tools.Deps.SearchFunc that delegates to HybridSearch.
// The returned function formats results as []tools.ChapterResult JSON.
func (s *Service) SearchTool() func(ctx context.Context, query string, novelID int64, maxChapter int, topK int) (string, error) {
	return func(ctx context.Context, query string, novelID int64, maxChapter int, topK int) (string, error) {
		results, err := s.HybridSearch(ctx, query, novelID, maxChapter, topK)
		if err != nil {
			return "", err
		}
		var out []tools.ChapterResult
		for _, r := range results {
			if len(out) >= topK {
				break
			}
			out = append(out, tools.ChapterResult{
				ChapterNum: r.Chapter.ChapterNumber,
				Score:      r.FinalScore,
				Summary:    r.Chapter.Summary,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}

// ChaptersTool returns a closure matching tools.Deps.ChaptersFunc.
// It fetches chapter summaries for a given range, with "recent N" mode triggered
// when start=0 (end holds N).
func (s *Service) ChaptersTool() func(ctx context.Context, novelID int64, start, end, maxChapter int) (string, error) {
	return func(ctx context.Context, novelID int64, start, end, maxChapter int) (string, error) {
		var chapters []model.Chapter
		var err error
		if start <= 0 {
			// "recent N" mode: end holds the value of N
			n := end
			if n <= 0 {
				n = 5
			}
			chapters, err = s.chapterRepo.ListRecentChapters(novelID, maxChapter, n)
		} else {
			chapters, err = s.chapterRepo.ListChaptersInRange(novelID, start, end, maxChapter)
		}
		if err != nil {
			return "", err
		}
		var out []tools.ChapterSummary
		for _, ch := range chapters {
			chars, _ := model.UnmarshalCharacters(ch.Characters)
			charNames := make([]string, 0, len(chars))
			for _, c := range chars {
				charNames = append(charNames, c.Name)
			}
			events, _ := model.UnmarshalEvents(ch.Events)
			eventTitles := make([]string, 0, len(events))
			for _, e := range events {
				eventTitles = append(eventTitles, e.Title)
			}
			out = append(out, tools.ChapterSummary{
				ChapterNum: ch.ChapterNumber,
				Title:      ch.Title,
				Summary:    ch.Summary,
				Characters: charNames,
				Events:     eventTitles,
			})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}
}
