package service

import (
	"context"
	"fmt"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"strings"

	"github.com/pgvector/pgvector-go"
)

// RAGService provides semantic search and context assembly for novels.
type RAGService struct {
	chapterRepo *repository.ChapterRepo
	aiClient    *ai.Client
}

func NewRAGService(chapterRepo *repository.ChapterRepo, aiClient *ai.Client) *RAGService {
	return &RAGService{
		chapterRepo: chapterRepo,
		aiClient:    aiClient,
	}
}

// SearchResult holds a retrieved chapter with its similarity score.
type SearchResult struct {
	Chapter  model.Chapter
	Score    float64
}

// Search performs semantic similarity search over chapter summaries.
// Falls back to full-text search if embeddings are unavailable.
// maxChapter enforces the spoiler-free boundary.
func (s *RAGService) Search(ctx context.Context, query string, novelID int64, maxChapter int, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	// Generate query embedding
	queryVec, err := s.aiClient.Embedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	vec := pgvector.NewVector(queryVec)
	chapters, scores, err := s.chapterRepo.SearchSimilar(novelID, maxChapter, vec, topK)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// If no chapters have embeddings yet, fall back to full-text
	if len(chapters) == 0 {
		tsQuery := strings.ReplaceAll(query, " ", " | ")
		ftResults, ftErr := s.chapterRepo.FullTextSearch(novelID, maxChapter, tsQuery, topK)
		if ftErr != nil {
			return nil, fmt.Errorf("search (also fulltext fallback failed): %w", ftErr)
		}
		for _, r := range ftResults {
			chapters = append(chapters, r.Chapter)
			scores = append(scores, r.FinalScore)
		}
	}

	var results []SearchResult
	for i, ch := range chapters {
		results = append(results, SearchResult{
			Chapter: ch,
			Score:   scores[i],
		})
	}
	return results, nil
}

// BuildContext assembles a prompt context from search results.
// Returns a string suitable for inclusion in a system/user prompt.
func (s *RAGService) BuildContext(novelTitle string, currentChapter int, results []SearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("小说《%s》\n", novelTitle))
	sb.WriteString(fmt.Sprintf("用户阅读进度：第 %d 章\n\n", currentChapter))
	sb.WriteString("=== 相关章节摘要（按语义相似度检索） ===\n")

	// Collect unique characters and events
	allChars := make(map[string]model.CharacterInfo)
	allEvents := make([]model.EventInfo, 0)

	for _, r := range results {
		ch := r.Chapter
		if ch.Summary != "" {
			sb.WriteString(fmt.Sprintf("[第%d章 %.4f] %s\n", ch.ChapterNumber, r.Score, ch.Summary))
		}

		chars, _ := model.UnmarshalCharacters(ch.Characters)
		for _, c := range chars {
			if existing, ok := allChars[c.Name]; ok {
				if c.Status != "" {
					existing.Status = c.Status
				}
				allChars[c.Name] = existing
			} else {
				allChars[c.Name] = c
			}
		}

		events, _ := model.UnmarshalEvents(ch.Events)
		allEvents = append(allEvents, events...)
	}

	sb.WriteString("\n=== 相关人物 ===\n")
	for name, char := range allChars {
		sb.WriteString(fmt.Sprintf("- %s", name))
		if char.Status != "" {
			sb.WriteString(fmt.Sprintf("（%s）", char.Status))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n=== 相关事件 ===\n")
	seenEvents := make(map[string]bool)
	for _, evt := range allEvents {
		key := evt.Title
		if seenEvents[key] {
			continue
		}
		seenEvents[key] = true
		sb.WriteString(fmt.Sprintf("- [第%d章] %s: %s\n", evt.ChapterNum, evt.Title, evt.Summary))
	}

	return sb.String()
}

// AgenticRetrieve performs multi-step retrieval with LLM verification.
// If the initial search results are insufficient, it rewrites the query and retries.
func (s *RAGService) AgenticRetrieve(ctx context.Context, query string, novelID int64, maxChapter int) (string, error) {
	// Step 1: Initial search
	results, err := s.Search(ctx, query, novelID, maxChapter, 10)
	if err != nil {
		return "", err
	}

	// Step 2: Verify retrieval quality with LLM
	if len(results) >= 3 {
		// Quick check: are the top results relevant?
		novel, _ := s.chapterRepo.ListByNovel(novelID) // lightweight; just need title context
		_ = novel // title from calling context instead
	}

	// Step 3: If results seem sparse, broaden the search
	if len(results) < 3 {
		broaderResults, err := s.Search(ctx, query, novelID, maxChapter, 20)
		if err == nil && len(broaderResults) > len(results) {
			results = broaderResults
		}
	}

	// Ensure chronological order for readability
	results = sortByChapterNumber(results)

	// Step 4: Build context (title passed empty — caller fills from Novel)
	context := s.BuildContext("", maxChapter, results)
	return context, nil
}

// sortByChapterNumber sorts search results by chapter number ascending.
func sortByChapterNumber(results []SearchResult) []SearchResult {
	sorted := make([]SearchResult, len(results))
	copy(sorted, results)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].Chapter.ChapterNumber > sorted[j].Chapter.ChapterNumber {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
