package search

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"note-memory/internal/service/tools"
)

// agenticRAGInstruction is the system prompt for the Agentic RAG ChatModelAgent.
// Same tools as the Reading Memory agent, but the goal is context collection, not Q&A.
const agenticRAGInstruction = `你是一个检索代理。你的任务是找到足够的小说章节信息来满足检索需求。

## 可用工具
- search_chapters: 搜索章节内容（pgvector + 全文检索）
- resolve_entity: 通过别名/称号查找人物规范名
- query_timeline: 查询人物境界突破时间线（Neo4j 图数据库）
- query_relations: 查询人物关系网（Neo4j 图数据库）

## 工作流程
1. 分析检索需求，判断需要走哪个数据源
2. 如果涉及具体人物但称呼不确定，先调用 resolve_entity 获取规范名
3. 根据需求类型选择工具（可多次调用、组合使用）：
   - 剧情/事件细节 → search_chapters
   - 境界/修为相关 → query_timeline
   - 人物关系相关 → query_relations
4. 评估返回结果是否充分，如不充分则换个角度重试
5. 整理所有检索到的信息

## 严格规则
- 所有工具返回的信息来自用户阅读进度之前的章节
- 如果多次检索返回相同结果，说明信息不足，如实输出
- 不要编造信息，只使用工具返回的内容

## 输出格式
直接输出检索到的上下文信息，格式如下：

=== 相关章节摘要 ===
[第X章] 摘要内容...

=== 相关人物（从搜索结果中提取） ===
- 人物名（状态变化）

=== 相关事件 ===
- [第X章] 事件简述`

// newAgenticRAGAgent creates a ChatModelAgent for autonomous multi-step retrieval.
// Uses the same shared tool set (tools.Build) as the Reading Memory agent, so the LLM
// can freely choose between pgvector search, Neo4j graph traversal, and entity resolution.
func newAgenticRAGAgent(ctx context.Context, model einomodel.ToolCallingChatModel, deps tools.Deps) (adk.Agent, error) {
	t, err := tools.Build(deps)
	if err != nil {
		return nil, fmt.Errorf("build tools: %w", err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "AgenticRAG",
		Description: "检索代理，自主选择 pgvector / Neo4j 数据源收集上下文",
		Instruction: agenticRAGInstruction,
		Model:       model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: t,
			},
		},
		MaxIterations: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("create agentic rag agent: %w", err)
	}

	return agent, nil
}

// runAgenticRAG runs the Agentic RAG agent and returns the assembled context.
func runAgenticRAG(ctx context.Context, agent adk.Agent, query string, novelTitle string, maxChapter int) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	userMsg := fmt.Sprintf(
		"小说：《%s》\n用户阅读进度：第 1~%d 章\n\n检索需求：%s",
		novelTitle, maxChapter, query,
	)

	iter := runner.Run(ctx, []*schema.Message{
		schema.UserMessage(userMsg),
	})
	var answer string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return "", fmt.Errorf("agentic rag run error: %w", event.Err)
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil {
			continue
		}
		if msg != nil && msg.Role == schema.Assistant && msg.Content != "" {
			answer = msg.Content
		}
	}
	if answer == "" {
		return "", fmt.Errorf("agentic rag produced no answer")
	}
	return answer, nil
}
