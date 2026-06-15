package summarizer

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// instruction is the system prompt for the Chapter Summarizer ChatModelAgent.
// Private — this defines the agent's identity at creation time.
const instruction = `你是一个小说分析助手。请根据提供的章节内容完成以下任务：

1. 用 2-3 句话总结本章主要情节。
2. 提取本章出现的主要人物。只提取有明确姓名或固定称呼的角色，不要提取"黄脸修士""中年儒生""师兄"之类的外貌描述或泛称角色。以 JSON 数组格式返回。每个人物包含以下字段：
   - name: 人物名
   - aliases: 别名数组
   - status: 本章中的状态或变化
   - realm: 当前修炼境界名称（如"筑基期""元婴期"，根据文中描述推断，没有则为空字符串）
   - first_appearance: 章节号
   格式：[{"name":"人物名","aliases":["别名"],"status":"状态","realm":"境界名","first_appearance":章节号}]
3. 提取本章的关键事件，以 JSON 数组格式返回：
   [{"title":"事件名","participants":["人物名"],"summary":"事件简述","impact":"影响","chapter_num":章节号}]

请严格按照以下 XML 格式输出：
<summary>总结内容</summary>
<characters>人物JSON数组</characters>
<events>事件JSON数组</events>`

// Config holds dependencies for creating a Chapter Summarizer agent.
type Config struct {
	ChatModel einomodel.ToolCallingChatModel
}

// New creates a ChatModelAgent for chapter summarization.
// The agent's instruction is baked in — callers only inject dependencies.
func New(ctx context.Context, cfg Config) (adk.Agent, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ChapterSummarizer",
		Description: "小说章节摘要生成器，分析章节内容提取摘要、人物和事件",
		Instruction: instruction,
		Model:       cfg.ChatModel,
	})
	if err != nil {
		return nil, fmt.Errorf("create summarizer agent: %w", err)
	}

	return agent, nil
}

// Run runs the summarizer agent on a chapter and returns the AI response.
// The caller is responsible for parsing the XML output.
func Run(ctx context.Context, agent adk.Agent, title, content string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	userPrompt := fmt.Sprintf("章节标题：%s\n\n章节内容：\n%s", title, content)

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
			return "", fmt.Errorf("summarizer run error: %w", event.Err)
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
		return "", fmt.Errorf("summarizer produced no answer")
	}
	return answer, nil
}
