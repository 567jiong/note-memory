# Agentic RAG / LightRAG — 设计文档

> 版本：2.0 | 日期：2026-06-15 | 状态：已实现

## 1. 架构演进

**v1.0（已废弃）**：硬编码 for 循环 — `Search → LLM验证 → 改写Query → 重试` 最多 3 轮，控制流和业务逻辑耦合在一起。

**v2.0（当前）**：ADK ChatModelAgent + 共享工具集 — LLM 自主选择数据源（pgvector / Neo4j / 全文检索），ReAct 循环由 ADK 框架管理，无硬编码轮次限制。

## 2. 核心思想：LightRAG

不同于传统 RAG 的 "Embedding → TopK → LLM生成" 单管线，**LightRAG** 给 LLM 配备了四个检索工具，由 LLM 自主决定：

- 走哪个数据源（pgvector 语义搜索 / Neo4j 图遍历 / 全文检索）
- 先查什么后查什么
- 是否需要改写查询重试
- 什么时候收集够了

```
用户需求
    │
    ▼
┌─────────────────────────────────────────┐
│        ADK ChatModelAgent (ReAct)        │
│                                          │
│  ┌────────────┐  ┌───────────────┐      │
│  │ search_     │  │ resolve_      │      │
│  │ chapters    │  │ entity        │      │
│  │ (pgvector + │  │ (向量匹配)     │      │
│  │  full-text) │  └───────────────┘      │
│  └────────────┘                          │
│  ┌────────────┐  ┌───────────────┐      │
│  │ query_      │  │ query_        │      │
│  │ timeline    │  │ relations     │      │
│  │ (Neo4j 境界)│  │ (Neo4j 关系)   │      │
│  └────────────┘  └───────────────┘      │
│                                          │
│  LLM 自主选择工具组合 → 多轮调用 → 输出   │
└─────────────────────────────────────────┘
    │
    ▼
最终输出（答案 / 上下文）
```

## 3. 共享工具集

所有检索工具定义在 `internal/service/tools/`，零内部依赖，可被 QA 和 RAG agent 共用：

```
internal/service/tools/
├── types.go    — SearchChaptersInput, TimelineEntry, RelationEntry 等
└── tools.go    — Deps + Build() 返回 4 个 tool.BaseTool
```

### Tool Deps（闭包注入）

```go
type Deps struct {
    NovelID    int64
    MaxChapter int

    SearchFunc     func(ctx, query, novelID, maxChapter, topK) (string, error)
    TimelineFunc   func(ctx, novelID, charName, maxChapter) (string, error)
    RelationsFunc  func(ctx, novelID, charName, maxChapter) (string, error)
    EntityFunc     func(ctx, query, novelID, topK) (string, error)
}
```

每个 Func 可为 nil — 对应工具优雅返回 `[]`，允许 agent 只配备可用的工具子集。

### Tool 工厂方法

各 service 提供工厂方法返回闭包，调用方无需写内联转换逻辑：

| 工厂方法 | 位置 | 数据源 |
|---------|------|--------|
| `searchSvc.SearchTool()` | `search/search.go` | pgvector + full-text |
| `graphReader.TimelineTool()` | `graph/reader.go` | Neo4j 境界时间线 |
| `graphReader.RelationsTool()` | `graph/reader.go` | Neo4j 人物关系 |
| `entitySvc.EntityTool()` | `entity/entity.go` | 实体向量匹配 |

## 4. 两个 Agent，同一套工具

### 4.1 QA Agent（阅读记忆助手）

- **位置**：`internal/service/qa/agent.go`
- **Instruction**："你是小说阅读记忆助手，帮助用户回忆剧情、人物关系、境界历程"
- **工具**：4 个全配（search + entity + timeline + relations）
- **MaxIterations**：8
- **输出**：整合后的中文答案

### 4.2 RAG Agent（检索代理）

- **位置**：`internal/service/search/agentic_rag.go`
- **Instruction**："你是检索代理，自主选择数据源收集上下文"
- **工具**：2 个（search + entity，timeline/relations 待 Neo4j 接入）
- **MaxIterations**：8
- **输出**：格式化的上下文文本（供 RecapService 二次生成）

两个 agent **共享同一套 `tools.Build()` 调用**，区别只在 instruction 和注入的工具数量。

## 5. 代码结构

```
internal/service/
├── tools/                    ← 共享工具集（零内部依赖）
│   ├── types.go
│   └── tools.go
├── qa/                       ← 问答模块
│   ├── qa.go                 — Service + AskQuestion
│   └── agent.go              — newReadingAgent + runReadingAgent
├── search/                   ← 检索模块
│   ├── search.go             — SearchService + SearchTool()
│   ├── rag.go                — RAGService + AgenticRetrieve
│   └── agentic_rag.go        — newAgenticRAGAgent + runAgenticRAG
├── entity/                   ← 实体模块
│   ├── entity.go             — EntityService + EntityTool()
│   └── descriptor.go         — newDescriptorAgent
├── chapter/                  ← 章节处理模块
│   ├── chapter.go            — ChapterService
│   ├── chunker.go            — ChunkContent
│   └── summarizer.go         — newSummarizerAgent
├── novel/                    ← 小说管理模块
│   ├── novel.go              — NovelService
│   └── metallm.go            — llmExtractMeta
└── recap/                    ← 回顾模块
    └── recap.go              — RecapService
```

每个模块自包含其 agent 创建逻辑，无跨模块 wiring。

## 6. 与 v1.0 的关键差异

| 维度 | v1.0（硬编码循环） | v2.0（ADK Agent） |
|------|-------------------|-------------------|
| 控制流 | for 循环 + if 分支 | ReAct 框架自动管理 |
| 检索路径 | 固定 HybridSearch | LLM 自主在 4 个工具间选择 |
| 迭代限制 | 硬编码 max 3 轮 | MaxIterations=8，LLM 自行决定何时停 |
| 验证方式 | 独立 LLM call + JSON parse | Agent 内部推理，无需额外 API 调用 |
| 查询改写 | LLM 输出 JSON 中的 rewritten_query | Agent 自行决定换个角度搜 |
| 工具复用 | QA 和 RAG 各自写闭包 | tools.Deps + 工厂方法，零重复 |

## 7. 调用方

| 调用方 | 使用的 Agent | 触发 |
|--------|------------|------|
| `QAService.AskQuestion` | QA Agent（4 工具） | 用户提问 |
| `RAGService.AgenticRetrieve` | RAG Agent（2 工具+） | 回顾生成时收集上下文 |
| `RecapService.GenerateRecap` | 间接通过 RAGService | 生成阅读恢复回顾 |
