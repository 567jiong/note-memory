package search

import (
	"context"
	"fmt"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"note-memory/internal/graph"
	"note-memory/internal/service/entity"
	"note-memory/internal/service/tools"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"
)

// RAGService provides semantic search and context assembly for novels.
type RAGService struct {
	chapterRepo *repository.ChapterRepo
	chatModel   einomodel.ToolCallingChatModel
	embedder    embedding.Embedder
	searchSvc   *Service
	entitySvc   *entity.Service
	graphReader *graph.GraphReader
}

func NewRAGService(chapterRepo *repository.ChapterRepo, chatModel einomodel.ToolCallingChatModel, embedder embedding.Embedder, searchSvc *Service, entitySvc *entity.Service, graphReader *graph.GraphReader) *RAGService {
	return &RAGService{
		chapterRepo: chapterRepo,
		chatModel:   chatModel,
		embedder:    embedder,
		searchSvc:   searchSvc,
		entitySvc:   entitySvc,
		graphReader: graphReader,
	}
}

// SearchResult holds a retrieved chapter with its similarity score and matched content.
type SearchResult struct {
	Chapter        model.Chapter
	Score          float64
	MatchedContent string // chunk content that matched the query (empty if chapter-level match)
}

// AgenticResult holds the complete output of an Agentic RAG retrieval.
type AgenticResult struct {
	Context  string
	Verified bool
}

// Search performs semantic similarity search using chunk-level embeddings.
// Falls back to chapter-level search, then full-text if embeddings are unavailable.
// maxChapter enforces the spoiler-free boundary.
func (s *RAGService) Search(ctx context.Context, query string, novelID int64, maxChapter int, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	vecs, err := s.embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed query: empty result")
	}
	// Convert float64 → float32 for pgvector
	queryVec := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		queryVec[i] = float32(v)
	}

	vec := pgvector.NewVector(queryVec)

	// Phase 3: chunk-level search with matched content for better LLM context
	chapters, matchedContents, scores, err := s.chapterRepo.SearchChunksWithContent(novelID, maxChapter, vec, topK)
	if err != nil {
		return nil, fmt.Errorf("chunk search: %w", err)
	}

	// If no chunks have embeddings yet, fall back to full-text
	if len(chapters) == 0 {
		tsQuery := strings.ReplaceAll(query, " ", " | ")
		ftResults, ftErr := s.chapterRepo.FullTextSearch(novelID, maxChapter, tsQuery, topK)
		if ftErr != nil {
			return nil, fmt.Errorf("search (also fulltext fallback failed): %w", ftErr)
		}
		for _, r := range ftResults {
			chapters = append(chapters, r.Chapter)
			matchedContents = append(matchedContents, "") // full-text has no chunk content
			scores = append(scores, r.FinalScore)
		}
	}

	var results []SearchResult
	for i, ch := range chapters {
		results = append(results, SearchResult{
			Chapter:        ch,
			Score:          scores[i],
			MatchedContent: matchedContents[i],
		})
	}
	return results, nil
}

// BuildContext assembles a prompt context from search results.
func (s *RAGService) BuildContext(novelTitle string, currentChapter int, results []SearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("小说《%s》\n", novelTitle))
	sb.WriteString(fmt.Sprintf("用户阅读进度：第 %d 章\n\n", currentChapter))
	sb.WriteString("=== 相关章节摘要（按语义相似度检索） ===\n")

	allChars := make(map[string]model.CharacterInfo)
	allEvents := make([]model.EventInfo, 0)

	for _, r := range results {
		ch := r.Chapter

		// Show matched chunk content first (more specific than summary)
		if r.MatchedContent != "" {
			sb.WriteString(fmt.Sprintf("[第%d章 %.4f] 匹配片段：%s\n", ch.ChapterNumber, r.Score, r.MatchedContent))
		}

		if ch.Summary != "" {
			sb.WriteString(fmt.Sprintf("[第%d章 %.4f] 摘要：%s\n", ch.ChapterNumber, r.Score, ch.Summary))
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

const agenticTopK = 10

// AgenticRetrieve performs multi-step retrieval with an ADK agent that
// autonomously selects data sources (pgvector, Neo4j, full-text) and
// accumulates context. The agent's final message is the assembled context.
func (s *RAGService) AgenticRetrieve(ctx context.Context, query string, novelID int64, maxChapter int, novelTitle string) (*AgenticResult, error) {
	agent, err := newAgenticRAGAgent(ctx, s.chatModel, tools.Deps{
		NovelID:       novelID,
		MaxChapter:    maxChapter,
		SearchFunc:    s.searchSvc.SearchTool(),
		TimelineFunc:  s.graphReader.TimelineTool(),
		RelationsFunc: s.graphReader.RelationsTool(),
		EntityFunc:    s.entitySvc.EntityTool(),
	})
	if err != nil {
		return nil, fmt.Errorf("create agentic rag agent: %w", err)
	}

	context, err := runAgenticRAG(ctx, agent, query, novelTitle, maxChapter)
	if err != nil {
		return nil, fmt.Errorf("run agentic rag: %w", err)
	}

	return &AgenticResult{
		Context:  context,
		Verified: true,
	}, nil
}

// convertSearchResults converts SearchResult to model.HybridSearchResult.
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
