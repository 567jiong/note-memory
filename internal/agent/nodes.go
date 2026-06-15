package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"note-memory/internal/graph"
	"note-memory/internal/model"
	"note-memory/internal/service"
	"sort"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// GraphDeps holds all external dependencies needed by agent graph nodes.
type GraphDeps struct {
	SearchSvc   *service.SearchService
	RagSvc      *service.RAGService
	GraphReader *graph.GraphReader
	ChatModel   einomodel.ToolCallingChatModel
}

// classifyQuestion determines the query type based on simple keyword matching.
// Future: replace with LLM-based classification via a ChatModel node.
func classifyQuestion(question string) QueryClass {
	q := strings.ToLower(question)

	timelineKW := []string{"境界", "突破", "年龄", "多少岁", "时间线", "修炼历程", "升级", "什么修为", "什么境界", "什么时候"}
	relationKW := []string{"仇敌", "仇人", "敌人", "师徒", "师父", "徒弟", "道侣", "朋友", "宗门", "认识", "关系", "恩人", "联盟", "敌对"}

	timeline, relation := false, false
	for _, kw := range timelineKW {
		if strings.Contains(q, kw) {
			timeline = true
			break
		}
	}
	for _, kw := range relationKW {
		if strings.Contains(q, kw) {
			relation = true
			break
		}
	}

	if timeline && relation {
		return QueryMixed
	}
	if timeline {
		return QueryTimeline
	}
	if relation {
		return QueryRelation
	}
	return QueryFact
}

// ---- Router Node ----

// routerNode classifies the question and sets QueryClass on the state.
func (d *GraphDeps) routerNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	s.QueryClass = string(classifyQuestion(s.Question))
	return s, nil
}

// classifyBranch routes to the appropriate retrieval node based on query class.
func (d *GraphDeps) classifyBranch(ctx context.Context, s *ReadingAgentState) (string, error) {
	switch QueryClass(s.QueryClass) {
	case QueryTimeline, QueryRelation:
		return "graph", nil
	case QueryMixed:
		return "search", nil // mixed: search first, graph enriches later
	default:
		return "search", nil
	}
}

// ---- Search Node ----

// searchNode performs hybrid search and stores results in state.
func (d *GraphDeps) searchNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	results, err := d.SearchSvc.HybridSearch(ctx, s.SearchQuery, s.NovelID, s.MaxChapter, 10)
	if err != nil {
		// Fallback to semantic search
		sr, err2 := d.RagSvc.Search(ctx, s.SearchQuery, s.NovelID, s.MaxChapter, 10)
		if err2 != nil {
			s.Error = fmt.Sprintf("search failed: hybrid=%v, semantic=%v", err, err2)
			return s, nil
		}
		results = convertFromSearchResults(sr)
	}

	// Accumulate unique results across iterations
	seen := make(map[int64]bool)
	for _, r := range s.SearchResults {
		seen[r.Chapter.ID] = true
	}
	for _, r := range results {
		if seen[r.Chapter.ID] {
			continue
		}
		seen[r.Chapter.ID] = true
		s.SearchResults = append(s.SearchResults, r)
	}

	return s, nil
}

// ---- Graph Node ----

// graphNode enriches state with Neo4j knowledge graph data.
func (d *GraphDeps) graphNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	if d.GraphReader == nil || !d.GraphReader.IsEnabled() {
		return s, nil
	}

	graphCtx, _ := graph.RouteQuery(ctx, d.GraphReader, s.Question, s.NovelID, "主角", s.MaxChapter)
	s.GraphTimeline = graphCtx.RealmTimeline
	s.GraphRelations = graphCtx.Relations
	s.GraphStatus = graphCtx.StatusChanges

	return s, nil
}

// ---- Verify Node ----

// verifyNode asks the LLM to judge whether current search results are sufficient.
func (d *GraphDeps) verifyNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	s.Iteration++

	if len(s.SearchResults) == 0 {
		s.RetrievalOK = false
		s.MissingInfo = "no search results found"
		return s, nil
	}

	// Build summary of top 5 results for LLM verification
	var summaries strings.Builder
	for i, r := range s.SearchResults {
		if i >= 5 {
			break
		}
		if r.Chapter.Summary != "" {
			summaries.WriteString(fmt.Sprintf("[第%d章] %s\n", r.Chapter.ChapterNumber, r.Chapter.Summary))
		}
	}

	sysPrompt := `你是一个检索质量评估器。请判断检索到的章节摘要是否包含足够信息来回答用户问题。

请按以下 JSON 格式输出（不要输出其他内容）：
{
  "sufficient": true或false,
  "reasoning": "评估理由（一句话）",
  "missing": "如果不足，缺失什么信息（一句话；如果充足则为空）",
  "rewritten_query": "如果不足，改写后的查询词（中文关键词，用空格分隔；如果充足则为空）"
}

判断标准：
- "sufficient": true — 检索结果包含回答问题的关键信息
- "sufficient": false — 关键信息缺失，需要改写查询重新检索`

	userPrompt := fmt.Sprintf(`用户问题：%s
用户阅读进度：第 1 ~ %d 章

检索到的章节摘要：
%s
请评估这些结果是否足够回答用户问题。`, s.Question, s.MaxChapter, summaries.String())

	msg, err := d.ChatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(sysPrompt),
		schema.UserMessage(userPrompt),
	}, einomodel.WithTemperature(0.3), einomodel.WithMaxTokens(300))
	if err != nil {
		// LLM failed — accept current results
		s.RetrievalOK = true
		return s, nil
	}

	verdict := parseVerdict(msg.Content)
	s.RetrievalOK = verdict.Sufficient
	s.MissingInfo = verdict.Missing
	s.RewrittenQuery = verdict.RewrittenQuery

	return s, nil
}

// verifyBranch decides the next step after verification.
func (d *GraphDeps) verifyBranch(ctx context.Context, s *ReadingAgentState) (string, error) {
	if s.RetrievalOK || s.Iteration >= s.MaxIterations {
		return "generate", nil
	}
	return "rewrite", nil
}

// ---- Rewrite Node ----

// rewriteNode updates the search query for the next iteration (pure state mutation).
func (d *GraphDeps) rewriteNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	if s.RewrittenQuery != "" && s.RewrittenQuery != s.SearchQuery {
		s.SearchQuery = s.RewrittenQuery
	}
	return s, nil
}

// ---- Generate Node ----

// generateNode assembles context and calls the LLM to produce the final answer.
func (d *GraphDeps) generateNode(ctx context.Context, s *ReadingAgentState) (*ReadingAgentState, error) {
	// Build context from search results
	var contextResults []service.SearchResult
	var chapters []model.Chapter
	for _, r := range s.SearchResults {
		contextResults = append(contextResults, service.SearchResult{
			Chapter: r.Chapter,
			Score:   r.FinalScore,
		})
		chapters = append(chapters, r.Chapter)
	}

	// Sort by chapter number
	sort.Slice(contextResults, func(i, j int) bool {
		return contextResults[i].Chapter.ChapterNumber < contextResults[j].Chapter.ChapterNumber
	})

	context := d.RagSvc.BuildContext(s.NovelTitle, s.MaxChapter, contextResults)

	// Enrich with Neo4j graph data
	if s.GraphTimeline != "" {
		context += "\n" + s.GraphTimeline
	}
	if s.GraphRelations != "" {
		context += "\n" + s.GraphRelations
	}
	if s.GraphStatus != "" {
		context += "\n" + s.GraphStatus
	}

	s.FinalContext = context

	// Generate answer
	sysPrompt := fmt.Sprintf(`你是一个阅读助手，帮助用户回忆小说《%s》的剧情。

## 严格规则（极其重要）
- 你只能使用下面提供的上下文信息来回答问题
- 所有上下文信息都来自第 1~%d 章，绝不能引用第 %d 章及以后的内容
- 如果上下文中没有足够信息回答问题，请如实告知"根据当前的阅读进度，这个信息尚未揭示"，不要编造
- 回答要简洁、准确

## 上下文信息
%s`, s.NovelTitle, s.MaxChapter, s.MaxChapter+1, context)

	userPrompt := fmt.Sprintf("用户提问：%s\n\n请根据上下文回答。如果信息不足，请明确说明。", s.Question)

	msg, err := d.ChatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(sysPrompt),
		schema.UserMessage(userPrompt),
	}, einomodel.WithTemperature(0.5), einomodel.WithMaxTokens(1000))
	if err != nil {
		return nil, fmt.Errorf("generate answer: %w", err)
	}

	s.FinalAnswer = msg.Content
	return s, nil
}

// ---- Helpers ----

type verdictJSON struct {
	Sufficient     bool   `json:"sufficient"`
	Reasoning      string `json:"reasoning"`
	Missing        string `json:"missing"`
	RewrittenQuery string `json:"rewritten_query"`
}

func parseVerdict(raw string) verdictJSON {
	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	}

	var v verdictJSON
	if err := json.Unmarshal([]byte(cleaned), &v); err != nil {
		start := strings.Index(cleaned, "{")
		end := strings.LastIndex(cleaned, "}")
		if start >= 0 && end > start {
			json.Unmarshal([]byte(cleaned[start:end+1]), &v)
		}
	}
	return v
}

func convertFromSearchResults(sr []service.SearchResult) []model.HybridSearchResult {
	result := make([]model.HybridSearchResult, 0, len(sr))
	for _, r := range sr {
		result = append(result, model.HybridSearchResult{
			Chapter:    r.Chapter,
			FinalScore: r.Score,
		})
	}
	return result
}
