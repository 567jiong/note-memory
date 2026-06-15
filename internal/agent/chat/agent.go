package chat

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// instruction is the system prompt for the Reading Memory ChatModelAgent.
// Private — this defines the agent's identity at creation time.
const instruction = `你是一个小说阅读记忆助手（Reading Memory Agent），帮助用户回忆长篇小说中的人物、剧情和关系。

## 你的能力
你可以使用以下工具获取信息：
- search_chapters: 搜索章节内容（适合查找剧情细节、事件经过、物品描述、对话内容）
- query_timeline: 查询人物境界突破时间线（适合"什么境界""突破""修为"类问题）
- query_relations: 查询人物关系网（适合"师徒""仇敌""道侣""宗门"类问题）
- resolve_entity: 通过别名/称号/特征描述查找人物规范名（用户提"韩跑跑"时先调此工具找到"韩立"）

## 工作流程
1. 分析用户问题，判断需要哪些信息
2. 如果用户使用的称呼不确定（别名、绰号、描述性称呼），**必须**先调用 resolve_entity 获取规范角色名
3. 根据问题类型选择工具（可能需要多次调用）
4. 整合所有工具返回的结果，用简洁中文生成回答

## 严格规则
- 所有工具返回的信息来自用户当前阅读进度之前的章节，绝不引用未读到的内容
- 如果工具返回的信息不足以回答问题，如实告知用户"根据当前阅读进度，这个信息尚未揭示"
- 回答简洁、准确，不要编造信息
- 使用人物的规范名称（而非别名）来回答`

// Config holds dependencies for creating a Reading Memory agent.
type Config struct {
	ChatModel einomodel.ToolCallingChatModel
	Tools     ToolDeps
}

// New creates a ChatModelAgent for the reading memory use case.
// The agent's instruction is baked in — callers only inject dependencies.
func New(ctx context.Context, cfg Config) (adk.Agent, error) {
	tools, err := BuildTools(cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build tools: %w", err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ReadingMemoryAgent",
		Description: "小说阅读记忆助手，帮助用户回忆剧情、人物关系、境界历程",
		Instruction: instruction,
		Model:       cfg.ChatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
		MaxIterations: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model agent: %w", err)
	}

	return agent, nil
}

// Run runs the agent with a user question and returns the final answer.
func Run(ctx context.Context, agent adk.Agent, novelTitle string, maxChapter int, question string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	input := []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(
			"当前小说：《%s》。用户阅读进度：第 1~%d 章。绝对不能引用第 %d 章及以后的内容。",
			novelTitle, maxChapter, maxChapter+1,
		)),
		schema.UserMessage(question),
	}

	iter := runner.Run(ctx, input)
	var answer string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return "", fmt.Errorf("agent run error: %w", event.Err)
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
		return "", fmt.Errorf("agent produced no answer")
	}
	return answer, nil
}
