package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Deps provides request-scoped dependencies for the retrieval tools.
// Each Func can be nil — the corresponding tool will return empty results
// instead of erroring, so an agent can be created with a subset of tools.
type Deps struct {
	NovelID    int64
	MaxChapter int

	// SearchFunc performs hybrid search on chapters (pgvector + full-text).
	SearchFunc func(ctx context.Context, query string, novelID int64, maxChapter int, topK int) (string, error)

	// TimelineFunc queries realm breakthrough timeline from Neo4j.
	TimelineFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)

	// RelationsFunc queries character relationship graph from Neo4j.
	RelationsFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)

	// EntityFunc resolves entity aliases/descriptions to canonical names via vector search.
	EntityFunc func(ctx context.Context, query string, novelID int64, topK int) (string, error)

	// ChaptersFunc returns chapter summaries for a given range (start to end).
	// start=0 and end=0 triggers "recent N" mode using the caller's N parameter.
	ChaptersFunc func(ctx context.Context, novelID int64, start, end, maxChapter int) (string, error)

	// TechniqueFunc queries a character's technique acquisition timeline from Neo4j.
	TechniqueFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)

	// AllTechniquesFunc queries all known techniques from Neo4j.
	AllTechniquesFunc func(ctx context.Context, novelID int64, maxChapter int) (string, error)

	// EventsFunc queries events a character participated in from Neo4j (max 20, newest first).
	EventsFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)
}

// ── Timeout configuration (per-tool) ──────────────────────────────────────────

var toolTimeouts = map[string]time.Duration{
	"search_chapters":      15 * time.Second, // embedding API + pgvector + full-text + optional reranker
	"resolve_entity":       10 * time.Second, // embedding API
	"query_timeline":        5 * time.Second, // Neo4j — simple traversal
	"query_relations":       8 * time.Second, // Neo4j — graph traversal with OPTIONAL MATCH
	"get_chapters":          3 * time.Second, // pure PostgreSQL
	"query_techniques":      8 * time.Second, // Neo4j — OPTIONAL MATCH
	"query_all_techniques":  8 * time.Second, // Neo4j — larger result set
	"query_events":          5 * time.Second, // Neo4j — LIMIT 20, newest first
}

// withToolTimeout derives a context with the per-tool deadline. Falls back to 10 s.
func withToolTimeout(ctx context.Context, toolName string) (context.Context, context.CancelFunc) {
	d, ok := toolTimeouts[toolName]
	if !ok {
		d = 10 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// ── Error formatting (JSON → LLM can read & decide next step) ────────────────

// toolErrJSON builds a JSON error object that is returned as the tool result
// (NOT as a Go error). The LLM sees this JSON in the next ReAct turn and can
// decide to retry with different parameters, switch tools, or tell the user.
func toolErrJSON(toolName string, err error, suggestion string) string {
	msg := map[string]string{
		"error": fmt.Sprintf("[%s] %v", toolName, err),
		"tool":  toolName,
	}
	if suggestion != "" {
		msg["suggestion"] = suggestion
	}
	b, _ := json.Marshal(msg)
	return string(b)
}

// timeoutErrJSON returns a JSON error specifically for context deadline exceeded.
func timeoutErrJSON(toolName string) string {
	return toolErrJSON(toolName, fmt.Errorf("操作超时"), "请稍后重试，或尝试缩小查询范围、使用其他工具")
}

// validateNotEmpty checks that a required string field is non-empty.
// Returns an error message string; empty string means valid.
func validateNotEmpty(value, fieldName, toolName string) (jsonErr string) {
	if strings.TrimSpace(value) == "" {
		return toolErrJSON(toolName,
			fmt.Errorf("缺少必填参数 %s", fieldName),
			fmt.Sprintf("请提供有效的 %s 后重试", fieldName))
	}
	return ""
}

// ── Tool set construction ─────────────────────────────────────────────────────

// Build creates the full tool set for a Retrieval / Reading Memory agent.
// Returns eight tools: search_chapters, resolve_entity, query_timeline, query_relations,
// get_chapters, query_techniques, query_all_techniques, query_events.
func Build(deps Deps) ([]tool.BaseTool, error) {
	searchTool, err := newSearchChaptersTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create search_chapters tool: %w", err)
	}

	resolveTool, err := newResolveEntityTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create resolve_entity tool: %w", err)
	}

	timelineTool, err := newQueryTimelineTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create query_timeline tool: %w", err)
	}

	relationsTool, err := newQueryRelationsTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create query_relations tool: %w", err)
	}

	chaptersTool, err := newGetChaptersTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create get_chapters tool: %w", err)
	}

	techniquesTool, err := newQueryTechniquesTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create query_techniques tool: %w", err)
	}

	allTechniquesTool, err := newQueryAllTechniquesTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create query_all_techniques tool: %w", err)
	}

	eventsTool, err := newQueryEventsTool(deps)
	if err != nil {
		return nil, fmt.Errorf("create query_events tool: %w", err)
	}

	return []tool.BaseTool{searchTool, resolveTool, timelineTool, relationsTool, chaptersTool, techniquesTool, allTechniquesTool, eventsTool}, nil
}

// ── 1. search_chapters ────────────────────────────────────────────────────────

func newSearchChaptersTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"search_chapters",
		"搜索小说章节内容（RRF融合 + 交叉编码器精排）。传入中文关键词或自然语言问题，返回相关章节的摘要(summary)、匹配文本片段(content)和相关性得分(score)。"+
			"score 字段含义：>0.7 高相关，0.3-0.7 中等，<0.3 低可信度。"+
			"content 字段包含匹配到的具体原文片段，优先使用。"+
			"适合查找：剧情细节、事件经过、对话内容。",
		func(ctx context.Context, input *SearchChaptersInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.Query, "query", "search_chapters"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用 → 空结果
			if deps.SearchFunc == nil {
				return `[]`, nil
			}

			// 3. 加超时
			ctx, cancel := withToolTimeout(ctx, "search_chapters")
			defer cancel()

			// 4. 执行 + 错误 → JSON 给 LLM 决策
			result, err := deps.SearchFunc(ctx, input.Query, deps.NovelID, deps.MaxChapter, 10)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("search_chapters"), nil
				}
				return toolErrJSON("search_chapters", err,
					"请尝试调整搜索关键词、缩小查询范围，或改用 get_chapters 工具"), nil
			}
			return result, nil
		},
	)
}

// ── 2. resolve_entity ─────────────────────────────────────────────────────────

func newResolveEntityTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"resolve_entity",
		"通过别名、称号或特征描述查找人物的规范名称。"+
			"当用户使用的称呼不是规范名时（如'韩跑跑''那个拿掌天瓶的'），必须先调用此工具获取规范名，再用规范名查询关系或时间线。",
		func(ctx context.Context, input *ResolveEntityInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.Description, "description", "resolve_entity"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用
			if deps.EntityFunc == nil {
				return `{"matched_names":[]}`, nil
			}

			// 3. 超时
			ctx, cancel := withToolTimeout(ctx, "resolve_entity")
			defer cancel()

			// 4. 执行
			result, err := deps.EntityFunc(ctx, input.Description, deps.NovelID, 3)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("resolve_entity"), nil
				}
				return toolErrJSON("resolve_entity", err,
					"请尝试用更具体的特征描述，或直接用已知角色名查询其他工具"), nil
			}
			return result, nil
		},
	)
}

// ── 3. query_timeline ─────────────────────────────────────────────────────────

func newQueryTimelineTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_timeline",
		"查询人物的修炼境界突破时间线。返回每次突破的章节号和年龄。"+
			"适合回答：'XXX现在什么境界''XXX什么时候突破的筑基期''XXX的修炼历程'。需要提供规范角色名。",
		func(ctx context.Context, input *QueryTimelineInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.CharacterName, "character_name", "query_timeline"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用
			if deps.TimelineFunc == nil {
				return `[]`, nil
			}

			// 3. 超时
			ctx, cancel := withToolTimeout(ctx, "query_timeline")
			defer cancel()

			// 4. 执行
			result, err := deps.TimelineFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("query_timeline"), nil
				}
				return toolErrJSON("query_timeline", err,
					"图谱查询失败，可尝试用 get_chapters 查看章节摘要来推断境界信息"), nil
			}
			return result, nil
		},
	)
}

// ── 4. query_relations ────────────────────────────────────────────────────────

func newQueryRelationsTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_relations",
		"查询人物的人际关系网。返回师徒、仇敌、道侣、朋友、宗门归属等关系。"+
			"适合回答：'XXX和YYY什么关系''XXX有哪些仇敌''XXX的师父是谁'。需要提供规范角色名。",
		func(ctx context.Context, input *QueryRelationsInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.CharacterName, "character_name", "query_relations"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用
			if deps.RelationsFunc == nil {
				return `[]`, nil
			}

			// 3. 超时
			ctx, cancel := withToolTimeout(ctx, "query_relations")
			defer cancel()

			// 4. 执行
			result, err := deps.RelationsFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("query_relations"), nil
				}
				return toolErrJSON("query_relations", err,
					"图谱查询失败，可尝试用 search_chapters 搜索角色名来推断关系"), nil
			}
			return result, nil
		},
	)
}

// ── 5. get_chapters ───────────────────────────────────────────────────────────

func newGetChaptersTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"get_chapters",
		"按章节范围获取摘要和出场人物。每章返回章节号、标题、摘要、出场人物列表和事件列表。"+
			"适合回答：'最近主角在做什么''最近发生了什么''第X到Y章讲了什么''第X章讲了什么'。"+
			"三种用法：1) 传 n 获取最近 N 章（如 n=5）；"+
			"2) 传 start+end 指定范围（如 start=100,end=110）；"+
			"3) 传 start 获取单章（如 start=50,end=50）。"+
			"n 默认 5，最大 5。范围不会超出用户阅读进度。",
		func(ctx context.Context, input *GetChaptersInput) (string, error) {
			// 依赖不可用
			if deps.ChaptersFunc == nil {
				return `[]`, nil
			}

			// 参数计算
			start, end := input.Start, input.End
			if start <= 0 && end <= 0 {
				n := input.N
				if n <= 0 {
					n = 5
				}
				if n > 5 {
					n = 5
				}
				start = 0
				end = n
			} else {
				if start <= 0 {
					start = 1
				}
				if end <= 0 {
					end = start
				}
				// 范围校验：不允许超大范围
				if end-start > 20 {
					return toolErrJSON("get_chapters",
						fmt.Errorf("章节范围过大 (start=%d, end=%d, 跨度 %d > 20)", start, end, end-start),
						"单次最多获取 20 章，请缩小范围后分次查询"), nil
				}
			}

			// 超时
			ctx, cancel := withToolTimeout(ctx, "get_chapters")
			defer cancel()

			// 执行
			result, err := deps.ChaptersFunc(ctx, deps.NovelID, start, end, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("get_chapters"), nil
				}
				return toolErrJSON("get_chapters", err,
					"数据库查询失败，请缩小章节范围后重试"), nil
			}
			return result, nil
		},
	)
}

// ── 6. query_techniques ───────────────────────────────────────────────────────

func newQueryTechniquesTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_techniques",
		"查询人物的功法/秘术习得时间线。返回每次习得或突破的章节号。"+
			"适合回答：'XXX修炼了什么功法''XXX的功法有哪些''无名口诀是什么'类问题。"+
			"注意：功法（如青元剑诀、无名口诀）不同于修炼境界（如筑基期、元婴期），境界查询请用 query_timeline。需要提供规范角色名。",
		func(ctx context.Context, input *QueryTechniquesInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.CharacterName, "character_name", "query_techniques"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用
			if deps.TechniqueFunc == nil {
				return `[]`, nil
			}

			// 3. 超时
			ctx, cancel := withToolTimeout(ctx, "query_techniques")
			defer cancel()

			// 4. 执行
			result, err := deps.TechniqueFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("query_techniques"), nil
				}
				return toolErrJSON("query_techniques", err,
					"图谱查询失败，可尝试用 search_chapters 搜索功法名"), nil
			}
			return result, nil
		},
	)
}

// ── 7. query_all_techniques ───────────────────────────────────────────────────

func newQueryAllTechniquesTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_all_techniques",
		"查询当前阅读进度之前所有已知的功法/秘术。返回每种功法的修炼者、层次和习得章节。"+
			"适合回答：'这本书里有哪些厉害功法''所有剑诀有哪些''有人修炼了什么秘术'类问题。"+
			"不需要参数。",
		func(ctx context.Context, _ *QueryAllTechniquesInput) (string, error) {
			// 依赖不可用
			if deps.AllTechniquesFunc == nil {
				return `[]`, nil
			}

			// 超时
			ctx, cancel := withToolTimeout(ctx, "query_all_techniques")
			defer cancel()

			// 执行
			result, err := deps.AllTechniquesFunc(ctx, deps.NovelID, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("query_all_techniques"), nil
				}
				return toolErrJSON("query_all_techniques", err,
					"图谱查询失败，可尝试用 search_chapters 搜索功法相关关键词"), nil
			}
			return result, nil
		},
	)
}

// ── 8. query_events ───────────────────────────────────────────────────────────

func newQueryEventsTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_events",
		"查询某个人物参与的事件。需要提供规范角色名，返回最近 20 个事件（按章节倒序）。"+
			"每条记录包含事件标题、章节号、摘要和参与角色。"+
			"适合回答：'XXX经历了哪些大事''XXX在什么时候做了什么'。",
		func(ctx context.Context, input *QueryEventsInput) (string, error) {
			// 1. 参数校验
			if errJSON := validateNotEmpty(input.CharacterName, "character_name", "query_events"); errJSON != "" {
				return errJSON, nil
			}

			// 2. 依赖不可用
			if deps.EventsFunc == nil {
				return `[]`, nil
			}

			// 3. 超时
			ctx, cancel := withToolTimeout(ctx, "query_events")
			defer cancel()

			// 4. 执行
			result, err := deps.EventsFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return timeoutErrJSON("query_events"), nil
				}
				return toolErrJSON("query_events", err,
					"图谱查询失败，可尝试用 get_chapters 或 search_chapters 替代"), nil
			}
			return result, nil
		},
	)
}
