# RAG 检索系统 — 技术设计文档

> 版本：3.0 | 日期：2026-06-12 | 状态：已实现

## 1. 架构概览

```
用户输入（问题 / 搜索词 / 回顾请求）
          │
          ▼
   ┌─ 查询预处理 ──────────────────────────┐
   │  1. 别名扩展： "韩跑跑" → "韩跑跑 韩立"   │
   │  2. 中文 bigram 分词：用于全文检索       │
   │  3. Embedding 向量化：用于语义检索       │
   └───────────────────────────────────────┘
          │
          ▼
   ┌─ 混合检索（Hybrid Search）─────────────┐
   │                                        │
   │  Chunk 向量检索          tsvector 全文检索│
   │  ┌──────────────┐    ┌──────────────┐  │
   │  │ cc.embedding │    │ ts_rank(tsv, │  │
   │  │ <=> 问题向量  │    │  to_tsquery) │  │
   │  │ → 按章节聚合  │    │ → 关键词匹配度 │  │
   │  └──────┬───────┘    └──────┬───────┘  │
   │         │                    │          │
   │         └──────┬─────────────┘          │
   │                ▼                        │
   │    Go 层合并：0.7×语义 + 0.3×全文       │
   └───────────────────────────────────────┘
          │
          ▼
   ┌─ 无剧透过滤 ──────────────────────────┐
   │  WHERE chapter_number <= 用户进度       │
   └───────────────────────────────────────┘
          │
          ▼
   ┌─ 上下文拼装 + LLM 生成 ───────────────┐
   │  相关章节摘要 + 人物 + 事件 → Prompt    │
   └───────────────────────────────────────┘
```

## 2. Embedding（向量嵌入）

### 2.1 模型

使用 BAAI/bge-large-zh-v1.5 模型（兼容 OpenAI Embedding API），维度 1024。
Embedding 仅用于 **Chunk 级别**（chapter_chunks 表），章节级 embedding 已移除。

```
输入: "叶尘在山洞中获得太虚真人传承戒指，开启修仙之路"
输出: [0.023, -0.451, 0.789, ...]  (1024 维 float32 向量)
```

### 2.2 原理

Embedding 将语义相近的文本映射到向量空间中邻近的位置：

```
"主角获得了一件法宝"    → [0.1, 0.2, ...]
"主角拿到了金手指"      → [0.12, 0.19, ...]  ← 语义相近，向量也相近
"今天天气很好"          → [-0.5, 0.8, ...]   ← 语义不同，向量很远
```

### 2.3 应用场景

| 场景 | 输入 | 检索目标 |
|------|------|---------|
| 问答 | 用户问题文本 | 相关章节摘要 |
| 回顾 | "主角状态 主线任务 关键事件 伏笔" | 覆盖多维度的章节 |
| 搜索 | 用户关键词 | 匹配度最高的章节 |

### 2.4 存储

使用 PostgreSQL `pgvector` 扩展：

```sql
-- 列定义
embedding vector(1536)

-- 余弦相似度检索（1 - cosine_distance = 相似度）
SELECT *, 1 - (embedding <=> query_vec) AS score
FROM chapters
ORDER BY embedding <=> query_vec
LIMIT 10;
```

## 3. 中文分词：jieba + 自定义词典

### 3.1 方案

采用 **jieba 分词 + 自定义词典 + Bigram fallback** 三层策略：

```
章节文本
   │
   ├── jieba 分词（通用词典）      → 准确切分常见词汇
   │     "主角/觉醒/能力/修炼"
   │
   ├── 自定义词典（每本小说独立）   → 玄幻术语强制保留
   │     词典来源: characters.name + aliases + events.title
   │     "掌天瓶" "九阳灵脉" "太虚诀" "筑基期"
   │
   └── Bigram fallback             → 未知新词兜底
         "九阳" "阳灵" "灵脉"  ← 即使词典没收，bigram 也能部分命中
```

### 3.2 词典自动构建

每本小说 AI 解析完成后，从所有章节的 characters 和 events 中提取实体名，自动生成 jieba 自定义词典：

```
# temp/note-memory-dicts/novel_1.txt
掌天瓶 100 n
九阳灵脉 100 n
太虚诀 100 n
韩立 100 n
叶尘 100 n
...
```

词频设为 100（高权重），确保 jieba 强制识别为完整词，不会切成碎片。

### 3.3 Bigram fallback 的必要性（保留）

即使有自定义词典，小说中仍可能出现新造的、先前未曾出现的术语。Bigram 保证这些新词至少能被部分匹配到。

## 4. 混合检索（Hybrid Search）

### 4.1 为什么需要混合检索

纯语义检索的问题：

- 对专有名词（人名、地名、法宝名）召回可能不准
- 低频术语在 embedding 训练语料中覆盖不足

纯全文检索的问题：

- 无法理解语义：搜"瓶子"找不到"掌天瓶"（除非完全匹配）
- 同义词、近义词无法召回

混合检索 = 语义 + 全文，互补增强。

### 4.2 加权公式

```
final_score = 0.7 × semantic_score + 0.3 × text_score

semantic_score = 1 - cosine_distance(embedding, query_vec)
                = 1 - (embedding <=> query_vec)

text_score = ts_rank(tsv, to_tsquery('simple', query_bigrams))
```

权重配置在 `repository/chapter.go` 中，可调整。

### 4.3 索引构建（写入时）

```
原始文本: "掌天瓶是韩立的金手指"
         ↓ jieba + 自定义词典
tokens:   "掌天瓶" "是" "韩立" "的" "金手指"
         ↓ + bigram fallback
合并:     "掌天瓶" "掌天" "天瓶" "韩立" "金手指" "金手" "手指" ...
         ↓ to_tsvector('simple', merged)
tsvector:  存入 chapters.tsv
```

自定义词典中 "掌天瓶" "韩立" "金手指" 词频=100，jieba 会强制保留为完整词。

#### 查询构建（搜索时）

```
用户输入: "掌天瓶"
         ↓ jieba + 自定义词典
tokens:   "掌天瓶"
         ↓ to_tsquery('simple', tokens)
tsquery:  '掌天瓶'
         ↓ PostgreSQL 执行
匹配:     tsvector 中包含 '掌天瓶' 的行 → 精确命中！
```

### 4.4 搜索范围

`search_text` 列包含以下内容的 bigrams + 完整词：

- 章节标题
- AI 摘要
- 人物名（保留完整词，如 "韩立"）
- 人物别名（如 "韩跑跑"）
- 事件标题（如 "获得掌天瓶"）

## 5. 别名映射（Alias Expansion）

### 5.1 数据来源

从 AI 提取的 `characters` JSON 自动构建：

```json
{
  "name": "韩立",
  "aliases": ["韩跑跑", "厉飞雨"]
}
```

### 5.2 映射表

```
entity_aliases 表:
┌──────────┬────────────────┬──────────┐
│ novel_id │ canonical_name │ alias    │
├──────────┼────────────────┼──────────┤
│ 1        │ 韩立           │ 韩立     │
│ 1        │ 韩立           │ 韩跑跑   │
│ 1        │ 韩立           │ 厉飞雨   │
│ 1        │ 太虚真人       │ 太虚真人 │
└──────────┴────────────────┴──────────┘
```

### 5.3 查询扩展

```
用户输入:  "韩跑跑现在什么境界"
           ↓ alias 扩展
扩展后:    "韩跑跑 韩立 现在什么境界"
           ↓ embedding
向量化时包含韩立和韩跑跑两个词的语义
```

### 5.4 构建时机

AI 解析完成后自动运行 `RebuildAliasIndex()`，遍历所有已解析章节的 characters，构建完整的别名映射表。

## 6. 无剧透保障机制

三层防护：

```
Layer 1: SQL 级别
  WHERE chapter_number <= user_progress  ← 数据库层硬过滤

Layer 2: Prompt 级别
  "你只能使用第 1~N 章的信息。绝对禁止引用第 N+1 章及以后的内容。"

Layer 3: 缓存隔离
  recap 缓存 key = (novel_id, chapter_number)
  进度变化 → 缓存失效 → 重新检索当前进度内的内容
```

## 7. 性能数据

| 指标 | 数值 |
|------|------|
| Embedding 维度 | 1536 |
| Embedding 单次耗时 | ~100-300ms |
| 批量 embedding 大小 | 100 条/批 |
| pgvector 检索（1000 条数据） | ~5-20ms |
| tsvector 检索（1000 条数据） | ~5-10ms |
| 混合检索总耗时 | ~100-400ms |
| 别名扩展 | < 1ms（内存查询） |

## 8. SQL 索引策略

```sql
-- 语义检索索引（IVFFlat，数据 >1000 条后启用）
CREATE INDEX idx_chapters_embedding ON chapters
  USING ivfflat (embedding vector_cosine_ops) WITH (lists = 50);

-- 全文检索索引（GIN，支持高效 tsquery）
CREATE INDEX idx_chapters_tsv ON chapters USING GIN (tsv);

-- 别名查询索引
CREATE INDEX idx_entity_aliases_lookup ON entity_aliases(novel_id, alias);

-- 章节查询索引
CREATE INDEX idx_chapters_novel_number ON chapters(novel_id, chapter_number);
```

## 9. 内容分块（Chapter Chunking）

### 9.1 动机

每章只存一个 Embedding（来自 AI 摘要）存在盲区：
- 摘要只有 2-3 句话，用户搜索"掌天瓶"时摘要可能用"神秘法宝"代替了这个词
- 一章可能有 3-5 个独立事件/场景，一个向量无法区分
- 摘要的语义粒度不足以匹配细节查询

### 9.2 分块策略

```
章节内容（最长 50000 字）
    ↓
句子切分（正则：。！？…\n）
    ↓
贪婪合并（≤400 字/块，BGE 512 token 限制）
    ↓
相邻块重叠 2 句（~50-100 字，保持语义连续）
    ↓
段落 \n\n 优先作为块边界（场景切换点）
    ↓
超长句（>400 字）在 ，；处切分
    ↓
chapter_chunks 表（每块独立 Embedding）
```

### 9.3 搜索降级链

```
查询 → Embedding → query_vec
    ↓
① Chunk 级搜索（chapter_chunks.embedding）         ← 首选，细粒度
    ↓ 无 Chunk
② 章级搜索（chapters.embedding）                    ← 降级，粗粒度
    ↓ 无 Embedding
③ 全文检索（chapters.tsv）                          ← 兜底
```

Chunk 搜索结果按 `chapter_id` 去重聚合，每章取最高分。

### 9.4 存储结构

```sql
CREATE TABLE chapter_chunks (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    chapter_id BIGINT REFERENCES chapters(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL DEFAULT 0,
    content TEXT NOT NULL,          -- 块文本 ≤400 字
    embedding vector(1024),         -- 块向量
    char_start INT NOT NULL,        -- 原文起始位置
    char_end INT NOT NULL           -- 原文结束位置
);
```

## 10. 文件清单

| 文件 | 职责 |
|------|------|
| `internal/ai/embedding.go` | Embedding API 调用 |
| `internal/service/chunker.go` | 内容分块引擎（句子切分、贪婪合并、重叠策略） |
| `internal/service/search.go` | 混合检索、别名扩展、Bigram 分词 |
| `internal/service/rag.go` | 语义搜索（Chunk 优先 + 章级降级）、Agentic RAG、上下文拼装 |
| `internal/service/qa.go` | 问答服务（集成 Agentic RAG） |
| `internal/repository/chapter.go` | SQL 层：HybridSearch / SearchChunks / SearchSimilar / 别名 CRUD |
| `internal/model/models.go` | ChapterChunk / HybridSearchResult / EntityAlias / AliasInfo |
| `migrations/002_pgvector.sql` | pgvector 列 + 扩展 |
| `migrations/003_search.sql` | search_text / tsv / entity_aliases |
| `migrations/004_chunks.sql` | chapter_chunks 表 + 索引 |
