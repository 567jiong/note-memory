package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"note-memory/internal/service/tools"
)

// agentInstruction is the system prompt for the Reading Memory ChatModelAgent.
const agentInstruction = `你是一个小说阅读记忆助手（Reading Memory Agent），帮助用户回忆长篇小说中的人物、剧情和关系。

## 你的能力
你可以使用以下工具获取信息：
- search_chapters: 搜索章节内容（适合查找剧情细节、事件经过、物品描述、对话内容）
- get_chapters: 按章节范围获取摘要和出场人物（适合"最近主角在做什么""最近发生了什么""第X到Y章讲了什么"）
- query_timeline: 查询人物境界突破时间线（适合"什么境界""突破""修为"类问题）
- query_techniques: 查询人物的功法/秘术习得时间线（适合"XXX修炼了什么功法""无名口诀""XXX的功法有哪些"类问题）
- query_all_techniques: 查询当前已知所有功法秘术（适合"这本书有哪些厉害功法""所有剑诀"类问题）
- query_relations: 查询人物关系网（适合"师徒""仇敌""道侣""宗门"类问题）
- resolve_entity: 通过别名/称号/特征描述查找人物规范名（用户提"韩跑跑"时先调此工具找到"韩立"）

## 工具选择指南
- "什么境界""修为""突破" → query_timeline（查境界突破）
- "功法""秘术""口诀""剑诀""修炼了什么" → query_techniques 或 query_all_techniques（查功法技能）
- "关系""师徒""仇敌""道侣""认识谁" → query_relations（查人际关系）
- "发生了什么""最近""第X章" → get_chapters（查章节摘要）
- "XXX是谁""韩跑跑是谁" → resolve_entity（先解析实体名）
- 具体剧情细节、物品、对话 → search_chapters（全文搜索）

注意严格区分"功法秘术"和"修炼境界"——功法用 query_techniques，境界用 query_timeline。

## 工作流程
1. 分析用户问题，判断需要哪些信息
2. 如果用户使用的称呼不确定（别名、绰号、描述性称呼），**必须**先调用 resolve_entity 获取规范角色名
3. 当用户询问"最近""最新""这段时间"的剧情时，优先使用 get_chapters（传 n 参数）
4. 当用户询问特定章节范围时，使用 get_chapters（传 start/end 参数）
5. 根据问题类型选择工具（可能需要多次调用）
6. 整合所有工具返回的结果，用简洁中文生成回答

## 严格规则
- 所有工具返回的信息来自用户当前阅读进度之前的章节，绝不引用未读到的内容
- 如果工具返回的信息不足以回答问题，如实告知用户"根据当前阅读进度，这个信息尚未揭示"
- 回答简洁、准确，不要编造信息
- 使用人物的规范名称（而非别名）来回答`

// readingAgentConfig holds dependencies for the Reading Memory agent.
type readingAgentConfig struct {
	ChatModel einomodel.ToolCallingChatModel
	ToolDeps  tools.Deps
}

// newReadingAgent creates a ChatModelAgent for the reading memory use case.
func newReadingAgent(ctx context.Context, cfg readingAgentConfig) (adk.Agent, error) {
	t, err := tools.Build(cfg.ToolDeps)
	if err != nil {
		return nil, fmt.Errorf("build tools: %w", err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ReadingMemoryAgent",
		Description: "小说阅读记忆助手，帮助用户回忆剧情、人物关系、境界历程",
		Instruction: agentInstruction,
		Model:       cfg.ChatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: t,
			},
		},
		MaxIterations: 8,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model agent: %w", err)
	}

	return agent, nil
}

// prettyJSON reformats a JSON string with indentation for logging readability.
// Returns the original string unchanged if it's not valid JSON.
func prettyJSON(raw string) string {
	if !json.Valid([]byte(raw)) {
		return raw
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return strings.TrimSpace(buf.String())
}

// runReadingAgent runs the agent with a user question and returns the final answer.
// It logs the full ReAct loop: LLM reasoning, tool calls with parameters, and tool results.
func runReadingAgent(ctx context.Context, agent adk.Agent, novelTitle string, maxChapter int, question string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})

	input := []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(
			"当前小说：《%s》。用户阅读进度：第 1~%d 章。绝对不能引用第 %d 章及以后的内容。",
			novelTitle, maxChapter, maxChapter+1,
		)),
		schema.UserMessage(question),
	}

	log.Println("[QA] ═══════════════════════════════════════════")
	log.Printf("[QA] 📝 用户问题: %s", question)
	log.Printf("[QA] 📖 小说: 《%s》 | 进度: 第 %d 章", novelTitle, maxChapter)
	log.Println("[QA] ═══════════════════════════════════════════")

	iter := runner.Run(ctx, input)
	var answer string
	stepNum := 0

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			log.Printf("[QA] ❌ Agent 运行错误: %v", event.Err)
			return "", fmt.Errorf("agent run error: %w", event.Err)
		}

		msg, _, err := adk.GetMessage(event)
		if err != nil {
			log.Printf("[QA] ⚠️ 获取消息失败: %v", err)
			continue
		}
		if msg == nil {
			continue
		}

		switch msg.Role {
		case schema.Assistant:
			// --- LLM output: could be thinking with tool calls, or final answer ---
			hasToolCalls := len(msg.ToolCalls) > 0
			hasContent := msg.Content != ""
			hasReasoning := msg.ReasoningContent != ""

			if hasReasoning {
				log.Printf("[QA] 🧠 [Step %d] LLM 思考过程:", stepNum)
				log.Println("[QA] ───────────────────────────────────────────")
				log.Printf("[QA] %s", strings.TrimSpace(msg.ReasoningContent))
				log.Println("[QA] ───────────────────────────────────────────")
			}

			if hasToolCalls {
				stepNum++
				log.Printf("[QA] 🔧 [Step %d] LLM 决定调用 %d 个工具:", stepNum, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					log.Printf("[QA]   工具 %d: %s", i+1, tc.Function.Name)
					log.Printf("[QA]   参数 %d: %s", i+1, prettyJSON(tc.Function.Arguments))
				}
				log.Println("[QA] ───────────────────────────────────────────")
			}

			// The final answer: assistant content without tool calls
			if hasContent {
				answer = msg.Content
				if !hasToolCalls {
					log.Printf("[QA] 💬 [Step %d] LLM 最终回答:", stepNum+1)
					log.Println("[QA] ───────────────────────────────────────────")
					log.Printf("[QA] %s", strings.TrimSpace(msg.Content))
					log.Println("[QA] ───────────────────────────────────────────")
				}
			}

		case schema.Tool:
			// --- Tool execution result ---
			toolName := event.Output.MessageOutput.ToolName
			log.Printf("[QA] ✅ 工具 [%s] 返回结果:", toolName)
			log.Println("[QA] ───────────────────────────────────────────")
			// Truncate very long outputs for readability
			result := msg.Content
			const maxResultLen = 600
			if len(result) > maxResultLen {
				log.Printf("[QA] %s...", result[:maxResultLen])
				log.Printf("[QA] ... (共 %d 字符，已截断)", len(result))
			} else {
				log.Printf("[QA] %s", result)
			}
			log.Println("[QA] ───────────────────────────────────────────")

		default:
			// Log any unexpected message type for debugging
			if msg.Content != "" {
				log.Printf("[QA] [%s] %s", msg.Role, msg.Content)
			}
		}
	}

	log.Println("[QA] ═══════════════════════════════════════════")

	if answer == "" {
		log.Println("[QA] ❌ Agent 未产生任何回答")
		return "", fmt.Errorf("agent produced no answer")
	}
	return answer, nil
}
