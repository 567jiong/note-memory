项目方向：小说阅读回忆 Agent（Reading Memory Agent）

核心问题

* 用户阅读长篇小说（网文、轻小说、漫画、动漫等）时，经常出现断更、停看几周甚至几个月的情况。
* 回来继续阅读时会忘记：

  * 主角当前身份
  * 人物关系
  * 重要事件
  * 当前主线任务
  * 埋下的伏笔
* 用户真正需要的不是简单总结，而是快速恢复阅读状态。

产品定位

* 不是「小说总结器」。
* 而是「阅读记忆 Agent（Reading Memory Agent）」。

示例：
用户当前看到第501章。

Agent输出：

你当前应该记得：

* 主角当前身份
* 当前主线目标
* 最近发生的关键事件
* 重要人物状态
* 仍未揭晓的伏笔

并提供：

* 30秒回忆版
* 3分钟回忆版
* 人物关系版
* 时间线版

核心卖点
无剧透（Spoiler-Free）

用户读到：
第500章

系统只能使用：
第1~500章数据

禁止引用：
第501章及以后内容

例如：

用户问：
「张三和李四什么关系？」

Agent必须根据用户当前阅读进度回答，而不是根据全书结局回答。

为什么普通GPT不够
未来大模型上下文会越来越长。

如果只是：
「总结前500章内容」

GPT未来直接就能做。

因此没有护城河。

真正的价值在于：

* 阅读进度感知
* 无剧透
* 人物状态管理
* 事件时间线管理
* 阅读记忆恢复

技术架构思路

第一层：章节库

Chapter

* chapter_id
* title
* content

第二层：人物库

Character

* name
* aliases
* first_appearance
* current_status
* relationships

例如：

韩立

* 当前境界
* 当前身份
* 重要道具
* 最近事件

第三层：事件库

Event

* event_id
* chapter
* participants
* summary
* impact

例如：

事件：
获得掌天瓶

发生章节：
第7章

参与人物：
韩立

影响：
开启修仙之路

第四层：关系图谱

Character A
↓
师徒
↓
Character B

Character A
↓
敌对
↓
Character C

第五层：时间线

Chapter1
↓
Chapter2
↓
Chapter3
...
↓
Chapter500

Agent根据时间线判断哪些信息可见。

关于RAG的讨论

观点：
RAG没死。

死的是2023年的朴素RAG。

传统模式：

Query
↓
Embedding
↓
TopK Retrieval
↓
LLM

这种模式容易：

* 检索错误
* 混入剧透
* 缺乏时间概念

未来方向：

Agentic RAG

Agent
↓
搜索
↓
检索
↓
验证
↓
重新检索
↓
生成答案

更准确地说：

LLM = 大脑

RAG = 记忆

Agent = 行动系统

三者融合。

MVP方案

第一阶段（已完成 ✅）

目标：
跑通"上传 → 解析 → AI总结 → 无剧透回顾"完整闭环

功能：
* 上传TXT小说 → 自动识别章节标题（支持中文数字、阿拉伯数字、英文多种格式）
* AI 逐章总结 + 人物/事件提取（后台异步）
* 阅读进度设置（记录当前读到第几章）
* 无剧透阅读恢复回顾（30秒版 + 3分钟版）
* 简单 Web UI

技术栈：
* Go 1.22+
* Gin Web Framework
* PostgreSQL + GORM
* OpenAI 兼容 API（支持 OpenAI / DeepSeek 等）
* Go html/template + Tailwind CSS CDN

项目结构：
```
note-memory/
├── cmd/server/main.go         # 应用入口
├── internal/
│   ├── config/config.go       # 环境变量配置
│   ├── model/models.go        # GORM 数据模型
│   ├── parser/chapter.go      # TXT 章节解析器
│   ├── repository/            # 数据访问层
│   │   ├── novel.go
│   │   ├── chapter.go
│   │   ├── progress.go
│   │   └── recap.go
│   ├── service/               # 业务逻辑层
│   │   ├── novel.go           # 小说上传/管理
│   │   ├── chapter.go         # AI 章节总结
│   │   └── recap.go           # 回顾生成
│   ├── ai/openai.go           # OpenAI 客户端
│   ├── handler/novel.go       # HTTP 处理器
│   └── middleware/cors.go     # CORS
├── migrations/001_init.sql    # 数据库初始化
├── web/templates/             # 前端模板
│   ├── layout.html
│   ├── index.html
│   ├── novel.html
│   └── recap.html
└── .env.example
```

API 设计：

| Method | Path | 说明 |
|--------|------|------|
| POST | /api/novels | 上传 TXT 文件 |
| GET | /api/novels | 获取小说列表 |
| GET | /api/novels/:id | 获取小说详情 |
| PUT | /api/novels/:id/progress | 更新阅读进度 |
| POST | /api/novels/:id/parse | 触发 AI 解析 |
| POST | /api/novels/:id/recap | 生成回顾 |
| GET | /api/novels/:id/recap | 获取回顾 |

无剧透保障机制：
* 数据库查询严格过滤 chapter_number <= current_progress
* AI Prompt 明确标注进度边界，反复强调禁止使用后续章节
* 回顾缓存按 (novel_id, chapter_number) 缓存，进度变化自动重新生成

第二阶段（已完成 ✅ — 2026-06-11）

增加：
* ✅ pgvector 向量存储：章节摘要 → embedding (1536维) → 余弦相似度检索
* ✅ 无剧透智能问答：POST /api/novels/:id/ask，基于 RAG 语义检索 + 进度感知
* ✅ RAG 升级回顾生成：从"最近 N 章"改为语义检索最相关章节
* ✅ 语义搜索：GET /api/novels/:id/search?q=关键词
* ✅ Agentic RAG：检索 → LLM 验证 → 改写查询 → 重新检索 → 生成
* ✅ Web UI 聊天面板 + 搜索框

待后续：
* 人物关系图（知识图谱可视化）
* 事件时间线（交互式时间线浏览）
* 人物卡片/状态变化追踪

实际开发时间：
AI辅助开发：约 2 小时（10 个任务全部完成）

第三阶段（商业化）

增加：
* 用户系统
* 多小说支持（书架管理）
* 浏览器插件
* 阅读器插件
* 微信登录
* 订阅付费

预计时间：3~6 个月

启动与验证

启动方式：
1. 复制 .env.example 为 .env，填写数据库和 API 配置
2. 运行 PostgreSQL，创建 note_memory 数据库
3. go run cmd/server/main.go
4. 访问 http://localhost:8080

验证 MVP：
1. 准备测试 TXT 文件（包含章节标题的小说文本）
2. 通过 Web UI 上传
3. 等待后台 AI 解析完成
4. 设置阅读进度
5. 点击"生成回顾"，验证输出仅包含进度之前的章节内容

创业建议

不要先写3个月代码。

先验证需求。

做一个简单落地页：

标题：

"看到500章忘记前面剧情？30秒帮你恢复阅读状态，无剧透回顾。"

提供：

* 邮箱登记
* 微信群加入

先观察：

是否有100个以上真实用户愿意留下联系方式。

如果有：
继续开发。

如果没有：
及时调整方向。

更大的方向

不要局限于小说。

升级为：

长期内容消费记忆 Agent

支持：

* 小说
* 漫画
* 动漫
* 美剧
* 游戏剧情
* 在线课程

场景示例：

《海贼王》
停看一年后快速回忆。

《进击的巨人》
观看前恢复剧情记忆。

《黑神话：悟空》
半年后继续游戏时恢复剧情状态。

课程学习
间隔数月后恢复知识记忆。

最终愿景

构建一个"内容记忆层（Memory Layer）"。

用户不是来获取总结。

而是来恢复上下文状态（Restore Context）。

核心价值：
帮助用户在长时间中断后，以最短时间重新进入内容世界，并保证无剧透。
