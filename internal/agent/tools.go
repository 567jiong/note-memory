package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolDeps provides request-scoped dependencies via function closures.
// Using functions instead of concrete types avoids import cycles between agent ↔ service.
type ToolDeps struct {
	NovelID    int64
	MaxChapter int

	// SearchFunc performs hybrid search on chapters.
	SearchFunc func(ctx context.Context, query string, novelID int64, maxChapter int, topK int) (string, error)

	// TimelineFunc queries realm breakthrough timeline.
	TimelineFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)

	// RelationsFunc queries character relationships.
	RelationsFunc func(ctx context.Context, novelID int64, charName string, maxChapter int) (string, error)

	// EntityFunc resolves entity aliases/descriptions to canonical names.
	EntityFunc func(ctx context.Context, query string, novelID int64, topK int) (string, error)
}

// BuildTools creates the tool set for the Reading Memory Agent.
func BuildTools(deps ToolDeps) ([]tool.BaseTool, error) {
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

	return []tool.BaseTool{searchTool, resolveTool, timelineTool, relationsTool}, nil
}

// --- search_chapters ---

func newSearchChaptersTool(deps ToolDeps) (tool.InvokableTool, error) {
	return utils.InferTool(
		"search_chapters",
		"搜索小说章节内容。传入中文关键词，返回相关章节的摘要和匹配文本片段。"+
			"适合查找：剧情细节、事件经过、物品描述、对话内容。不适合：人物关系、境界查询。",
		func(ctx context.Context, input *SearchChaptersInput) (string, error) {
			if deps.SearchFunc == nil {
				return `[]`, nil
			}
			return deps.SearchFunc(ctx, input.Query, deps.NovelID, deps.MaxChapter, 5)
		},
	)
}

// --- resolve_entity ---

func newResolveEntityTool(deps ToolDeps) (tool.InvokableTool, error) {
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

func newQueryTimelineTool(deps ToolDeps) (tool.InvokableTool, error) {
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

// --- query_relations ---

func newQueryRelationsTool(deps ToolDeps) (tool.InvokableTool, error) {
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
