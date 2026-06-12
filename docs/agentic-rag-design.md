# Agentic RAG 检索系统 — 设计文档

> 版本：1.0 | 日期：2026-06-12 | 状态：已实现

## 1. 什么是 Agentic RAG

传统 RAG（检索增强生成）是单次检索：

```
Query → Embedding → TopK Retrieval → LLM 生成
```

**Agentic RAG** 引入验证和改写循环：

```
Query → [混合检索] → [LLM 验证] → 不足? → [LLM 改写 Query] → [重新检索]
                ↓ 充足                          ↓ (最多 3 轮)
           [上下文拼装] ← ← ← ← ← ← ← ← ← ← ← ┘
                ↓
           [LLM 生成答案]
```

核心差异：**Agent 不是"检索一次就认命"，而是像人一样——搜一下看看够不够，不够就换个角度再搜。**

## 2. 架构

### 2.1 核心组件

```
RAGService
├── Search()              语义搜索（Chunk向量优先 → 章级降级 → 全文兜底）
├── BuildContext()        上下文拼装（章节摘要 + 人物 + 事件）
├── AgenticRetrieve()     多步检索入口（混合搜索 + 验证 + 改写循环）
│   ├── HybridSearch()    第 1 步：混合检索（语义向量搜索Chunk + 全文）
│   ├── verifyRetrieval() 第 2 步：LLM 评估检索质量
│   └── (loop)            第 3 步：改写查询 → 回到第 1 步
└── verifyRetrieval()     LLM 验证检索质量
    └── parseVerdict()    解析 LLM 返回的结构化 JSON
```

### 2.2 数据流

```
AgenticRetrieve(query, novelID, maxChapter, novelTitle)
    │
    ├── iteration 1:
    │   ├── HybridSearch(query) → results₁
    │   ├── verifyRetrieval(query, results₁) → verdict
    │   └── if sufficient → break
    │       if insufficient → currentQuery = verdict.rewritten_query
    │
    ├── iteration 2:
    │   ├── HybridSearch(rewritten_query) → results₂
    │   ├── merge(results₁, results₂) — 按 chapter_id 去重
    │   ├── verifyRetrieval(query, merged) → verdict
    │   └── ...
    │
    ├── iteration 3: (最后一次，无论如何都停止)
    │
    ├── dedupe + sort by chapter_number
    ├── trim to ~3000 chars (按 score 截断)
    └── BuildContext() → AgenticResult
```

## 3. 验证 Prompt 设计

### 3.1 System Prompt

```
你是一个检索质量评估器。判断检索到的章节摘要是否足以回答用户问题。

输出 JSON：{ "sufficient": bool, "reasoning": str, "missing": str, "rewritten_query": str }

判断标准：
- sufficient=true：章节摘要包含回答问题的关键信息
- sufficient=false：关键信息缺失，需改写查询
- 改写查询聚焦于缺失的具体信息（人名、事件、物品等关键词）
```

### 3.2 设计考量

- **只发送 Top 5 章节摘要给验证器**（节省 token）
- **Temperature 设为 0.3**（结构化输出需要低温度）
- **MaxTokens 设为 300**（验证结论应该很精简）
- **JSON 解析容错**：处理 LLM 可能输出的 ```json 代码块包装

## 4. 容错策略

| 场景 | 处理方式 |
|------|---------|
| LLM 验证调用失败（超时/报错） | 接受当前结果，标记 verified=false，退出循环 |
| LLM 返回非 JSON | 尝试提取 `{...}` 片段；失败则当 sufficient=true |
| 改写查询与原查询相同 | 退出循环（防止死循环） |
| 3 轮后仍 insufficient | 强制退出，使用累积的所有结果 |
| 检索返回 0 结果 | 跳过验证，继续下一轮（可能改写查询后有结果） |
| HybridSearch 失败 | 降级为纯语义搜索 |

## 5. 调用方集成

| 调用方 | 用途 | Agentic? | 说明 |
|--------|------|----------|------|
| `QAService.AskQuestion` | 智能问答 | ✅ 是 | 用户提问走完整 Agentic 循环 |
| `RecapService.GenerateRecap` | 回顾生成 | ✅ 是 | 回顾 query 走 Agentic 循环 |
| `NovelHandler.Search` | 搜索端点 | ❌ 否 | 搜索要快，直接用 HybridSearch |
| `QAService.SearchChapters` | 搜索兜底 | ❌ 否 | 同上，纯语义搜索兜底 |

## 6. Token 成本

- 每轮 Agentic 额外 LLM 调用：~500 tokens（验证 prompt + 响应）
- 最多 3 轮 → 最多 3 次额外调用 → ~1500 tokens
- 对于 Recap（query 固定），后续可考虑缓存验证结果
