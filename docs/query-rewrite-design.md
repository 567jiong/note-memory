# Query Rewrite：Step-back Prompting

> 版本：2.0 | 日期：2026-06-17 | 状态：已实现（Agent Prompt 级别）

---

## 设计决策

Step-back 放在 **Agent System Prompt** 中，由 LLM 自行判断何时使用，而非硬编码在 HybridSearch 里。

**为什么不放在 HybridSearch？**
- Agent 有对话上下文，能判断问题是否需要背景知识
- Agent 在 ReAct 循环中可以迭代 —— step-back 不行就换策略
- 不再增加额外的 LLM 调用（Agent 自己的思考不额外计费）
- 符合 v2.0 "Agent 自主决策" 的设计哲学

---

## 实现位置

`internal/service/qa/agent.go` — `agentInstruction` 中的 "步回检索策略（Step-back）" 段落。

Agent 收到需要背景知识的问题时（等级判定、体系分类、恩怨背景），会：

1. 先思考问题属于哪个更大的体系
2. 生成一个更抽象的"步回问题"
3. 用步回问题调用工具检索背景知识
4. 再用原始问题检索具体信息
5. 结合两轮结果回答

---

## 保留的底层能力

`internal/service/search/` 目录提供可复用的 RAG 组件：

| 文件 | 功能 |
|------|------|
| `search.go` | HybridSearch + SearchTool + ChaptersTool |
| `rrf.go` | applyRRF + applyWeightedRRF（融合算法） |
| `reranker.go` | Reranker 接口 + HTTPReranker（精排） |
| `rag.go` | Search() + BuildContext()（可复用检索+上下文拼装） |
