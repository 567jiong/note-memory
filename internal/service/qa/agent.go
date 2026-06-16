package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

## 你的工具
你有两类数据源：
1. **Neo4j 知识图谱**（结构化，能跨章节追踪变化）：
   - query_timeline: 查询人物境界突破时间线（每次突破的章节和年龄）
   - query_relations: 查询人物关系网（师徒/仇敌/道侣/宗门归属，含关系变化的时间区间和触发事件）
   - query_techniques: 查询人物的功法/秘术习得时间线（含层次突破记录）
   - query_all_techniques: 查询当前已知所有功法秘术及其修炼者
   - get_chapters: 按章节范围获取摘要和出场人物（适合"最近发生了什么""第X到Y章"）

2. **全文检索引擎**（pgvector + 分词搜索，适合搜具体文本）：
   - search_chapters: 搜索章节内容（关键词匹配 + 语义相似度）
   - resolve_entity: 通过别名/特征描述找人物规范名（实体向量匹配）

## 工具调用优先级（非常重要）

**优先使用 Neo4j 图谱工具**处理以下问题（跨章节、结构化变化、全局性）：
- 境界/修为 → query_timeline
- 功法/秘术/技能 → query_techniques 或 query_all_techniques
- 人际关系/势力归属 → query_relations
- 章节剧情概览 → get_chapters
- 物品归属/拥有者变化（如"XXX法宝之前是谁的""掌天瓶落到了谁手里"）→ 先用 query_relations 查相关人物的关系变化和触发事件，信息不足再用 get_chapters 查相关章节

**只在以下情况使用 search_chapters（具体文本、图谱覆盖不到）：**
- 具体对话内容、物品外观描述、细节描写
- 图谱工具返回的信息不足以回答问题
- 用户明确要求搜索某个关键词或短语

**resolve_entity 调用规则：**
- 用户使用了不确定的称呼（别名/绰号/描述）→ 必须先调用
- 用户直接用了规范名 → 不需要调用

## 工作流程
1. 分析用户问题：是结构化查询（图谱优先）还是文本搜索（全文检索）
2. 称呼不确定 → 先调用 resolve_entity
3. 优先选择 Neo4j 工具（能跨章节追溯变化），图谱信息不足时再用 search_chapters
4. 整合所有工具返回的结果，用简洁中文生成回答

## 严格规则
- 绝不引用用户阅读进度之后的章节内容
- 工具返回的信息不足以回答时，如实告知"根据当前阅读进度，这个信息尚未揭示"
- 不编造信息，回答简洁准确
- 使用人物的规范名称（而非别名）来回答

注意严格区分：
- 功法秘术（青元剑诀、无名口诀）≠ 修炼境界（筑基期、元婴期）→ 功法用 query_techniques，境界用 query_timeline
- 物品归属变化属于全局性问题 → 图谱优先，不要直接用 search_chapters`

// readingAgentConfig holds dependencies for the Reading Memory agent.
type readingAgentConfig struct {
	ChatModel einomodel.ToolCallingChatModel
	ToolDeps  tools.Deps
}

// newReadingAgent creates a ChatModelAgent (streaming-capable) for the reading memory use case.
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

// StreamEvent represents a single SSE event during streaming agent execution.
type StreamEvent struct {
	Type    string `json:"type"`    // "thinking", "delta", "tool_call", "tool_result", "done", "error"
	Content string `json:"content"` // text delta or full answer (for done/error)
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
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

// runReadingAgentStream runs the agent with streaming enabled, pushing text/thinking
// events through onEvent for SSE delivery. Tool calls and tool results are logged
// server-side but never sent to the frontend.
func runReadingAgentStream(ctx context.Context, agent adk.Agent, novelTitle string, maxChapter int, question string, onEvent func(StreamEvent)) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	input := []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(
			"当前小说：《%s》。用户阅读进度：第 1~%d 章。绝对不能引用第 %d 章及以后的内容。",
			novelTitle, maxChapter, maxChapter+1,
		)),
		schema.UserMessage(question),
	}

	log.Println("[QA-Stream] ═══════════════════════════════════════════")
	log.Printf("[QA-Stream] 📝 用户问题: %s", question)
	log.Printf("[QA-Stream] 📖 小说: 《%s》 | 进度: 第 %d 章", novelTitle, maxChapter)
	log.Println("[QA-Stream] ═══════════════════════════════════════════")

	iter := runner.Run(ctx, input)
	var fullAnswer string
	stepNum := 0

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			log.Printf("[QA-Stream] ❌ Agent 运行错误: %v", event.Err)
			onEvent(StreamEvent{Type: "error", Content: event.Err.Error()})
			return "", fmt.Errorf("agent run error: %w", event.Err)
		}

		mo := event.Output.MessageOutput
		if mo == nil {
			continue
		}

		if mo.IsStreaming && mo.Role == schema.Assistant {
			// ── Streaming assistant turn: read token-level chunks directly ──
			sr := mo.MessageStream
			if sr == nil {
				continue
			}

			var sb strings.Builder
			// Merge tool call deltas by index — they arrive incrementally across chunks.
			tcMap := make(map[int]*schema.ToolCall)
			var maxIdx int = -1

			for {
				chunk, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					log.Printf("[QA-Stream] ⚠️ 读取流块失败: %v", err)
					break
				}

				// Emit reasoning as thinking event (frontend)
				if chunk.ReasoningContent != "" {
					onEvent(StreamEvent{Type: "thinking", Content: chunk.ReasoningContent})
					log.Printf("[QA-Stream] 🧠 [Step %d] LLM 思考: %s", stepNum, strings.TrimSpace(chunk.ReasoningContent))
				}

				// Merge tool call deltas by Index — tool calls arrive incrementally
				// across multiple chunks. The Index field identifies which tool call
				// each delta belongs to.
				for _, tc := range chunk.ToolCalls {
					var idx int
					if tc.Index != nil {
						idx = *tc.Index
					}
					if idx > maxIdx {
						maxIdx = idx
					}
					if existing, ok := tcMap[idx]; ok {
						// Merge delta fields into existing tool call
						if tc.ID != "" {
							existing.ID = tc.ID
						}
						if tc.Type != "" {
							existing.Type = tc.Type
						}
						if tc.Function.Name != "" {
							existing.Function.Name = tc.Function.Name
						}
						existing.Function.Arguments += tc.Function.Arguments
					} else {
						cp := tc // copy to avoid aliasing the loop variable
						tcMap[idx] = &cp
					}
				}

				// Emit content delta immediately to frontend
				if chunk.Content != "" {
					sb.WriteString(chunk.Content)
					onEvent(StreamEvent{Type: "delta", Content: chunk.Content})
				}
			}

			// After stream ends: log tool calls to server only, NOT to frontend.
			if len(tcMap) > 0 {
				stepNum++
				log.Printf("[QA-Stream] 🔧 [Step %d] LLM 调用 %d 个工具:", stepNum, len(tcMap))
				for i := 0; i <= maxIdx; i++ {
					if tc, ok := tcMap[i]; ok {
						log.Printf("[QA-Stream]   工具 %d: %s", i+1, tc.Function.Name)
						log.Printf("[QA-Stream]   参数 %d: %s", i+1, prettyJSON(tc.Function.Arguments))
					}
				}
				log.Println("[QA-Stream] ───────────────────────────────────────────")
				// Text before tool calls is intermediate reasoning; not appended to fullAnswer.
			} else {
				// No tool calls in this turn → accumulate as final answer text.
				fullAnswer += sb.String()
			}

		} else if !mo.IsStreaming {
			// ── Non-streaming event: tool result or fallback assistant message ──
			msg, _, err := adk.GetMessage(event)
			if err != nil {
				log.Printf("[QA-Stream] ⚠️ 获取消息失败: %v", err)
				continue
			}
			if msg == nil {
				continue
			}

			switch msg.Role {
			case schema.Assistant:
				// Fallback: non-streaming assistant message (some models may not stream).
				if len(msg.ToolCalls) > 0 {
					stepNum++
					log.Printf("[QA-Stream] 🔧 [Step %d] LLM 调用 %d 个工具:", stepNum, len(msg.ToolCalls))
					for _, tc := range msg.ToolCalls {
						log.Printf("[QA-Stream]   工具: %s | 参数: %s", tc.Function.Name, prettyJSON(tc.Function.Arguments))
					}
					log.Println("[QA-Stream] ───────────────────────────────────────────")
					// Tool call info NOT sent to frontend.
				}
				if msg.Content != "" {
					onEvent(StreamEvent{Type: "delta", Content: msg.Content})
					// Only accumulate as answer if this turn has no tool calls.
					if len(msg.ToolCalls) == 0 {
						fullAnswer += msg.Content
					}
				}

			case schema.Tool:
				// Tool execution result: server log only, NOT sent to frontend.
				toolName := mo.ToolName
				result := msg.Content
				log.Printf("[QA-Stream] ✅ 工具 [%s] 返回结果:", toolName)
				log.Println("[QA-Stream] ───────────────────────────────────────────")
				const maxResultLen = 600
				if len(result) > maxResultLen {
					log.Printf("[QA-Stream] %s...", result[:maxResultLen])
					log.Printf("[QA-Stream] ... (共 %d 字符，已截断)", len(result))
				} else {
					log.Printf("[QA-Stream] %s", result)
				}
				log.Println("[QA-Stream] ───────────────────────────────────────────")

			default:
				if msg.Content != "" {
					log.Printf("[QA-Stream] [%s] %s", msg.Role, msg.Content)
				}
			}
		}
	}

	log.Println("[QA-Stream] ═══════════════════════════════════════════")

	if fullAnswer == "" {
		log.Println("[QA-Stream] ❌ Agent 未产生任何回答")
		onEvent(StreamEvent{Type: "error", Content: "agent produced no answer"})
		return "", fmt.Errorf("agent produced no answer")
	}

	// Send completion event with full answer
	onEvent(StreamEvent{Type: "done", Content: fullAnswer})
	return fullAnswer, nil
}
