package descriptor

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// instruction is the system prompt for the Entity Descriptor ChatModelAgent.
// Private — this defines the agent's identity at creation time.
const instruction = `你是一个小说实体描述生成器。根据提供的人物信息，生成一段富描述文本（100-300字）。

## 要求
- 自然流畅，适合用于语义搜索
- 包含：正式姓名、所有已知别名/马甲/称号、修炼境界历程、所属宗门/势力、
       持有的重要法宝/功法、关键人际关系、性格特征
- 不要编造信息，只使用提供的数据

## 输出格式
直接输出描述文本，不要JSON，不要XML标签，不要任何前缀。`

// Config holds dependencies for creating an Entity Descriptor agent.
type Config struct {
	ChatModel einomodel.ToolCallingChatModel
}

// New creates a ChatModelAgent for entity description generation.
// The agent's instruction is baked in — callers only inject dependencies.
func New(ctx context.Context, cfg Config) (adk.Agent, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "EntityDescriptor",
		Description: "小说实体描述生成器，根据人物信息生成富文本描述用于语义搜索",
		Instruction: instruction,
		Model:       cfg.ChatModel,
	})
	if err != nil {
		return nil, fmt.Errorf("create descriptor agent: %w", err)
	}

	return agent, nil
}

// Run runs the descriptor agent to generate a rich text description for a character.
// Returns a fallback description if the model returns empty.
func Run(ctx context.Context, agent adk.Agent, name string, aliases []string, status string, firstChapter int) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	userPrompt := fmt.Sprintf(`人物名称：%s
别名列表：%s
当前状态：%s
首次出场章节：%d`,
		name, strings.Join(aliases, "、"), status, firstChapter)

	iter := runner.Run(ctx, []*schema.Message{
		schema.UserMessage(userPrompt),
	})
	var answer string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return "", fmt.Errorf("descriptor run error: %w", event.Err)
		}
		msg, _, err := adk.GetMessage(event)
		if err != nil {
			continue
		}
		if msg != nil && msg.Role == schema.Assistant && msg.Content != "" {
			answer = msg.Content
		}
	}

	description := strings.TrimSpace(answer)
	if description == "" {
		// Fallback: minimal description from available fields
		description = fmt.Sprintf("%s，别名%s", name, strings.Join(aliases, "、"))
	}

	return description, nil
}
