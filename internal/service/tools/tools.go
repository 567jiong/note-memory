package tools

import (
	"context"
	"fmt"

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

// --- search_chapters ---

func newSearchChaptersTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"search_chapters",
		"搜索小说章节内容（RRF融合 + 交叉编码器精排）。传入中文关键词或自然语言问题，返回相关章节的摘要(summary)、匹配文本片段(content)和相关性得分(score)。"+
			"score 字段含义：>0.7 高相关，0.3-0.7 中等，<0.3 低可信度。"+
			"content 字段包含匹配到的具体原文片段，优先使用。"+
			"适合查找：剧情细节、事件经过、对话内容。",
		func(ctx context.Context, input *SearchChaptersInput) (string, error) {
			if deps.SearchFunc == nil {
				return `[]`, nil
			}
			return deps.SearchFunc(ctx, input.Query, deps.NovelID, deps.MaxChapter, 10)
		},
	)
}

// --- resolve_entity ---

func newResolveEntityTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"resolve_entity",
		"通过别名、称号或特征描述查找人物的规范名称。"+
			"当用户使用的称呼不是规范名时（如'韩跑跑''那个拿掌天瓶的'），必须先调用此工具获取规范名，再用规范名查询关系或时间线。",
		func(ctx context.Context, input *ResolveEntityInput) (string, error) {
			if deps.EntityFunc == nil {
				return `{"matched_names":[]}`, nil
			}
			return deps.EntityFunc(ctx, input.Description, deps.NovelID, 3)
		},
	)
}

// --- query_timeline ---

func newQueryTimelineTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_timeline",
		"查询人物的修炼境界突破时间线。返回每次突破的章节号和年龄。"+
			"适合回答：'XXX现在什么境界''XXX什么时候突破的筑基期''XXX的修炼历程'。需要提供规范角色名。",
		func(ctx context.Context, input *QueryTimelineInput) (string, error) {
			if deps.TimelineFunc == nil {
				return `[]`, nil
			}
			return deps.TimelineFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
		},
	)
}

// --- get_chapters ---

func newGetChaptersTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"get_chapters",
		"按章节范围获取摘要和出场人物。每章返回章节号、标题、摘要、出场人物列表和事件列表。"+
			"适合回答：'最近主角在做什么''最近发生了什么''第X到Y章讲了什么''第X章讲了什么'。"+
			"三种用法：1) 传 n 获取最近 N 章（如 n=5）；"+
			"2) 传 start+end 指定范围（如 start=100,end=110）；"+
			"3) 传 start 获取单章（如 start=50,end=50）。"+
			"n 默认 5，最大 20。范围不会超出用户阅读进度。",
		func(ctx context.Context, input *GetChaptersInput) (string, error) {
			if deps.ChaptersFunc == nil {
				return `[]`, nil
			}
			start, end := input.Start, input.End
			if start <= 0 && end <= 0 {
				// "recent N" mode
				n := input.N
				if n <= 0 {
					n = 5
				}
				if n > 20 {
					n = 20
				}
				start = 0 // signal to the func to use "recent N"
				end = n
			} else {
				// range mode
				if start <= 0 {
					start = 1
				}
				if end <= 0 {
					end = start
				}
			}
			return deps.ChaptersFunc(ctx, deps.NovelID, start, end, deps.MaxChapter)
		},
	)
}

// --- query_relations ---

func newQueryRelationsTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_relations",
		"查询人物的人际关系网。返回师徒、仇敌、道侣、朋友、宗门归属等关系。"+
			"适合回答：'XXX和YYY什么关系''XXX有哪些仇敌''XXX的师父是谁'。需要提供规范角色名。",
		func(ctx context.Context, input *QueryRelationsInput) (string, error) {
			if deps.RelationsFunc == nil {
				return `[]`, nil
			}
			return deps.RelationsFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
		},
	)
}

// --- query_techniques ---

func newQueryTechniquesTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_techniques",
		"查询人物的功法/秘术习得时间线。返回每次习得或突破的章节号。"+
			"适合回答：'XXX修炼了什么功法''XXX的功法有哪些''无名口诀是什么'类问题。"+
			"注意：功法（如青元剑诀、无名口诀）不同于修炼境界（如筑基期、元婴期），境界查询请用 query_timeline。需要提供规范角色名。",
		func(ctx context.Context, input *QueryTechniquesInput) (string, error) {
			if deps.TechniqueFunc == nil {
				return `[]`, nil
			}
			return deps.TechniqueFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
		},
	)
}

// --- query_all_techniques ---

func newQueryAllTechniquesTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_all_techniques",
		"查询当前阅读进度之前所有已知的功法/秘术。返回每种功法的修炼者、层次和习得章节。"+
			"适合回答：'这本书里有哪些厉害功法''所有剑诀有哪些''有人修炼了什么秘术'类问题。"+
			"不需要参数。",
		func(ctx context.Context, _ *QueryAllTechniquesInput) (string, error) {
			if deps.AllTechniquesFunc == nil {
				return `[]`, nil
			}
			return deps.AllTechniquesFunc(ctx, deps.NovelID, deps.MaxChapter)
		},
	)
}

// --- query_events ---

func newQueryEventsTool(deps Deps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_events",
		"查询某个人物参与的事件。需要提供规范角色名，返回最近 20 个事件（按章节倒序）。"+
			"每条记录包含事件标题、章节号、摘要和参与角色。"+
			"适合回答：'XXX经历了哪些大事''XXX在什么时候做了什么'。",
		func(ctx context.Context, input *QueryEventsInput) (string, error) {
			if deps.EventsFunc == nil {
				return `[]`, nil
			}
			return deps.EventsFunc(ctx, deps.NovelID, input.CharacterName, deps.MaxChapter)
		},
	)
}
