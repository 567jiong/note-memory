# Phase 2: RAG 智能检索 — 实施计划

> 状态：待审批 | 日期：2026-06-11

## Context

MVP Phase 1 已跑通基本闭环（上传 → 解析 → 回顾）。但回顾生成的上下文组装方式非常简陋：直接取"最近 20 章摘要"拼进 Prompt。这导致：

- 用户问"张三和李四什么关系"无法回答
- 回顾生成时关键章节（如伏笔章）可能被"最近 N 章"遗漏
- 章节越多，朴素截断方式越无效

Phase 2 引入 **Agentic RAG**：用向量语义搜索替代硬截断，实现精准检索 + 无剧透 Q&A。

## Phase 2 目标

| # | 功能 | 说明 |
|---|------|------|
| 1 | pgvector 向量存储 | 为章节摘要生成 embedding，支持语义相似度搜索 |
| 2 | 无剧透智能问答 | `POST /api/novels/:id/ask` — 用户可自由提问 |
| 3 | RAG 升级回顾生成 | 回顾不再取"最近 N 章"，而是语义检索最相关章节 |
| 4 | 角色/事件搜索 | 输入人名/事件名，检索所有相关章节（进度感知） |
| 5 | 回顾缓存失效 | 进度变化时智能判断是否需要重新生成 |

## 技术架构

```
用户提问: "韩立现在什么境界？"
         ↓
   [Embedding] → 问题向量
         ↓
   [pgvector 检索] → TopK 最相关章节（过滤 chapter <= progress）
         ↓
   [Agent 验证] → 检索到的章节是否足以回答问题？
         ↓ (不足则改写 query 重检索)
   [上下文拼装] → 相关章节摘要 + 人物信息 + 事件
         ↓
   [LLM 生成] → 无剧透答案
```

## 数据库变更

### 1. 启用 pgvector 扩展

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

### 2. chapters 表加 embedding 列

```sql
ALTER TABLE chapters ADD COLUMN embedding vector(1536);
CREATE INDEX idx_chapters_embedding ON chapters USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
```

> 维度 1536 对应 OpenAI `text-embedding-3-small` 模型

### 3. 新增 Q&A 缓存表

```sql
CREATE TABLE IF NOT EXISTS qa_cache (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    current_chapter INT NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_qa_novel_chapter ON qa_cache(novel_id, current_chapter);
```

## 代码变更清单

### 新文件

| 文件 | 用途 |
|------|------|
| `internal/ai/embedding.go` | Embedding API 调用 |
| `internal/service/rag.go` | RAG 检索 + Agent 验证 + 上下文拼装（核心） |
| `internal/service/qa.go` | Q&A 服务（问题 → 检索 → 生成答案） |
| `internal/handler/qa.go` | Q&A、搜索 API handler |
| `migrations/002_pgvector.sql` | pgvector 迁移文件 |

### 修改文件

| 文件 | 变更内容 |
|------|---------|
| `internal/model/models.go` | Chapter 加 `Embedding` 字段；加 `QACache` 模型 |
| `internal/ai/openai.go` | 加 `Embedding()` 方法 |
| `internal/repository/chapter.go` | 加向量检索方法 `SearchSimilar` |
| `internal/service/chapter.go` | 总结后同步生成 embedding |
| `internal/service/recap.go` | 用 RAG 替代"最近 N 章" |
| `internal/handler/novel.go` | 加 Q&A、搜索路由 |
| `cmd/server/main.go` | 注册新路由 |
| `go.mod` | 加 pgvector Go 驱动依赖 |

## 核心逻辑设计

### 1. Embedding 生成

```go
func (c *Client) Embedding(ctx context.Context, text string) ([]float32, error)
func (c *Client) EmbeddingBatch(ctx context.Context, texts []string) ([][]float32, error)
```

调用 `POST /embeddings`，模型 `text-embedding-3-small`，维度 1536。

### 2. RAG 检索器

```
RAGService
├── Search(query, novelID, maxChapter) → []Chapter
│   1. embedding(query) → queryVec
│   2. SELECT * FROM chapters WHERE novel_id=? AND chapter_number<=?
│      ORDER BY embedding <=> queryVec LIMIT ?
│   3. 返回 TopK 最相关章节
│
├── AgenticRetrieve(query, novelID, maxChapter) → context string
│   1. Search(query) → 初检结果
│   2. LLM 验证: "这些章节能否回答用户问题？"
│   3. 若不能 → 改写 query → 重新 Search
│   4. 拼装上下文返回
│
└── BuildContext(chapters, characters, events) → promptString
```

### 3. 智能 Q&A

```
POST /api/novels/:id/ask
Body: { "question": "韩立现在什么境界？" }

流程:
1. 获取阅读进度
2. AgenticRetrieve(question, novelID, progress)
3. 构建 system prompt（强调无剧透边界）
4. LLM 生成答案
5. 缓存到 qa_cache
```

### 4. RAG 升级回顾

原来：`ListRecentChapters(novelID, progress, 20)` — 固定取最近 20 章

改为：
1. 生成回顾专用 query: "主角当前状态、主线任务、最近关键事件、人物关系、伏笔"
2. Search(query, novelID, progress) → TopK=30 最相关章节
3. 按章节号排序（保持时间线）
4. 拼装上下文 → LLM 生成

### 5. 进度感知搜索（无剧透核心）

```sql
-- 所有检索都带上进度过滤
WHERE novel_id = $1
  AND chapter_number <= $2  -- 无剧透边界
  AND embedding IS NOT NULL
ORDER BY embedding <=> $3
LIMIT $4
```

## API 新增

| Method | Path | 说明 |
|--------|------|------|
| `POST` | `/api/novels/:id/ask` | 无剧透问答 |
| `GET` | `/api/novels/:id/search?q=关键词` | 语义搜索相关章节 |
| `POST` | `/api/novels/:id/embed` | 触发 embedding 生成 |

## 实施步骤（10 个任务）

| # | 任务 | 预计时间 |
|---|------|---------|
| 1 | pgvector 环境搭建 + DB 迁移 | 0.5h |
| 2 | Embedding API 封装 | 0.5h |
| 3 | 向量检索 Repository | 1h |
| 4 | RAG 检索服务（核心） | 2h |
| 5 | Embedding 生成集成 | 1h |
| 6 | 智能 Q&A 端点 | 1h |
| 7 | RAG 升级回顾生成 | 1h |
| 8 | 语义搜索端点 | 0.5h |
| 9 | Web UI 升级（聊天面板 + 搜索框） | 1.5h |
| 10 | 测试 + PRD 更新 | 1h |

## 依赖

```go
// go.mod 新增
github.com/pgvector/pgvector-go
```

## 验证方式

1. 上传 50+ 章测试小说 → 生成 embedding
2. 提问："主角在第 20 章时是什么状态？" → 确认答案仅基于 1~20 章
3. 搜索："掌天瓶" → 确认返回所有相关章节（且在进度以内）
4. 修改进度到第 30 章 → 重新生成回顾 → 确认可引用新内容
5. 确认 31 章及以后内容从未出现在任何回答中（无剧透验证）
