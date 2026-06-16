package chapter

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// summarizerInstruction is the system prompt for the Chapter Summarizer ChatModelAgent.
const summarizerInstruction = `你是一个小说分析助手。请根据提供的章节内容完成以下任务：

1. 用 2-3 句话总结本章主要情节。

2. 提取本章出现的主要人物。只提取有明确姓名或固定称呼的角色，不要提取"黄脸修士""中年儒生""师兄"之类的外貌描述或泛称角色。以 JSON 数组格式返回。每个人物包含以下字段：
   - name: 人物名
   - aliases: 别名数组
   - type: 角色类型，从以下选择：主角（故事核心人物，有大量视角和成长线）、重要配角（与主角频繁互动，有独立剧情线）、配角（有名字和一定戏份但不推动主线）、反派（与主角对立的重要角色）、路人（仅短暂出场无后续影响力）
   - status: 本章中的状态或变化
   - realm: 当前修炼境界名称（如"筑基期""元婴期"，根据文中描述推断。功法技能如"无名口诀"不要放入此字段，没有则为空字符串）
   - first_appearance: 章节号
   格式：[{"name":"人物名","aliases":["别名"],"type":"角色类型","status":"状态","realm":"境界名","first_appearance":章节号}]

3. 提取本章的关键事件，以 JSON 数组格式返回：
   [{"title":"事件名","participants":["人物名"],"summary":"事件简述","impact":"影响","chapter_num":章节号}]

4. 提取本章涉及的人物关系变化。只提取本章内新建立或发生显著变化的关系。以 JSON 数组格式返回：
   [{"from_name":"人物A","to_name":"人物B","relation_type":"关系类型","description":"关系简述","trigger_event":"触发事件名"}]
   relation_type 从以下选择：
   - MASTER_OF（师徒，from是师父to是徒弟）
   - FRIEND_OF（朋友/盟友）
   - ENEMY_OF（仇敌/对手）
   - LOVE_INTEREST（道侣/爱慕对象）
   - BELONGS_TO（宗门/势力归属，from是人物to是势力名）
   如果关系在本章结束/断裂，请在 description 中注明"关系断裂"。

5. 提取本章涉及的功法/秘术/技能信息（不是修炼境界！）。以 JSON 数组格式返回：
   [{"name":"功法名","level":"当前层次","practitioner":"修炼/施展者","action":"习得/突破/施展","chapter_num":章节号,"description":"简述"}]
   注意严格区分"功法秘术"和"修炼境界"——"无名口诀""青元剑诀""大衍诀"是功法；"筑基期""金丹期""元婴期"是境界，境界不在此处提取。

请严格按照以下 XML 格式输出：
<summary>总结内容</summary>
<characters>人物JSON数组</characters>
<events>事件JSON数组</events>
<relations>关系JSON数组</relations>
<techniques>功法JSON数组</techniques>`

// newSummarizerAgent creates a ChatModelAgent for chapter summarization.
func newSummarizerAgent(ctx context.Context, model einomodel.ToolCallingChatModel) (adk.Agent, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ChapterSummarizer",
		Description: "小说章节摘要生成器，分析章节内容提取摘要、人物和事件",
		Instruction: summarizerInstruction,
		Model:       model,
	})
	if err != nil {
		return nil, fmt.Errorf("create summarizer agent: %w", err)
	}
	return agent, nil
}

// runSummarizer runs the summarizer agent on a chapter and returns the AI response.
func runSummarizer(ctx context.Context, agent adk.Agent, title, content string) (string, error) {
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
