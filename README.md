
# 📚 阅读记忆助手 (Reading Memory Agent)

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> 看到第 500 章却忘了前面剧情？30 秒帮你恢复阅读状态，**绝不剧透**。

**阅读记忆助手** 是一个面向长篇小说读者的 AI 记忆恢复工具。当你因为停更、弃坑或间断阅读而忘记前文剧情时，它能基于你当前的阅读进度，生成无剧透的回顾、回答你的问题，帮你快速重新进入故事世界。

---

## ✨ 核心亮点

### 🚫 严格无剧透
所有 AI 回答严格限定在用户阅读进度之前的内容，Prompt 层 + 数据层双重保障，绝不引用后续章节。

### 🧠 Agentic RAG 问答
基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 框架构建的 ADK Agent，拥有 **8 个工具**（搜索章节、查询人物关系、境界时间线、功法习得、事件查询等），自主决策调用哪些数据源来回答问题。支持 **SSE 流式输出**。

### 🔍 混合检索引擎
- **语义搜索**：pgvector 向量检索（BGE-large-zh-v1.5 1024维）
- **全文搜索**：jieba 中文分词 + PostgreSQL tsvector，支持 bigram 回退
- **RRF 融合**：Reciprocal Rank Fusion 合并语义 & 全文结果
- **Cross-Encoder 精排**：可选 BGE-Reranker 对 Top-15 重排序
- **实体扩展**：别名/特征描述 → 规范角色名的向量匹配
- **优雅降级**：无 Embedding API 时自动降级纯全文检索

### 🕸️ 知识图谱
Neo4j 图数据库建模人物、境界、功法、事件及其关系，支持跨章节的结构化追溯（可选组件，不配置则自动跳过）。

### 💬 聊天会话管理
类似 ChatGPT 的多轮对话，自动保存消息历史，BufferWindow 策略控制上下文长度，首条消息自动生成会话标题。

### 📖 章节解析
自动识别 TXT 文件章节标题（支持"第一章""第1章""Chapter 1"等多种格式），GBK 编码自动转 UTF-8。

---

## 🏗️ 技术架构

```
┌─────────────────────────────────────────────────────────┐
│                    前端 (HTML + Tailwind)                │
│               Server-rendered templates                 │
├─────────────────────────────────────────────────────────┤
│                   Gin Web Framework                     │
│              REST API + SSE Streaming                   │
├──────────────┬──────────────────┬───────────────────────┤
│   Service    │     Q&A Agent    │     Search Layer      │
│   Layer      │   (Eino ADK)     │                       │
│              │  8 Tools         │  Hybrid Search        │
│  Chapter     │  · search        │  · RRF Fusion         │
│  Parsing     │  · timeline      │  · Reranker           │
│  Summarize   │  · relations     │  · jieba Tokenizer    │
│  Entity      │  · techniques    │  · Entity Expansion   │
│  Extraction  │  · events        │                       │
│              │  · chapters      │                       │
│              │  · resolve       │                       │
│              │  · all_tech      │                       │
├──────────────┴──────────────────┼───────────────────────┤
│         PostgreSQL + pgvector   │   Neo4j (可选)        │
│   · 章节/Chunk/实体向量存储      │   · 人物关系图谱      │
│   · 全文检索 (tsvector)         │   · 境界突破时间线    │
│   · 会话消息持久化              │   · 功法/事件网络      │
└────────────────────────────────┴───────────────────────┘
```

### 技术栈

| 类别 | 技术 |
|------|------|
| **语言** | Go 1.25 |
| **Web 框架** | Gin |
| **ORM** | GORM |
| **主数据库** | PostgreSQL 15+ + pgvector 扩展 |
| **图数据库** | Neo4j 5 (可选) |
| **AI 框架** | CloudWeGo Eino (ADK Agent + ChatModel + Embedding) |
| **LLM** | OpenAI 兼容 API（DeepSeek / 硅基流动 / 任意兼容服务） |
| **Embedding** | BAAI/bge-large-zh-v1.5 (1024维) |
| **Reranker** | BAAI/bge-reranker-v2-m3 (可选) |
| **中文分词** | go-ego/gse (jieba 词典) |
| **前端** | Go html/template + Tailwind CSS CDN |

---

## 🚀 快速开始

### 前置要求

- **Go** 1.25+ (本地开发)
- **PostgreSQL** 15+ (需要 pgvector 扩展)
- **Neo4j** 5.x (可选，知识图谱)
- **LLM API Key** (DeepSeek / OpenAI / 硅基流动 等兼容服务)

### 方式一：Docker Compose（推荐）

```bash
# 1. 克隆项目
git clone https://github.com/yourusername/note-memory.git
cd note-memory

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env，填入你的 LLM API Key

# 3. 一键启动全部服务（PostgreSQL + Neo4j + App）
docker compose up -d

# 4. 访问
open http://localhost:8080
```

### 方式二：手动部署

```bash
# 1. 克隆项目
git clone https://github.com/yourusername/note-memory.git
cd note-memory

# 2. 安装 PostgreSQL
# macOS: brew install postgresql@15
# Ubuntu: sudo apt install postgresql-15
# 启用 pgvector 扩展（见下方说明）

# 3. 启动 Neo4j
docker run -d --name neo4j-note \
  -p 7474:7474 -p 7687:7687 \
  -e NEO4J_AUTH=neo4j/password \
  neo4j:5-community

# 4. 创建数据库
createdb note_memory

# 5. 配置环境
cp .env.example .env
# 编辑 .env，填入你的配置

# 6. 启动
go run ./cmd/server

# 7. 访问
open http://localhost:8080
```

### 配置 pgvector 扩展

```sql
-- 连接到 note_memory 数据库后执行
CREATE EXTENSION IF NOT EXISTS vector;
```

---

## ⚙️ 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DB_HOST` | PostgreSQL 主机 | `localhost` |
| `DB_PORT` | PostgreSQL 端口 | `5432` |
| `DB_USER` | 数据库用户名 | `postgres` |
| `DB_PASSWORD` | 数据库密码 | `postgres` |
| `DB_NAME` | 数据库名 | `note_memory` |
| `OPENAI_API_KEY` | LLM API Key | (必填) |
| `OPENAI_BASE_URL` | LLM API 地址 | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | 对话模型名 | `gpt-4o-mini` |
| `OPENAI_EMBEDDING_API_KEY` | Embedding API Key | 复用 `OPENAI_API_KEY` |
| `OPENAI_EMBEDDING_BASE_URL` | Embedding API 地址 | 复用 `OPENAI_BASE_URL` |
| `OPENAI_EMBEDDING_MODEL` | Embedding 模型 | `text-embedding-3-small` |
| `RERANK_API_KEY` | Reranker API Key | (空 = 禁用) |
| `RERANK_BASE_URL` | Reranker API 地址 | `https://api.siliconflow.cn/v1` |
| `RERANK_MODEL` | Reranker 模型 | `BAAI/bge-reranker-v2-m3` |
| `NEO4J_URI` | Neo4j 连接地址 | (空 = 禁用图谱) |
| `NEO4J_USER` | Neo4j 用户名 | `neo4j` |
| `NEO4J_PASSWORD` | Neo4j 密码 | `password` |
| `SERVER_PORT` | 服务端口 | `8080` |

> 💡 **推荐配置**：Chat 用 DeepSeek (`OPENAI_BASE_URL=https://api.deepseek.com/v1`)，Embedding 和 Reranker 用硅基流动（有免费额度）。

---

## 📡 API 文档

### 小说管理

| Method | Path | 说明 |
|--------|------|------|
| `POST` | `/api/novels` | 上传 TXT 文件（multipart/form-data） |
| `GET` | `/api/novels` | 获取小说列表 |
| `GET` | `/api/novels/:id` | 获取小说详情（含章节列表 + 阅读进度） |
| `PUT` | `/api/novels/:id/progress` | 更新阅读进度 `{"current_chapter": 500}` |
| `POST` | `/api/novels/:id/parse` | 手动触发 AI 解析 |
| `POST` | `/api/novels/:id/resync-graph` | 重同步知识图谱 |

### 搜索 & 阅读

| Method | Path | 说明 |
|--------|------|------|
| `GET` | `/api/novels/:id/search?q=关键词` | 混合搜索章节 |
| `GET` | `/api/novels/:id/chapters/:number` | 获取章节正文内容 |
| `POST` | `/api/novels/:id/ask` | 无剧透问答（SSE 流式） |

### 会话管理

| Method | Path | 说明 |
|--------|------|------|
| `POST` | `/api/novels/:id/sessions` | 创建聊天会话 |
| `GET` | `/api/novels/:id/sessions` | 获取会话列表 |
| `GET` | `/api/novels/:id/sessions/:sid/messages` | 获取会话消息历史 |
| `POST` | `/api/novels/:id/sessions/:sid/ask` | 会话内问答（SSE 流式） |
| `DELETE` | `/api/novels/:id/sessions/:sid` | 删除会话 |

### 页面路由

| Path | 说明 |
|------|------|
| `/` | 首页（上传 + 小说列表） |
| `/novels/:id` | 小说详情页（章节列表 + AI 问答面板） |
| `/novels/:id/read` | 阅读器页面（章节正文 + 笔记） |

---

## 🗂️ 项目结构

```
note-memory/
├── cmd/server/                    # 应用入口
│   ├── main.go                    # 启动、依赖注入、优雅关闭
│   ├── init.go                    # 依赖初始化
│   ├── model.go                   # LLM/Embedding/Reranker 工厂
│   └── router.go                  # Gin 路由配置
├── internal/
│   ├── config/config.go           # 环境变量配置
│   ├── model/models.go            # GORM 数据模型 + JSONB 序列化
│   ├── parser/chapter.go          # TXT 章节解析器
│   ├── repository/                # 数据访问层
│   │   ├── novel.go
│   │   ├── chapter.go             # 含 HybridSearch / FullTextSearch / Chunk 搜索
│   │   └── progress.go
│   ├── service/
│   │   ├── novel/                 # 小说上传/管理 + LLM 元数据提取
│   │   ├── chapter/               # AI 章节总结 + Chunk 分块 + Neo4j 同步
│   │   ├── search/                # 混合检索（jieba分词+向量+RRF+Reranker）
│   │   ├── qa/                    # ADK Agent 问答 + 流式输出 + 会话历史
│   │   ├── entity/                # 实体向量管理（角色描述 → embedding）
│   │   └── tools/                 # Agent 工具定义（8个工具）
│   ├── graph/                     # Neo4j 图谱层
│   │   ├── neo4j.go               # 驱动封装
│   │   ├── schema.go              # 约束和索引
│   │   ├── reader.go              # 图谱查询（时间线/关系/功法/事件）
│   │   └── writer.go              # 图谱写入（实体节点/关系边）
│   ├── memory/                    # 会话记忆层
│   │   ├── store.go               # ChatHistoryStore 接口
│   │   ├── postgres.go            # PostgreSQL 持久化实现
│   │   ├── manager.go             # ChatSessionManager
│   │   └── strategy.go            # BufferWindow 记忆策略
│   ├── handler/                   # HTTP 处理器
│   │   ├── novel.go               # 小说相关 API
│   │   └── session.go             # 会话相关 API
│   ├── middleware/cors.go         # CORS 中间件
│   └── migration/                 # 数据库迁移
│       ├── migrate.go             # 迁移编排
│       ├── pre/                   # 预迁移（扩展）
│       └── post/                  # 后迁移（索引、向量列）
├── web/
│   ├── templates/                 # Go 模板
│   │   ├── layout.html
│   │   ├── index.html             # 首页
│   │   ├── novel.html             # 小说详情 + 聊天
│   │   └── read.html              # 阅读器
│   └── static/                    # 静态资源
├── docs/                          # 设计文档
│   ├── rag-technical-design.md
│   ├── neo4j-knowledge-graph-design.md
│   ├── agentic-rag-design.md
│   └── ...
├── testdata/test-novel.txt        # 测试数据
├── Dockerfile
├── docker-compose.yml
├── .env.example
└── go.mod
```

---

## 🔬 核心技术详解

### Agentic RAG Agent

系统使用 Eino ADK 构建了一个 `ReadingMemoryAgent`，拥有 **8 个工具**，自主决策调用链路：

1. **search_chapters** — 混合搜索章节内容（RRF + Reranker）
2. **resolve_entity** — 别名/描述 → 规范角色名（实体向量匹配）
3. **query_timeline** — Neo4j 境界突破时间线
4. **query_relations** — Neo4j 人物关系图谱
5. **get_chapters** — 按范围获取章节摘要
6. **query_techniques** — Neo4j 功法习得时间线
7. **query_all_techniques** — 全书已知功法一览
8. **query_events** — Neo4j 人物参与事件查询

Agent 采用 **步回检索策略（Step-back）**：对于需要背景知识的问题，先检索更抽象的背景信息，再检索具体细节，两轮结合生成准确回答。最多支持 **6 轮工具调用迭代**。

### 混合搜索管道

```
用户查询
  │
  ├─→ 实体向量扩展（别名/描述匹配）
  │
  ├─→ jieba 分词（中文查询优化）
  │
  ├─→ pgvector Chunk 语义搜索 ─┐
  │                              ├─→ RRF 融合（Top-60）
  └─→ tsvector 全文搜索 ───────┘        │
                                        ├─→ Cross-Encoder Reranker（Top-15 → K）
                                        │    (可选，无配置则 RRF-only)
                                        └─→ 返回 Top-K 结果
```

### 内容分块策略

章节内容按句子边界（`。！？…`）切分为重叠块（≤400字），块级独立向量化，搜索时块级匹配 → 按章节聚合去重。相邻块重叠 2 句确保跨块语义不丢失。

### 无剧透保障

- **数据层**：所有 SQL/Cypher 查询严格 `WHERE chapter_number <= current_progress`
- **Prompt 层**：System Prompt 明确标注进度边界，反复强调禁止引用后续章节
- **工具层**：每个工具调用自动注入 `maxChapter` 参数，LLM 无法绕过

---

## 🧪 本地开发

```bash
# 安装依赖
go mod download

# 运行测试
go test ./...

# 启动开发服务器
go run ./cmd/server

# 编译
go build -o server ./cmd/server
```


---

## 🙏 致谢

- [CloudWeGo Eino](https://github.com/cloudwego/eino) — Go 语言 AI 应用框架
- [pgvector](https://github.com/pgvector/pgvector) — PostgreSQL 向量扩展
- [gse](https://github.com/go-ego/gse) — Go 中文分词引擎
- [Neo4j](https://neo4j.com/) — 图数据库
- [硅基流动](https://siliconflow.cn/) — 免费 Embedding API
- [DeepSeek](https://deepseek.com/) — 高性价比 LLM API
