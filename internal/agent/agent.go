package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// AgentDeps holds all dependencies needed to create a Reading Memory Agent.
type AgentDeps struct {
	ChatModel einomodel.ToolCallingChatModel
	Tools     ToolDeps
}

// NewReadingAgent creates a ChatModelAgent for the reading memory use case.
// It is designed to be created per-request because ToolDeps carries request-level
// state (NovelID, MaxChapter).
func NewReadingAgent(ctx context.Context, deps AgentDeps) (adk.Agent, error) {
	tools, err := BuildTools(deps.Tools)
	if err != nil {
		return nil, fmt.Errorf("build tools: %w", err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ReadingMemoryAgent",
		Description: "小说阅读记忆助手，帮助用户回忆剧情、人物关系、境界历程",
		Instruction: AgentInstruction(),
		Model:       deps.ChatModel,
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

// RunAgent runs the agent with a user question and returns the final answer.
// This is a convenience wrapper around adk.Runner.
func RunAgent(ctx context.Context, agent adk.Agent, novelTitle string, maxChapter int, question string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: agent,
	})

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
