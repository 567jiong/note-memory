# Neo4j 知识图谱 — 混合架构技术设计

> 版本：1.0 | 日期：2026-06-12 | 状态：设计阶段

---

## 1. 动机

### 1.1 当前 PostgreSQL 无法高效处理的查询

| 查询场景 | 当前做法 | 痛点 |
|---------|---------|------|
| "韩立各境界突破年龄" | 扫全表 JSONB + 窗口函数 | 慢、SQL 复杂、无关联年龄 |
| "韩立和墨大夫的恩怨时间线" | 搜两个角色的章节 → LLM 推断 | 检索可能遗漏关键章 |
| "主角当前有哪些仇敌" | 搜"仇敌""敌人"关键词 → LLM | 无结构化关系 |
| "炼气期修士中有哪些人" | 无 | JSONB 不区分"境界"这个属性 |
| "太虚青灯的传承链" | 无 | 无法表达物品传递关系 |

### 1.2 根因

PostgreSQL 的 JSONB 只存储**每章独立快照**，没有跨章节的实体关联能力。

---

## 2. 混合架构

```
┌──────────────────────────────────────────────────┐
│                   用户问答                         │
│                      │                           │
│         ┌────────────┴────────────┐               │
│         ▼                         ▼               │
│   事实类问询                   关系/时间线问询       │
│  ("掌天瓶是什么")            ("境界突破年龄")       │
│         │                         │               │
│         ▼                         ▼               │
│  ┌──────────────┐         ┌──────────────┐        │
│  │ PostgreSQL   │         │    Neo4j     │        │
│  │              │         │              │        │
│  │ • Chunk 向量  │         │ • 实体节点    │        │
│  │ • 全文检索    │         │ • 关系边      │        │
│  │ • 章节内容    │         │ • 状态时间线  │        │
│  │ • 用户数据    │         │ • 图谱遍历    │        │
│  └──────┬───────┘         └──────┬───────┘        │
│         │                         │               │
│         └──────────┬──────────────┘               │
│                    ▼                               │
│              LLM 合成答案                          │
└──────────────────────────────────────────────────┘
```

**两个数据库不是替代关系，是互补关系。** PostgreSQL 管"这段内容讲了什么"，Neo4j 管"这些内容之间怎么关联"。

---

## 3. Neo4j 数据模型

### 3.1 节点类型

```
(:Novel)
  属性: id, title, author, total_chapters

(:Chapter)
  属性: id, novel_id, chapter_number, title

(:Character)          — 人物
  属性: id, novel_id, name, type (主角/配角/龙套),
         first_appearance_chapter, last_appearance_chapter

(:Item)               — 物品（法宝、丹药、功法、材料）
  属性: id, novel_id, name, type (法宝/丹药/功法/材料),
         rank (凡品/灵器/仙器/神器)

(:Realm)              — 修炼境界
  属性: id, novel_id, name, level (1=练气, 2=筑基...),
         description

(:Event)              — 剧情事件
  属性: id, novel_id, title, summary, impact,
         chapter_number

(:Location)           — 地点
  属性: id, novel_id, name, type (宗门/城市/秘境/洞府)

(:Faction)            — 势力/宗门
  属性: id, novel_id, name, type (宗门/家族/散修联盟)
```

### 3.2 关系类型

```
(:Character)-[:APPEARS_IN {status, age}]->(:Chapter)
  每章出现时可附带当前状态和年龄

(:Character)-[:BREAKTHROUGH_TO {at_chapter, age}]->(:Realm)
  境界突破关系，携带突破章节和年龄

(:Character)-[:OWNS {since_chapter, lost_chapter}]->(:Item)
  拥有物品的时间范围

(:Character)-[:MASTER_OF {since_chapter}]->(:Character)
(:Character)-[:FRIEND_OF {since_chapter}]->(:Character)
(:Character)-[:ENEMY_OF {since_chapter, ended_chapter}]->(:Character)
(:Character)-[:LOVE_INTEREST {since_chapter}]->(:Character)

(:Character)-[:BELONGS_TO {since_chapter, left_chapter}]->(:Faction)
  宗门归属

(:Character)-[:PARTICIPATES_IN {role}]->(:Event)
  参与事件

(:Event)-[:OCCURS_AT]->(:Location)
(:Event)-[:INVOLVES]->(:Item)
(:Event)-[:HAPPENS_IN]->(:Chapter)

(:Item)-[:CREATED_BY {at_chapter}]->(:Character)
(:Item)-[:TRANSFERRED_TO {at_chapter}]->(:Character)

(:Location)-[:PART_OF]->(:Location)
(:Location)-[:PART_OF]->(:Faction)
```

### 3.3 图模型示意

```
                        ┌──────────────┐
                        │ Novel: 凡人修仙传 │
                        └──────┬───────┘
                               │
         ┌─────────────────────┼─────────────────────┐
         ▼                     ▼                      ▼
   ┌──────────┐         ┌──────────┐           ┌──────────┐
   │ Chapter 1│         │Chapter 50│           │Chapter300│
   └────┬─────┘         └────┬─────┘           └────┬─────┘
        │                    │                      │
   APPEARS_IN           APPEARS_IN              APPEARS_IN
   status:凡人          status:筑基期            status:元婴期
   age: 15              age: 25                 age: 47
        │                    │                      │
        └────────────────────┼──────────────────────┘
                             ▼
                      ┌──────────────┐
                      │ Character    │
                      │ name: 韩立    │
                      └──────┬───────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
        BREAKTHROUGH_TO  MASTER_OF    ENEMY_OF
        at_chapter: 50   since: 120   since: 30
        age: 25                       ended: 200
              │              │              │
              ▼              ▼              ▼
        ┌──────────┐  ┌──────────┐  ┌──────────┐
        │ Realm    │  │Character │  │Character │
        │ 筑基期    │  │ 南宫婉    │  │ 墨大夫    │
        └──────────┘  └──────────┘  └──────────┘
```

---

---

## 4. 无剧透机制

### 4.1 关系自带时间属性

每条关系都携带 `since_chapter` 和可选的 `ended_chapter`，天然支持时间点查询：

```cypher
// 查询第 100 章时存在的所有师徒关系
MATCH (master:Character)-[r:MASTER_OF]->(disciple:Character)
WHERE r.since_chapter <= 100
  AND (r.ended_chapter IS NULL OR r.ended_chapter >= 100)
RETURN master.name, disciple.name, r.since_chapter
```

**看不到第 101 章及之后建立/结束的关系**——数据库层硬过滤。

### 4.2 三层防护（与 PG 一致）

```
Layer 1: Cypher 级别
  WHERE r.since_chapter <= $progress
    AND (r.ended_chapter IS NULL OR r.ended_chapter >= $progress)

Layer 2: Prompt 级别
  "你只能使用第 1~N 章的信息。绝对禁止引用第 N+1 章及以后的内容。"

Layer 3: 节点首次出现过滤
  WHERE other.first_appearance_chapter <= $progress
  → 还没出场的角色根本不会出现在结果里
```

### 4.3 第 N 章的关系图谱查询

```cypher
// 单条查询：当前进度下的完整人物关系网
MATCH (c:Character {novel_id: $novelId, name: $name})
      -[r]-(other:Character)
WHERE other.first_appearance_chapter <= $progress
  AND (r.since_chapter IS NULL OR r.since_chapter <= $progress)
  AND (r.ended_chapter IS NULL OR r.ended_chapter >= $progress)
RETURN c.name AS 主角,
       type(r) AS 关系类型,
       other.name AS 对方,
       r.since_chapter AS 始于,
       r.ended_chapter AS 止于
ORDER BY r.since_chapter
```

### 4.4 关系图谱变化追踪

Neo4j 独有的能力——**比对两个时间点的图谱快照**：

```cypher
// "第200章到第300章，韩立的关系网发生了什么变化？"

// 快照 200
MATCH (c {name: '韩立'})-[r200]-(other)
WHERE r200.since_chapter <= 200 AND (r200.ended_chapter IS NULL OR r200.ended_chapter >= 200)
WITH collect({name: other.name, type: type(r200)}) AS snap_200

// 快照 300
MATCH (c {name: '韩立'})-[r300]-(other)
WHERE r300.since_chapter <= 300 AND (r300.ended_chapter IS NULL OR r300.ended_chapter >= 300)
WITH snap_200, collect({name: other.name, type: type(r300)}) AS snap_300

RETURN
  [x IN snap_300 WHERE NOT x IN snap_200] AS 新增关系,
  [x IN snap_200 WHERE NOT x IN snap_300] AS 已结束关系
```

这在 PG 里几乎不可能实现，需要扫全表 JSONB + 差分计算。

---

## 5. 写入管道

### 4.1 整体流程

```
AI 解析章节 → 提取 characters + events + summary
    │
    ├── → PostgreSQL: 存 JSONB + Chunk + 全文索引（当前不变）
    │
    └── → Neo4j:  新 增 的 图 数 据 管 道
              │
              ├── UPSERT Entity 节点（Character/Item/Location/Faction）
              ├── CREATE 关系边（APPEARS_IN/PARTICIPATES_IN/OWNS/...）
              └── 状态变化检测 → BREAKTHROUGH_TO / BELONGS_TO 变更
```

### 4.2 实体提取增强

当前 AI Prompt 提取 `CharacterInfo` 只包含 `name/aliases/status`。需要扩展为结构化实体：

```
AI 解析章节的输出增强:
<entities>
{
  "characters": [
    {
      "name": "韩立",
      "aliases": ["韩跑跑"],
      "status": "突破筑基期",
      "age": 25,
      "realm": "筑基期",
      "faction": "黄枫谷",
      "items_owned": ["掌天瓶", "青竹蜂云剑"],
      "items_lost": ["玄铁令"]
    }
  ],
  "events": [
    {
      "title": "筑基突破",
      "participants": ["韩立"],
      "items_involved": ["筑基丹"],
      "location": "黄枫谷密室",
      "summary": "...",
      "impact": "韩立正式踏入筑基期"
    }
  ]
}
</entities>
```

### 4.3 写入 Neo4j 的时机

在 `summarizeChapter` 完成（摘要入库、Chunk 生成后），触发 Neo4j 同步：

```go
func (s *ChapterService) summarizeChapter(ctx context.Context, ch *model.Chapter) {
    // ... 现有流程（AI 总结、全文索引、别名、Chunk）...

    // 新增：同步到 Neo4j 知识图谱
    if s.graphWriter != nil {  // Neo4j 可选，未配置时跳过
        s.graphWriter.SyncChapter(ctx, ch, charsJSON, eventsJSON)
    }
}
```

---

## 6. 查询路由

### 6.1 路由决策

```
用户问题
    │
    ▼
┌─ QueryRouter.Classify(question) ─┐
│                                   │
│  关键词检测 + LLM 快速分类:        │
│                                   │
│  时间线类: "境界""突破""年龄"      │
│         "多少岁""时间线"           │
│           → Neo4j 查结构化数据     │
│                                   │
│  关系类: "师徒""仇敌""宗门"        │
│        "认识""关系"                │
│           → Neo4j 查图遍历         │
│                                   │
│  事实类: "是什么""怎么回事"         │
│         "剧情""发生了什么"          │
│           → PG Chunk 搜索 + LLM   │
│                                   │
│  混合类:                            │
│    → Neo4j + PG 双查，结果合并     │
└───────────────────────────────────┘
```

### 6.2 关键 Cypher 查询

**Q1: 主角各境界突破时间和年龄**

```cypher
MATCH (c:Character {novel_id: $novelId, type: '主角'})
      -[b:BREAKTHROUGH_TO]->(r:Realm)
RETURN r.name, r.level, b.at_chapter, b.age
ORDER BY r.level
```

**Q2: 当前进度的完整时间线**

```cypher
// 第 1 ~ N 章内，主角的所有状态变化
MATCH (c:Character {novel_id: $novelId, type: '主角'})
      -[rel]-(other)
WHERE rel.at_chapter IS NOT NULL AND rel.at_chapter <= $progress
RETURN c, rel, other
ORDER BY COALESCE(rel.at_chapter, rel.since_chapter, 0)
```

**Q3: 人际关系网**

```cypher
MATCH (c:Character {novel_id: $novelId, name: $name})
      -[r]-(other:Character)
WHERE r.since_chapter <= $progress
RETURN other.name, type(r), r.since_chapter, r.ended_chapter
```

**Q4: 物品传承链**

```cypher
MATCH path = (c1:Character)-[:OWNS]->(item:Item {name: $itemName})<-[:OWNS]-(c2:Character)
WHERE c1 <> c2
RETURN item.name, c1.name, c2.name
ORDER BY c1.first_appearance_chapter
```

---

## 7. 与现有系统的集成

### 7.1 写入集成

```
当前流程 (不变):
  summarizeChapter
    ├── AI 提取 → summary + characters + events
    ├── PG: UpdateSummary (摘要 + JSONB)
    ├── PG: UpdateSearchIndex (全文索引)
    ├── PG: UpsertChapterAliases (别名)
    └── PG: chunkAndEmbedChapter (Chunk + 向量)

新增流程:
  summarizeChapter
    └── 🆕 Neo4j: SyncChapter
         ├── UPSERT Character nodes + APPEARS_IN 关系
         ├── DETECT status changes → BREAKTHROUGH_TO / BELONGS_TO 等
         ├── UPSERT Item nodes + OWNS 关系
         └── UPSERT Event nodes + PARTICIPATES_IN 关系
```

### 7.2 查询集成

```
QAService.AskQuestion (当前):
  → AgenticRetrieve (PG Chunk 搜索 + LLM 验证)
  → BuildContext
  → LLM 生成

QAService.AskQuestion (改造后):
  → QueryRouter.Classify(question)
  ├── 事实类: 当前流程不变
  ├── 关系/时间线类:
  │     → Neo4j 执行 Cypher
  │     → 结构化结果注入 BuildContext
  │     → LLM 基于结构化时间线 + PG 章节摘要生成答案
  └── 混合类:
        → Neo4j + PG 双查
        → 结果合并注入 Prompt
```

### 7.3 新增代码清单

```
新 增:
  internal/graph/
    ├── neo4j.go           # Neo4j 驱动连接 + 会话管理
    ├── schema.go          # 约束 + 索引定义
    ├── writer.go          # 实体/关系写入（SyncChapter）
    ├── reader.go          # 查询方法（时间线/关系/传承）
    └── router.go          # 查询路由器（分类 → 路由到 PG 或 Neo4j）

修改:
  internal/service/chapter.go    # summarizeChapter 调用 graphWriter
  internal/service/qa.go         # AskQuestion 走路由器
  internal/config/config.go      # 增加 Neo4j 连接配置
  cmd/server/main.go             # 初始化 Neo4j 连接
  go.mod                         # 增加 neo4j-go-driver 依赖
```

---

## 8. 容错与降级

```
Neo4j 不可用时:
  ✅ 不影响现有功能（章节解析、Chunk、全文搜索正常）
  ✅ 图数据写入跳过（SyncChapter 返回 nil 时静默跳过）
  ✅ 查询降级为 PG 路径（Router 检测连接失败 → 走 Agentic RAG）

Neo4j 恢复后:
  → 手动触发 /api/novels/:id/rebuild-graph 全量重建
  → 或自动检测：下一章解析时自动恢复写入
```

---

## 9. 实施计划

### Phase A: 基础设施（~3h）

| # | 任务 | 说明 |
|---|------|------|
| 1 | `config.go` 增加 Neo4j 连接配置 | `NEO4J_URI`, `NEO4J_USER`, `NEO4J_PASSWORD` |
| 2 | `graph/neo4j.go` 驱动封装 | `go get github.com/neo4j/neo4j-go-driver/v5` |
| 3 | `graph/schema.go` 约束定义 | 唯一索引、属性索引 |
| 4 | `main.go` 初始化 Neo4j 连接 | 连接失败不阻塞启动 |

### Phase B: 写入管道（~5h）

| # | 任务 | 说明 |
|---|------|------|
| 5 | `graph/writer.go` 节点 UPSERT | Character/Item/Realm/Event/Faction/Location |
| 6 | `graph/writer.go` 关系写入 | APPEARS_IN/PARTICIPATES_IN/OWNS |
| 7 | 状态变化检测 | 对比上一章的 status，自动生成 BREAKTHROUGH_TO |
| 8 | `chapter.go` 集成 | summarizeChapter 末尾调用 `graphWriter.SyncChapter()` |
| 9 | 全量重建端点 | `POST /api/novels/:id/rebuild-graph` |

### Phase C: 查询集成（~5h）

| # | 任务 | 说明 |
|---|------|------|
| 10 | `graph/reader.go` 查询方法 | 时间线查询、关系网查询、传承链查询 |
| 11 | `graph/router.go` 查询路由 | 关键词 + LLM 分类 → 路由到 PG 或 Neo4j |
| 12 | `qa.go` 集成 | AskQuestion 走路由器 |
| 13 | BuildContext 扩展 | 支持结构化时间线数据注入 Prompt |

### Phase D: 全量测试（~3h）

| # | 任务 | 说明 |
|---|------|------|
| 14 | 单元测试 | writer/reader/router 各模块 |
| 15 | 集成测试 | testdata/test-novel.txt 端到端 |
| 16 | 文档更新 | PRD + 技术设计文档 |

**合计: ~16h**

---

## 10. 风险与考量

| 风险 | 缓解 |
|------|------|
| Neo4j 学习成本 | Cypher 是声明式，比窗口函数 SQL 简单得多 |
| 两个数据库维护成本 | Neo4j 完全可选，PG 功能独立运行不受影响 |
| AI 提取实体准确率 | 实体提取依赖 AI Prompt 增强，单独可调优 |
| 图数据一致性 | 全量重建端点可修复；写操作幂等（UPSERT） |
| 社区版限制 | Neo4j Community Edition 单数据库，够用 |

---

## 11. 环境依赖

```
go.mod 新增:
  github.com/neo4j/neo4j-go-driver/v5 v5.x

.env 新增:
  NEO4J_URI=bolt://localhost:7687
  NEO4J_USER=neo4j
  NEO4J_PASSWORD=password

Docker (开发环境):
  docker run -d --name neo4j-note \
    -p 7474:7474 -p 7687:7687 \
    -e NEO4J_AUTH=neo4j/password \
    neo4j:5-community
```
