package search

import (
	"context"
	"fmt"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"encoding/json"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/tools"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

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

	// per-novel custom dict paths
	dictMu    sync.RWMutex
	novelDict map[int64]string // novelID → custom dict file path
}

func NewService(chapterRepo *repository.ChapterRepo, embedder embedding.Embedder, entitySvc *entity.Service) *Service {
	return &Service{
		chapterRepo: chapterRepo,
		embedder:    embedder,
		entitySvc:   entitySvc,
		novelDict:   make(map[int64]string),
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

// ---- Custom Dictionary ----

// buildCustomDict creates a custom dictionary file for a novel from extracted entities.
// Returns the path to the dict file, or empty string if no entities available.
func (s *Service) buildCustomDict(novelID int64) string {
	aliases, err := s.chapterRepo.ListAliases(novelID)
	if err != nil || len(aliases) == 0 {
		return ""
	}

	// Collect unique entity names (canonical + aliases)
	terms := make(map[string]struct{})
	for _, a := range aliases {
		terms[a.Name] = struct{}{}
		for _, alias := range a.Aliases {
			terms[alias] = struct{}{}
		}
	}

	if len(terms) == 0 {
		return ""
	}

	// Build dict content: jieba format = "word freq tag"
	// Give entity terms high frequency (100) so they're always kept as whole words
	var sb strings.Builder
	for term := range terms {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 {
			continue // skip single-char
		}
		sb.WriteString(fmt.Sprintf("%s 100 n\n", term))
	}

	// Write to temp file
	dir := filepath.Join(os.TempDir(), "note-memory-dicts")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, fmt.Sprintf("novel_%d.txt", novelID))
	os.WriteFile(path, []byte(sb.String()), 0644)

	s.dictMu.Lock()
	s.novelDict[novelID] = path
	s.dictMu.Unlock()

	return path
}

// loadCustomDict loads a custom dictionary into the segmenter for a specific novel.
func (s *Service) loadCustomDict(path string) *gse.Segmenter {
	// Create a fresh segmenter with custom dict
	seg := new(gse.Segmenter)
	if path != "" {
		seg.LoadDict(path) // custom dict
	} else {
		seg.LoadDict() // default only
	}
	return seg
}

// ---- Tokenization ----

// tokenizeText tokenizes text using jieba + custom dict + bigram fallback.
// Returns space-separated tokens for tsvector indexing.
func (s *Service) tokenizeText(text string, customDictPath string) string {
	if text == "" {
		return ""
	}

	var seg *gse.Segmenter
	if customDictPath != "" {
		seg = s.loadCustomDict(customDictPath)
	} else {
		seg = s.getSegmenter()
	}

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
func (s *Service) tokenizeForQuery(query string, customDictPath string) string {
	if query == "" {
		return ""
	}

	var seg *gse.Segmenter
	if customDictPath != "" {
		seg = s.loadCustomDict(customDictPath)
	} else {
		seg = s.getSegmenter()
	}

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
	customDict := s.buildCustomDict(novelID)

	var parts []string

	// Chapter title
	if chapterTitle != "" {
		parts = append(parts, s.tokenizeText(chapterTitle, customDict))
	}

	// Summary
	if summary != "" {
		parts = append(parts, s.tokenizeText(summary, customDict))
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

// UpsertChapterAliases incrementally writes aliases from a single chapter's characters.
// Called per-chapter after AI summarization, avoiding the fragility of batch-at-end.
func (s *Service) UpsertChapterAliases(novelID int64, characters []model.CharacterInfo) error {
	if len(characters) == 0 {
		return nil
	}
	var entries []model.EntityAlias
	for _, c := range characters {
		if c.Name == "" {
			continue
		}
		entries = append(entries, model.EntityAlias{
			NovelID:       novelID,
			CanonicalName: c.Name,
			Alias:         c.Name,
		})
		for _, a := range c.Aliases {
			if a == "" {
				continue
			}
			entries = append(entries, model.EntityAlias{
				NovelID:       novelID,
				CanonicalName: c.Name,
				Alias:         a,
			})
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return s.chapterRepo.UpsertAliases(entries)
}

// RefreshDictForNovel rebuilds the custom jieba dictionary for a novel after incremental updates.
func (s *Service) RefreshDictForNovel(novelID int64) {
	s.buildCustomDict(novelID)
}

// RebuildAliasIndex rebuilds entity_aliases for a novel from all chapters.
// Use this for one-time repair; day-to-day aliases are written incrementally by UpsertChapterAliases.
func (s *Service) RebuildAliasIndex(novelID int64) error {
	chapters, err := s.chapterRepo.ListByNovel(novelID)
	if err != nil {
		return fmt.Errorf("list chapters: %w", err)
	}

	type aliasEntry struct {
		Name    string
		Aliases []string
	}
	aliasMap := make(map[string]aliasEntry)

	for _, ch := range chapters {
		chars, _ := model.UnmarshalCharacters(ch.Characters)
		for _, c := range chars {
			if c.Name == "" {
				continue
			}
			existing := aliasMap[c.Name]
			existing.Name = c.Name
			for _, a := range c.Aliases {
				if a == "" {
						continue
					}
				found := false
				for _, ea := range existing.Aliases {
					if ea == a {
						found = true
						break
					}
				}
				if !found {
					existing.Aliases = append(existing.Aliases, a)
				}
			}
			aliasMap[c.Name] = existing
		}
	}

	var entries []model.EntityAlias
	for _, v := range aliasMap {
		entries = append(entries, model.EntityAlias{
			NovelID:       novelID,
			CanonicalName: v.Name,
			Alias:         v.Name,
		})
		for _, a := range v.Aliases {
			entries = append(entries, model.EntityAlias{
				NovelID:       novelID,
				CanonicalName: v.Name,
				Alias:         a,
			})
		}
	}

	if err := s.chapterRepo.UpsertAliases(entries); err != nil {
		return err
	}

	// Rebuild custom dictionary with latest entities
	s.buildCustomDict(novelID)

	return nil
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

	// 1a. Expand query with entity embedding semantic match (new)
	if s.entitySvc != nil {
		entities, err := s.entitySvc.SearchEntities(ctx, query, novelID, 3)
		if err == nil && len(entities) > 0 {
			// Append matched entity names to the query for broader recall
			query = query + " " + strings.Join(entities, " ")
		}
	}

	// 1b. Expand query with alias resolution (fallback)
	expandedQuery := s.expandWithAliases(query, novelID)

	// 2. Tokenize query for full-text search (jieba + custom dict)
	customDict := s.getNovelDict(novelID)
	tsQuery := s.tokenizeForQuery(expandedQuery, customDict)

	// 3. Try to generate query embedding; fall back to full-text if unavailable
	vecs, err := s.embedder.EmbedStrings(ctx, []string{expandedQuery})
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

// getNovelDict returns the custom dict path for a novel, or empty string.
func (s *Service) getNovelDict(novelID int64) string {
	s.dictMu.RLock()
	defer s.dictMu.RUnlock()
	return s.novelDict[novelID]
}

// expandWithAliases expands query with known aliases, but only when unambiguous.
// If "厉飞雨" is both 韩立's alias AND a real character's name, it will NOT be expanded.
func (s *Service) expandWithAliases(query string, novelID int64) string {
	aliases, err := s.chapterRepo.ListAliases(novelID)
	if err != nil || len(aliases) == 0 {
		return query
	}

	// Step 1: Build alias → canonical mapping
	aliasMap := make(map[string]string)
	for _, a := range aliases {
		for _, alias := range a.Aliases {
			aliasMap[alias] = a.Name
		}
		aliasMap[a.Name] = a.Name
	}

	// Step 2: Build set of ALL canonical names for conflict detection
	canonicalSet := make(map[string]bool)
	for _, a := range aliases {
		canonicalSet[a.Name] = true
	}

	// Step 3: Expand only unambiguous aliases
	expanded := query
	for alias, canonical := range aliasMap {
		if alias == canonical {
			continue
		}
		// Conflict check: if this alias string IS itself a canonical name of another character,
		// don't expand — let the hybrid search + LLM disambiguate
		if canonicalSet[alias] && canonical != alias {
			continue
		}
		if strings.Contains(expanded, alias) && !strings.Contains(expanded, canonical) {
			expanded = expanded + " " + canonical
		}
	}
	return expanded
}

// isCJK checks if a rune is CJK.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}

var _ = utf8.RuneCountInString

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
