# 实体向量化 + LLM 路由 + LLM 实体提取

> 版本：1.0 | 日期：2026-06-15 | 状态：已完成

---

## 1. 实体向量化 (Entity Embeddings)

### 问题
`entity_aliases` 表只能精确字符串匹配。用户搜"韩老魔"而表中只有"韩跑跑"时匹配失败。

### 方案
每个实体存储一段 LLM 生成的富描述文本 + 1024 维 Embedding，搜索时通过向量相似度语义匹配。

### 新增表
```sql
entity_embeddings (novel_id, entity_name, entity_type, description, embedding)
```

### 新增文件
- `migrations/005_entity_embeddings.sql`
- `internal/service/entity.go` — `EntityService{BuildEntityDescription, EmbedAndStoreEntity, SearchEntities}`
- `internal/model/models.go` — `EntityEmbedding` 模型
- `internal/repository/chapter.go` — `UpsertEntityEmbedding`, `SearchEntityEmbeddings`

### 集成
- `summarizeChapter` 完成后 → 为每个 characters 调用 `UpsertEntityFromChapter`
- `HybridSearch` → 搜索前先 `SearchEntities` 语义匹配 → 扩展搜索词

---

## 2. LLM 路由

### 问题
`classifyQuestion()` 基于 24 个硬编码关键词。新小说新术语无法适配。

### 方案
LLM 分析问题语义，输出结构化路由决策。

### 核心函数
```go
func (d *GraphDeps) llmRoute(ctx, question) routeDecision
```

LLM 返回 JSON：
```json
{"source": "pgvector|neo4j|both", "query_type": "fact|timeline|relation|mixed",
 "extracted_entities": [...], "search_query": "...", "reasoning": "..."}
```

### 变更文件
- `internal/agent/nodes.go` — 新增 `routeDecision`, `llmRoute`, `llmRoutePrompt`；`routerNode` 改为 LLM 路由，`classifyQuestion` 降级为 fallback
- `internal/graph/router.go` — 新增 `RouteQueryWithClass`，接受预分类结果

---

## 3. LLM 实体提取

### 问题
`realmPatterns` 用 14 条正则匹配境界。只支持修仙小说，变体名称匹配不到。

### 方案
AI 章节解析时 LLM 直接输出 `realm` 和 `realm_level` 字段。

### 变更
- `CharacterInfo` 新增 `Realm`/`RealmLevel` 字段
- 章节解析 Prompt 增加境界提取指令
- `GraphWriter.syncCharacter` 直接使用 AI 输出的 realm
- 删除 `realmPatterns` 数组和 `detectRealmChange()` 函数
- `realmLevel()` 保留作为 fallback

### 删除代码
- `internal/graph/writer.go`: `realmPatterns` (14 条正则), `detectRealmChange()`

---

## 4. 验证

```
go build ./...  ✅
go vet ./...    ✅
go test ./...   ✅ (零回归)
```
