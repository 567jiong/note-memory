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
你有两类数据源，优先使用 Neo4j 知识图谱，它能跨章节追溯结构化变化：
1. **Neo4j 知识图谱**（结构化，跨章节追溯）：
   - query_timeline: 查询人物境界突破时间线（每次突破的章节和年龄）
   - query_relations: 查询人物关系网（师徒/仇敌/道侣/宗门归属，含关系变化的时间区间和触发事件）
   - query_techniques: 查询人物的功法/秘术习得时间线（含层次突破记录）
   - query_all_techniques: 查询当前已知所有功法秘术及其修炼者
   - query_events: 查询人物参与的事件（需提供规范角色名，返回最近20个）
   - get_chapters: 按章节范围获取摘要和出场人物（适合"最近发生了什么""第X到Y章"）

2. **全文检索引擎**（RRF融合 + 交叉编码器精排，适合搜具体文本细节）：
   - search_chapters: 搜索章节内容（语义 + 关键词混合检索），返回匹配文本片段(content)、摘要和相关性得分(score)
   - resolve_entity: 通过别名/特征描述找人物规范名（实体向量匹配）

## 工具调用优先级（非常重要）

**优先使用 Neo4j 图谱工具**处理以下问题（跨章节、结构化变化、全局性）：
- 境界/修为 → query_timeline
- 功法/秘术/技能 → query_techniques 或 query_all_techniques
- 人际关系/势力归属 → query_relations
- 事件/剧情大事 → query_events（优先图谱，能跨章节追溯人物参与的事件）
- 章节剧情概览 → get_chapters
- 物品归属/拥有者变化 → 先用 query_relations 查相关人物的关系变化和触发事件，信息不足再用 get_chapters

**只在以下情况使用 search_chapters（具体文本、图谱覆盖不到）：**
- 具体对话内容、物品外观描述、细节描写
- 图谱工具返回的信息不足以回答问题
- 用户明确要求搜索某个关键词或短语

**resolve_entity 调用规则：**
- 用户使用了不确定的称呼（别名/绰号/描述）→ 必须先调用
- 用户直接用了规范名 → 不需要调用

## 步回检索策略（Step-back）
当问题涉及等级判定、体系分类、恩怨背景等需要"背景知识"才能准确回答的场景时，
不要只搜具体问题，而是：

1. 先思考：这个问题属于哪个更大的体系或背景？
2. 生成一个更抽象、更通用的"步回问题"
3. 优先用 Neo4j 图谱工具检索背景知识（体系、规则、分类）：
   - 等级/体系类步回 → query_all_techniques 或 query_events
   - 恩怨/关系类步回 → query_relations
   - 图谱覆盖不到的文本背景 → search_chapters
4. 用原始问题调用对应工具检索具体信息
5. 结合两轮检索结果回答

**示例：**
- 用户："佛怒火莲是什么级别的斗技"
  → 步回搜：query_all_techniques 了解全书斗技等级体系
  → 具体搜：query_techniques(萧炎) 找佛怒火莲的层次信息
- 用户："韩立在乱星海获得了什么法宝"
  → 步回搜：query_events(韩立) 查其经历的重大事件
  → 具体搜：search_chapters("韩立 乱星海 法宝")
- 用户："萧炎为什么恨云山"
  → 步回搜：query_relations(萧炎) 找与云岚宗相关的恩怨关系
  → 具体搜：query_events(萧炎) 查恩怨相关事件

**注意：** 简单问题（如具体章节查询、人物当前状态）不需要步回

## 检索结果评估
search_chapters 返回结果中的 score 字段表示相关性置信度（0-1）：
- score > 0.7：高相关，信息可直接引用
- score 0.3-0.7：中等相关，需结合其他工具交叉验证
- score < 0.3：低可信度，建议改用 get_chapters 或调整关键词重试
- 如果同一查询关键词连续3次 score 均低于 0.3，说明信息不足，直接告知用户
- content 字段包含匹配到的原文片段，优先使用其中信息

## 工作流程
1. 分析用户问题：是结构化查询（图谱优先）还是文本搜索（全文检索）
2. 称呼不确定 → 先调用 resolve_entity
3. 优先选择 Neo4j 工具（能跨章节追溯变化），图谱信息不足时再用 search_chapters
4. 如果问题需要背景知识，使用步回检索策略
5. 整合所有工具返回的结果，用简洁中文生成回答

## 严格规则
- 绝不引用用户阅读进度之后的章节内容
- 工具返回的信息不足以回答时，如实告知"根据当前阅读进度，这个信息尚未揭示"
- 不编造信息，回答简洁准确
- 使用人物的规范名称（而非别名）来回答

注意严格区分：
- 功法秘术（青元剑诀、无名口诀）≠ 修炼境界（筑基期、元婴期）→ 功法用 query_techniques，境界用 query_timeline
- 物品归属变化属于全局性问题 → 图谱优先，不要直接用 search_chapters
- 事件查询优先用 query_events（图谱），具体细节再用 search_chapters（全文检索）`

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
		MaxIterations: 6,
		// 模型调用失败时自动重试（指数退避：100ms→200ms→400ms，带 jitter）
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries: 2, // 初始请求 + 最多 2 次重试 = 共 3 次
		},
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

// AgentRecorder is an optional hook interface for recording agent execution details.
// Implementations can capture tool calls, thinking, and results for evaluation.
type AgentRecorder interface {
	OnThinking(step int, content string)
	OnToolCall(step int, toolName, args string)
	OnToolResult(toolName, result, toolErr string)
	OnFinalAnswer(answer string)
	OnError(err error)
}

// runReadingAgentStreamWithHistory is like runReadingAgentStream but accepts
// conversation history (without system messages) and collects all produced
// messages for memory storage.
func runReadingAgentStreamWithHistory(ctx context.Context, agent adk.Agent, novelTitle string, maxChapter int, history []*schema.Message, question string, onEvent func(StreamEvent), recorder AgentRecorder) (*ChatResult, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	// Build input: system message + history + new user question
	input := []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(
			"当前小说：《%s》。用户阅读进度：第 1~%d 章。绝对不能引用第 %d 章及以后的内容。",
			novelTitle, maxChapter, maxChapter+1,
		)),
	}
	input = append(input, history...)
	input = append(input, schema.UserMessage(question))

	// Track produced messages for storage (user question + all agent/tool messages)
	produced := []*schema.Message{schema.UserMessage(question)}

	log.Println("[QA-Stream-H] ═══════════════════════════════════════════")
	log.Printf("[QA-Stream-H] 📝 用户问题: %s", question)
	log.Printf("[QA-Stream-H] 📖 小说: 《%s》 | 历史: %d 条 | 进度: 第 %d 章", novelTitle, len(history), maxChapter)
	log.Println("[QA-Stream-H] ═══════════════════════════════════════════")

	iter := runner.Run(ctx, input)
	var fullAnswer string
	stepNum := 0

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			log.Printf("[QA-Stream-H] ❌ Agent 运行错误: %v", event.Err)
			onEvent(StreamEvent{Type: "error", Content: event.Err.Error()})
			if recorder != nil {
				recorder.OnError(event.Err)
			}
			return nil, fmt.Errorf("agent run error: %w", event.Err)
		}

		mo := event.Output.MessageOutput
		if mo == nil {
			continue
		}

		if mo.IsStreaming && mo.Role == schema.Assistant {
			sr := mo.MessageStream
			if sr == nil {
				continue
			}

			var sb strings.Builder
			tcMap := make(map[int]*schema.ToolCall)
			var maxIdx int = -1

			for {
				chunk, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					log.Printf("[QA-Stream-H] ⚠️ 读取流块失败: %v", err)
					break
				}

				if chunk.ReasoningContent != "" {
					onEvent(StreamEvent{Type: "thinking", Content: chunk.ReasoningContent})
					if recorder != nil {
						recorder.OnThinking(stepNum, chunk.ReasoningContent)
					}
				}

				for _, tc := range chunk.ToolCalls {
					var idx int
					if tc.Index != nil {
						idx = *tc.Index
					}
					if idx > maxIdx {
						maxIdx = idx
					}
					if existing, ok := tcMap[idx]; ok {
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
						cp := tc
						tcMap[idx] = &cp
					}
				}

				if chunk.Content != "" {
					sb.WriteString(chunk.Content)
					onEvent(StreamEvent{Type: "delta", Content: chunk.Content})
				}
			}

			if len(tcMap) > 0 {
				stepNum++
				// Notify recorder of each tool call
				for i := 0; i <= maxIdx; i++ {
					if tc, ok := tcMap[i]; ok && recorder != nil {
						recorder.OnToolCall(stepNum, tc.Function.Name, tc.Function.Arguments)
					}
				}
				// Collect tool call messages
				toolCalls := make([]schema.ToolCall, 0, len(tcMap))
				for i := 0; i <= maxIdx; i++ {
					if tc, ok := tcMap[i]; ok {
						toolCalls = append(toolCalls, *tc)
					}
				}
				produced = append(produced, &schema.Message{
					Role:      schema.Assistant,
					ToolCalls: toolCalls,
				})
			} else {
					answerText := sb.String()
					fullAnswer += answerText
					produced = append(produced, &schema.Message{
						Role:    schema.Assistant,
						Content: answerText,
					})
			}

		} else if !mo.IsStreaming {
			msg, _, err := adk.GetMessage(event)
			if err != nil {
				continue
			}
			if msg == nil {
				continue
			}

			switch msg.Role {
			case schema.Assistant:
				if len(msg.ToolCalls) > 0 {
					stepNum++
					produced = append(produced, msg)
					for _, tc := range msg.ToolCalls {
						if recorder != nil {
							recorder.OnToolCall(stepNum, tc.Function.Name, tc.Function.Arguments)
						}
					}
				}
				if msg.Content != "" {
					onEvent(StreamEvent{Type: "delta", Content: msg.Content})
					if len(msg.ToolCalls) == 0 {
						fullAnswer += msg.Content
						produced = append(produced, msg)
					}
				}

			case schema.Tool:
				produced = append(produced, msg)
				if recorder != nil {
					toolName := mo.ToolName
					recorder.OnToolResult(toolName, msg.Content, "")
				}
			}
		}
	}

	log.Println("[QA-Stream-H] ═══════════════════════════════════════════")

	if fullAnswer == "" {
		log.Println("[QA-Stream-H] ❌ Agent 未产生任何回答")
		onEvent(StreamEvent{Type: "error", Content: "agent produced no answer"})
		if recorder != nil {
			recorder.OnError(fmt.Errorf("agent produced no answer"))
		}
		return nil, fmt.Errorf("agent produced no answer")
	}

	onEvent(StreamEvent{Type: "done", Content: fullAnswer})
	if recorder != nil {
		recorder.OnFinalAnswer(fullAnswer)
	}
	return &ChatResult{Answer: fullAnswer, Messages: produced}, nil
}
