package service

import (
	"context"
	"encoding/json"
	"fmt"
	"note-memory/internal/ai"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"sort"
	"strings"

	"github.com/pgvector/pgvector-go"
)

// RAGService provides semantic search and context assembly for novels.
type RAGService struct {
	chapterRepo *repository.ChapterRepo
	aiClient    *ai.Client
	searchSvc   *SearchService
}

func NewRAGService(chapterRepo *repository.ChapterRepo, aiClient *ai.Client, searchSvc *SearchService) *RAGService {
	return &RAGService{
		chapterRepo: chapterRepo,
		aiClient:    aiClient,
		searchSvc:   searchSvc,
	}
}

// SearchResult holds a retrieved chapter with its similarity score.
type SearchResult struct {
	Chapter model.Chapter
	Score   float64
}

// AgenticResult holds the complete output of an Agentic RAG retrieval.
type AgenticResult struct {
	Context    string          // 最终拼装好的上下文字符串
	Chapters   []model.Chapter // 去重后的检索结果（按章节号排序）
	Iterations int             // 实际检索轮数
	Verified   bool            // 是否通过了 LLM 验证
}

// retrievalVerdict is the structured output from the LLM verification step.
type retrievalVerdict struct {
	Sufficient     bool   `json:"sufficient"`
	Reasoning      string `json:"reasoning"`
	Missing        string `json:"missing"`
	RewrittenQuery string `json:"rewritten_query"`
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

// ---- Agentic RAG ----

const (
	maxAgenticIterations = 3
	agenticTopK          = 10
)

// AgenticRetrieve performs multi-step retrieval with LLM verification and query rewriting.
//
// Loop:
//  1. Hybrid search (semantic + full-text + alias expansion)
//  2. LLM verification: are the results sufficient to answer the query?
//  3. If not → LLM rewrites query → go to 1 (max 3 iterations)
//  4. Deduplicate, sort by chapter number, build context
func (s *RAGService) AgenticRetrieve(ctx context.Context, query string, novelID int64, maxChapter int, novelTitle string) (*AgenticResult, error) {
	type scoredChapter struct {
		chapter model.Chapter
		score   float64
	}

	seen := make(map[int64]bool)              // chapter ID → seen
	var allChapters []scoredChapter            // accumulated unique results
	currentQuery := query
	verified := false
	iteration := 0

	for iteration = 1; iteration <= maxAgenticIterations; iteration++ {
		// Step 1: Hybrid search
		results, err := s.searchSvc.HybridSearch(ctx, currentQuery, novelID, maxChapter, agenticTopK)
		if err != nil {
			// If hybrid search fails, try semantic-only as fallback
			sr, err2 := s.Search(ctx, currentQuery, novelID, maxChapter, agenticTopK)
			if err2 != nil {
				return nil, fmt.Errorf("agentic search failed (hybrid: %v, semantic: %v)", err, err2)
			}
			results = convertSearchResults(sr)
		}

		// Accumulate unique results
		for _, r := range results {
			if seen[r.Chapter.ID] {
				continue
			}
			seen[r.Chapter.ID] = true
			allChapters = append(allChapters, scoredChapter{
				chapter: r.Chapter,
				score:   r.FinalScore,
			})
		}

		// If we have no results at all, skip verification and try broader search
		if len(results) == 0 {
			continue
		}

		// Step 2: LLM verification
		verdict, err := s.verifyRetrieval(ctx, query, maxChapter, results)
		if err != nil {
			// LLM verification failed → accept current results as-is
			fmt.Printf("[rag] LLM verification failed (iter %d): %v — accepting current results\n", iteration, err)
			verified = false
			break
		}

		if verdict.Sufficient {
			verified = true
			break
		}

		// Step 3: Rewrite query for next iteration
		if verdict.RewrittenQuery == "" || verdict.RewrittenQuery == currentQuery {
			// No useful rewrite — stop iterating
			break
		}
		currentQuery = verdict.RewrittenQuery
		fmt.Printf("[rag] query rewritten for iteration %d: %q → %q (missing: %s)\n",
			iteration+1, query, currentQuery, verdict.Missing)
	}

	// Step 4: Sort by chapter number (chronological order)
	sort.Slice(allChapters, func(i, j int) bool {
		return allChapters[i].chapter.ChapterNumber < allChapters[j].chapter.ChapterNumber
	})

	// Convert to SearchResult for BuildContext
	var contextResults []SearchResult
	var chapters []model.Chapter
	for _, sc := range allChapters {
		contextResults = append(contextResults, SearchResult{
			Chapter: sc.chapter,
			Score:   sc.score,
		})
		chapters = append(chapters, sc.chapter)
	}

	// Trim context to ~3000 Chinese chars (~6000 bytes) if too long
	contextResults = trimContextByScore(contextResults, 3000)

	// Build context
	context := s.BuildContext(novelTitle, maxChapter, contextResults)

	return &AgenticResult{
		Context:    context,
		Chapters:   chapters,
		Iterations: iteration,
		Verified:   verified,
	}, nil
}

// verifyRetrieval asks the LLM to judge whether the retrieved chapters are sufficient
// to answer the user's query. Returns a structured verdict.
func (s *RAGService) verifyRetrieval(ctx context.Context, query string, maxChapter int, results []model.HybridSearchResult) (*retrievalVerdict, error) {
	// Build a compact summary of retrieved chapters
	var summaries strings.Builder
	for i, r := range results {
		if i >= 5 {
			break // Only send top 5 to the verifier (saves tokens)
		}
		if r.Chapter.Summary != "" {
			summaries.WriteString(fmt.Sprintf("[第%d章] %s\n", r.Chapter.ChapterNumber, r.Chapter.Summary))
		}
	}

	sysPrompt := `你是一个检索质量评估器。你的任务是判断当前检索到的章节摘要是否包含足够的信息来回答用户的问题。

请按以下 JSON 格式输出（不要输出其他内容）：
{
  "sufficient": true或false,
  "reasoning": "评估理由（一句话）",
  "missing": "如果不足，缺失什么信息（一句话；如果充足则为空）",
  "rewritten_query": "如果不足，改写后的查询词（中文关键词，用空格分隔；如果充足则为空）"
}

判断标准：
- "sufficient": true — 章节摘要中包含回答问题的关键信息
- "sufficient": false — 关键信息缺失，需要改写查询重新检索
- 改写查询应聚焦于缺失的具体信息（人名、事件、物品等关键词）`

	userPrompt := fmt.Sprintf(`用户问题：%s
用户阅读进度：第 1 ~ %d 章

检索到的章节摘要：
%s
请评估这些检索结果是否足以回答用户的问题。`, query, maxChapter, summaries.String())

	resp, err := s.aiClient.Chat(ctx, []ai.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userPrompt},
	}, 0.3, 300)
	if err != nil {
		return nil, fmt.Errorf("LLM verification call failed: %w", err)
	}

	verdict, err := parseVerdict(resp)
	if err != nil {
		return nil, fmt.Errorf("parse verdict JSON: %w", err)
	}
	return verdict, nil
}

// parseVerdict extracts a retrievalVerdict from an LLM response, handling common format issues.
func parseVerdict(raw string) (*retrievalVerdict, error) {
	cleaned := strings.TrimSpace(raw)

	// Strip ```json fences if present
	if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	}

	var v retrievalVerdict
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		// Try to find a JSON object in the response
		start := strings.Index(cleaned, "{")
		end := strings.LastIndex(cleaned, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(cleaned[start:end+1]), &v); err2 != nil {
				return nil, fmt.Errorf("json unmarshal failed: %w (cleaned: %s)", err, cleaned[:min(len(cleaned), 200)])
			}
		} else {
			return nil, fmt.Errorf("no JSON object found in response: %s", cleaned[:min(len(cleaned), 200)])
		}
	}

	return &v, nil
}

// ---- Helpers ----

// convertSearchResults converts []model.HybridSearchResult to the format used by Search().
func convertSearchResults(sr []SearchResult) []model.HybridSearchResult {
	result := make([]model.HybridSearchResult, 0, len(sr))
	for _, r := range sr {
		result = append(result, model.HybridSearchResult{
			Chapter:    r.Chapter,
			FinalScore: r.Score,
		})
	}
	return result
}

// trimContextByScore limits context to roughly maxChars Chinese characters,
// keeping the highest-scoring results.
func trimContextByScore(results []SearchResult, maxChars int) []SearchResult {
	// Estimate current size
	totalChars := 0
	for _, r := range results {
		if r.Chapter.Summary != "" {
			totalChars += len([]rune(r.Chapter.Summary))
		}
	}
	if totalChars <= maxChars {
		return results
	}

	// Sort by score descending, keep top until cap reached
	sorted := make([]SearchResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	var trimmed []SearchResult
	chars := 0
	for _, r := range sorted {
		chars += len([]rune(r.Chapter.Summary))
		trimmed = append(trimmed, r)
		if chars >= maxChars {
			break
		}
	}

	// Re-sort by chapter number
	sort.Slice(trimmed, func(i, j int) bool {
		return trimmed[i].Chapter.ChapterNumber < trimmed[j].Chapter.ChapterNumber
	})

	return trimmed
}
