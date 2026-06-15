# Eino 框架集成 — 设计方案

> 版本：1.0 | 日期：2026-06-14 | 状态：Phase A 完成，Phase B 进行中

---

## 1. 动机

当前 `internal/service/rag.go` 中的 `AgenticRetrieve()` 是用硬编码 for 循环实现的：

```go
// 当前实现（rag.go:174）
for iteration = 1; iteration <= maxAgenticIterations; iteration++ {
    results, _ := s.searchSvc.HybridSearch(...)   // 步骤1：搜索
    verdict, _ := s.verifyRetrieval(...)           // 步骤2：LLM 验证
    if verdict.Sufficient { break }                // 步骤3：判断
    currentQuery = verdict.RewrittenQuery          // 步骤4：改写
}
```

**问题**：
- 控制流（循环/分支）和业务逻辑耦合，加一个步骤要改整个函数
- 状态散落在局部变量中（`seen`, `allChapters`, `currentQuery`, `iteration`）
- 无法做 CheckPoint 持久化、中断恢复、人工审核
- 与 Neo4j 图查询的路由逻辑（`router.go`）是独立的两条路径，不在同一编排层

**目标**：用 Eino 的 `compose.Graph` 替换硬编码循环，所有步骤统一为图节点，状态流转通过 `ReadingAgentState`。

---

## 2. 为什么选 Eino

Go 生态 5 个 LangGraph 替代方案对比：

| 维度 | Eino | LangGraphGo | golanggraph |
|------|------|-------------|-------------|
| 生产验证 | ✅ 字节跳动 | — | — |
| 类型安全 | ✅ 完整泛型 | ✅ | ⚠️ interface{} |
| CheckPoint | ✅ Redis/PG 等 | ✅ | ⚠️ 仅内存 |
| Interrupt/HITL | ✅ | ✅ | ❌ |
| 文档语言 | 中文 | 英文 | 英文 |
| 版本 | v0.9.6 | v0.8.5 | v0.0.10 |

**决定**：Eino v0.9.6，只用 `compose.Graph`（不碰 Workflow/ADK 等高阶抽象，降低学习成本）。

---

## 3. 目标图结构

```
                     START
                       │
                       ▼
                  ┌─────────┐
                  │ Router  │  问题分类（关键字匹配 → fact/timeline/relation/mixed）
                  └────┬────┘
                       │
              ┌────────┼────────┐
              ▼        ▼        ▼
         fact/mixed  timeline  relation
              │        │        │
              ▼        ▼        ▼
         ┌────────┐ ┌──────────────┐
         │ Search │ │    Graph     │  Neo4j 时间线/关系查询
         │ (PG)   │ └──────┬───────┘
         └───┬────┘        │
             │             │
             ▼             │
         ┌────────┐        │
         │ Verify │  LLM   │
         └───┬────┘        │
             │             │
        ┌────┴────┐        │
        ▼         ▼        │
   insufficient sufficient │
        │         │        │
        ▼         └────────┤
   ┌────────┐              │
   │Rewrite │──→ Search    │  循环最多 3 次
   └────────┘              │
                           ▼
                      ┌──────────┐
                      │ Generate │  组装上下文 + LLM 生成答案
                      └────┬─────┘
                           │
                           ▼
                          END
```

**关键设计**：
- Router 输出三种路径：`fact/mixed → Search`, `timeline/relation → Graph`
- Graph 节点直接通向 Generate（时间线/关系类问题不需要语义搜索二次验证）
- Mixed 路径走 Search → Verify 循环，同时在 Generate 节点合并 Graph 数据
- Rewrite → Search 形成循环边，由 Verify 节点的 `iteration >= MaxIterations` 兜底退出

---

## 4. 核心数据结构

### 4.1 ReadingAgentState

```go
// internal/agent/state.go
type ReadingAgentState struct {
    // 请求参数（不可变）
    NovelID    int64
    MaxChapter int
    NovelTitle string
    Question   string

    // 检索结果
    SearchResults  []model.HybridSearchResult
    SearchQuery    string  // 可被 rewriteNode 改写
    GraphTimeline  string  // Neo4j 格式化时间线
    GraphRelations string  // Neo4j 格式化关系
    GraphStatus    string  // Neo4j 状态变化

    // 控制流
    Iteration      int
    MaxIterations  int   // 默认 3
    RetrievalOK    bool
    MissingInfo    string
    RewrittenQuery string

    // 输出
    FinalContext string
    FinalAnswer  string
    QueryClass   string

    // 诊断
    Error string
}
```

### 4.2 EinoChatModel

```go
// internal/agent/chatmodel.go
type EinoChatModel struct {
    aiClient    *ai.Client
    maxTokens   int
    temperature float64
}
```

实现 `model.BaseChatModel` 接口（`Generate` + `Stream`），将现有 `ai.Client` 包装为 Eino 兼容的 ChatModel，保留独立的 Embedding 配置。

**Stream 方法用 `schema.StreamReaderFromArray` 单 chunk 包装**，因为底层 `ai.Client` 不支持流式。

### 4.3 GraphDeps

```go
// internal/agent/nodes.go
type GraphDeps struct {
    SearchSvc   *service.SearchService
    RagSvc      *service.RAGService
    GraphReader *graph.GraphReader
    AIClient    *ai.Client
}
```

所有节点函数作为 `GraphDeps` 的方法，通过闭包注入到 Eino Lambda 节点中。

---

## 5. 节点清单

| 节点 | 文件 | 职责 | 外部调用 |
|------|------|------|---------|
| `routerNode` | nodes.go | 关键字分类 → 设置 `QueryClass` | 无 |
| `classifyBranch` | nodes.go | 根据 QueryClass 路由到 search/graph | 无 |
| `searchNode` | nodes.go | HybridSearch + 降级语义搜索 | searchSvc, ragSvc |
| `graphNode` | nodes.go | Neo4j 时间线 + 关系查询 | graphReader |
| `verifyNode` | nodes.go | LLM 判定检索质量 → 设置 RetrievalOK | aiClient |
| `verifyBranch` | nodes.go | 根据 RetrievalOK 路由到 generate/rewrite | 无 |
| `rewriteNode` | nodes.go | 替换 SearchQuery 为 RewrittenQuery | 无 |
| `generateNode` | nodes.go | 组装上下文 + LLM 生成最终答案 | ragSvc, aiClient |

---

## 6. 图构建代码

```go
// internal/agent/graph.go
func (d *GraphDeps) BuildGraph() (*compose.Runnable[*ReadingAgentState, *ReadingAgentState], error) {
    g := compose.NewGraph[*ReadingAgentState, *ReadingAgentState]()

    g.AddLambdaNode("router",   compose.InvokableLambda(d.routerNode))
    g.AddLambdaNode("search",   compose.InvokableLambda(d.searchNode))
    g.AddLambdaNode("graph",    compose.InvokableLambda(d.graphNode))
    g.AddLambdaNode("verify",   compose.InvokableLambda(d.verifyNode))
    g.AddLambdaNode("rewrite",  compose.InvokableLambda(d.rewriteNode))
    g.AddLambdaNode("generate", compose.InvokableLambda(d.generateNode))

    g.AddEdge(compose.START, "router")
    g.AddBranch("router", compose.NewGraphBranch(d.classifyBranch, map[string]bool{
        "search": true, "graph": true,
    }))

    g.AddEdge("search", "verify")
    g.AddEdge("graph", "generate")

    g.AddBranch("verify", compose.NewGraphBranch(d.verifyBranch, map[string]bool{
        "rewrite": true, "generate": true,
    }))

    g.AddEdge("rewrite", "search")      // 循环边
    g.AddEdge("generate", compose.END)

    return g.Compile(ctx, compose.WithGraphName("reading-memory-agent"))
}
```

---

## 7. QAService 集成方式

```go
// internal/service/qa.go — AskQuestion 修改后

func (s *QAService) AskQuestion(ctx context.Context, novelID int64, question string) (string, error) {
    // ... 获取 novel 和 progress（不变）...

    state := &agent.ReadingAgentState{
        NovelID:       novelID,
        MaxChapter:    currentChapter,
        NovelTitle:    novel.Title,
        Question:      question,
        SearchQuery:   question,
        MaxIterations: 3,
    }

    runnable := s.agentGraph  // 启动时编译好，复用
    result, err := runnable.Invoke(ctx, state)
    if err != nil {
        return "", fmt.Errorf("agent graph: %w", err)
    }
    return result.FinalAnswer, nil
}
```

**降级策略**：如果 `agentGraph == nil`（Eino 未初始化），回退到旧的 `AgenticRetrieve() + RouteQuery()` 路径。

---

## 8. 文件清单

```
新增:
  internal/agent/
    ├── state.go         ✅ 已完成 — ReadingAgentState + QueryClass
    ├── chatmodel.go     ✅ 已完成 — EinoChatModel 适配 BaseChatModel
    ├── nodes.go         ✅ 已完成 — 所有节点函数 + GraphDeps
    └── graph.go         🔜 待完成 — BuildGraph 图构建

修改:
  internal/service/qa.go          🔜 待完成 — AskQuestion 走 Eino Graph
  go.mod / go.sum                 ✅ 已完成 — Eino v0.9.6 + 间接依赖

保留不动:
  internal/service/rag.go         — Search/BuildContext 保持，AgenticRetrieve 后续可删
  internal/graph/router.go        — graphNode 内部复用 RouteQuery
  internal/ai/openai.go           — 不动，chatmodel.go 只是包装
  internal/config/config.go       — 不动
```

---

## 9. 下一步

1. **graph.go** — 实现 `BuildGraph()`，编译验证
2. **qa.go 集成** — `AskQuestion` 走 Graph，旧路径作降级
3. **编译验证** — `go build ./...` + `go vet ./...`
4. **后续**（本次不做）— CheckPoint、Interrupt、LLM Router、DeepAgent
