# 阅读记忆助手 — 前端交互增强计划

## Context

当前项目已完成后端核心能力（AI 章节解析、语义搜索、无剧透问答、知识图谱），但前端交互体验存在三个关键缺口：

1. **问答是同步阻塞的** — 用户提问后需要等待 LLM 完整生成完毕才能看到回复，体验差（大模型生成可能需要数十秒）
2. **没有真正的小说阅读界面** — 虽然有章节列表，但用户无法在系统中实际阅读章节内容，也无法在阅读过程中触发进度更新
3. **问答与阅读分离** — 当前问答区域在页面下方，用户阅读时需要反复上下滚动；缺乏"边看边问"的沉浸式体验

本计划将这三个问题一并解决，构建一个以阅读为核心的沉浸式页面。

## 目标架构

```
┌─────────────────────────────────────────────────────┐
│  Header: 小说标题 + 进度指示器                        │
├───────────────────────┬─────────────────────────────┤
│                       │                             │
│   阅读区 (70%)         │  AI 助手面板 (30%)           │
│                       │  ┌───────────────────────┐  │
│   ┌────────────────┐  │  │ 消息列表 (滚动)        │  │
│   │                │  │  │ ██ AI 回复流式输出     │  │
│   │  章节内容       │  │  │ ██ 用户提问           │  │
│   │  (可滚动)       │  │  │ ...                  │  │
│   │                │  │  └───────────────────────┘  │
│   │                │  │  ┌───────────────────────┐  │
│   │                │  │  │ 输入框 + 发送按钮       │  │
│   └────────────────┘  │  └───────────────────────┘  │
│                       │                             │
│   ◀ 上一章 | 下一章 ▶  │  可拖拽调整宽度              │
│                       │  可折叠/展开 (◀ ▶ 按钮)      │
├───────────────────────┴─────────────────────────────┤
│  底部状态栏: 章节进度 / 解析状态 / 最后更新时间       │
└─────────────────────────────────────────────────────┘
```

## 实现计划

### 一、SSE 流式输出

**核心问题**：当前 `POST /api/novels/:id/ask` 是同步 REST，需改造为 SSE 流式。

**技术基础**：
- Eino ADK 框架已内置流式支持，只需在 `RunnerConfig` 中设置 `EnableStreaming: true`
- ARK ChatModel 已实现 `Stream()` 方法
- Gin 框架支持 `c.Stream()` + `c.SSEvent()` 输出 SSE

**改动文件**：

#### 1.1 `internal/service/qa/qa.go` — 新增流式问答方法

新增 `AskQuestionStream(ctx, novelID, question, writer func(delta string)) (string, error)`：
- 创建 Agent 时设置 `EnableStreaming: true`
- 遍历 `iter.Next()` 事件循环，将 LLM 增量文本块通过回调 `writer(delta)` 实时推送
- 区分事件类型：推理内容 (`ReasoningContent`)、工具调用、最终文本
- 返回完整的最终 answer 字符串（用于缓存）

#### 1.2 `internal/handler/novel.go` — 新增 SSE 端点

新增 `AskQuestionStream(c *gin.Context)` 处理器：
```go
func (h *NovelHandler) AskQuestionStream(c *gin.Context) {
    // 设置 SSE 响应头
    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")
    c.Header("X-Accel-Buffering", "no")  // 禁用 nginx 缓冲
    
    // 调用流式服务，每个 delta 作为一个 SSE 事件推送
    h.qaSvc.AskQuestionStream(ctx, id, question, func(delta string) {
        c.SSEvent("delta", delta)
        c.Writer.Flush()
    })
    
    // 最后发送完成事件
    c.SSEvent("done", "")
}
```

#### 1.3 `cmd/server/router.go` — 注册新路由

新增路由：`api.POST("/novels/:id/ask-stream", h.AskQuestionStream)`

保留原有 `/ask` 非流式端点作为兼容。

**SSE 事件协议**：
| 事件类型 | 含义 | 数据 |
|---------|------|------|
| `thinking` | AI 思考过程 | 思考文本增量 |
| `tool_call` | 调用工具 | `{tool, args}` |
| `tool_result` | 工具返回 | `{tool, result}` |
| `delta` | 回答文本增量 | 文本片段 |
| `done` | 回答完成 | 完整 answer |
| `error` | 发生错误 | 错误信息 |

---

### 二、章节阅读界面

**核心问题**：当前无章节内容 API 和阅读 UI。

**技术基础**：
- `ChapterRepo.GetByNovelAndNumber(novelID, chapterNumber)` 已存在
- 章节内容字段 `Content` 已存储（最多 50000 字符）

**改动文件**：

#### 2.1 `internal/handler/novel.go` — 新增章节内容 API

新增 `GetChapterContent(c *gin.Context)` 处理器：
- 解析 URL 参数 `novelID` 和 `chapterNumber`
- 调用 `novelSvc.GetChapterContent(novelID, chapterNumber)` 返回章节完整信息（标题、内容、解析状态）
- 同时在请求中携带 `current_chapter` 可触发阅读进度自动更新

#### 2.2 `internal/service/novel/novel.go` — 新增服务方法

新增 `GetChapterContent(novelID, chapterNumber int64) (*model.Chapter, error)`：
- 封装 `chapterRepo.GetByNovelAndNumber` 调用
- 后续可在此处添加相邻章节预加载逻辑

#### 2.3 `cmd/server/router.go` — 注册路由

新增路由：`api.GET("/novels/:id/chapters/:number", h.GetChapterContent)`

#### 2.4 `web/templates/novel.html` 或新模板 — 重构为阅读视图

新增 `web/templates/read.html` — 独立的阅读页面模板：
- **布局**：左侧（70%宽）阅读区 + 右侧（30%宽）AI 助手面板
- **阅读区**：
  - 章节内容渲染（将纯文本转为 HTML 段落，`white-space: pre-wrap`）
  - 上一章 / 下一章 导航按钮
  - 章节标题显示
  - 阅读进度追踪：用户滚动到底部或点击"下一章"时自动调用 `PUT /api/novels/:id/progress`
  - 键盘导航：← → 键切换章节，支持快捷键
- **进度自动更新**：用户阅读到某个章节时，在 localStorage 暂存进度，适时同步到服务端（防抖 3 秒）
- 路由：`GET /novels/:id/read`

---

### 三、AI 助手面板（侧边栏）

**核心问题**：问答需要与阅读同屏，且可自由呼出/隐藏。

**改动文件**：

#### 3.1 集成到阅读页面 (`web/templates/read.html`)

AI 助手面板作为阅读页面的右侧区域：

**布局结构**：
```
<div class="flex h-screen">
  <!-- 阅读区：flex-1 -->
  <div id="reader-panel" class="flex-1 overflow-y-auto ...">
    <!-- 章节内容 -->
  </div>
  
  <!-- 分隔条：可拖拽调整宽度 -->
  <div id="resize-handle" class="w-1 cursor-col-resize bg-gray-200 hover:bg-blue-400 ..."></div>
  
  <!-- AI 助手面板 -->
  <div id="agent-panel" class="w-96 overflow-y-auto ...">
    <!-- 面板头部：标题 + 折叠按钮 -->
    <!-- 消息列表 -->
    <!-- 输入区域 -->
  </div>
</div>
```

**功能特性**：

1. **折叠/展开**：
   - 面板右侧有"◀"收缩按钮，点击后面板滑出屏幕
   - 收缩后在阅读区右下角显示浮动圆形按钮（💬），点击展开
   - 使用 CSS transition 动画，过渡时间 300ms

2. **可拖拽调整宽度**：
   - 中间分隔条支持拖拽调整面板宽度（200px ~ 600px 范围）
   - 使用 `mousedown` / `mousemove` / `mouseup` 事件实现
   - 拖拽时添加 `user-select: none` 防止文字选中

3. **SSE 流式问答**：
   - 使用 `EventSource` 连接到 SSE 端点
   - 实时渲染 AI 回复的文本增量
   - 显示工具调用状态指示器（"🔍 正在搜索相关章节..."）
   - 支持思考过程折叠显示（类似 ChatGPT 的 "Thinking" 区域）

4. **上下文感知**：
   - 提问时自动附带当前正在阅读的章节号
   - AI 回答中引用的章节号可点击跳转

5. **消息持久化**：
   - 消息历史存储在 localStorage，按小说 ID 分组
   - 面板展开时恢复历史消息

6. **快捷操作**：
   - 选中阅读区文字后可右键"问问 AI"（context menu）
   - 输入框支持 Shift+Enter 换行，Enter 发送

---

### 四、路由与入口调整

#### 4.1 `cmd/server/router.go`

新增路由：
```go
// 阅读页面
r.GET("/novels/:id/read", func(c *gin.Context) { c.HTML(200, "read.html", nil) })

// 章节内容 API
api.GET("/novels/:id/chapters/:number", h.GetChapterContent)

// SSE 流式问答
api.POST("/novels/:id/ask-stream", h.AskQuestionStream)
```

#### 4.2 入口调整

- 首页 `index.html`：小说卡片增加"📖 阅读"按钮，跳转到 `/novels/:id/read`
- `novel.html`（详情页）：增加"开始阅读"按钮，保留现有功能（搜索、问答可作为备用入口）
- 已有回顾页 `recap.html` 保持不变

---

### 五、前端技术选型与实现细节

#### 5.1 技术栈
- **纯 JavaScript + Tailwind CSS CDN**（与现有代码风格一致，不引入前端框架）
- SSE 使用 `EventSource` API（或 `fetch` + `ReadableStream` 用于 POST 请求的 SSE）
- 章节内容切换使用 `fetch` API

#### 5.2 POST 请求的 SSE 处理

由于浏览器原生 `EventSource` 仅支持 GET 请求，而问答需要 POST 传参，采用以下方案：
- 使用 `fetch` + `response.body.getReader()` 手动解析 SSE 流
- 编写通用的 `sseFetch(url, body)` 工具函数
- 解析 `text/event-stream` 格式（按 `\n\n` 分割，解析 `event:` 和 `data:` 行）

#### 5.3 章节内容渲染

```javascript
function renderChapterContent(text) {
    // 1. HTML 转义
    // 2. 按空行分段，每段包裹 <p> 标签
    // 3. 段落间添加间距
    // 4. 渲染到阅读区
}
```

#### 5.4 响应式设计
- 桌面端（>1024px）：左右分栏
- 平板端（768-1024px）：面板宽度缩小，阅读区为主
- 移动端（<768px）：面板默认隐藏，点击浮动按钮以全屏覆盖层形式展开

---

### 六、实施顺序

| 阶段 | 内容 | 依赖 |
|------|------|------|
| Phase 1 | 后端 SSE 流式问答（qa.go + handler + router） | 无 |
| Phase 2 | 后端章节内容 API（handler + service + router） | 无 |
| Phase 3 | 前端阅读页面 + AI 助手面板（read.html） | Phase 1, 2 |
| Phase 4 | 入口调整 + 响应式适配 | Phase 3 |
| Phase 5 | 测试、边界处理、错误提示优化 | Phase 4 |

---

### 七、验证方式

1. **SSE 流式输出**：
   - 启动服务，使用 curl 测试 `POST /api/novels/:id/ask-stream`，确认收到 `text/event-stream` 格式的增量响应
   - 在浏览器中打开阅读页面，提问后观察 AI 回复是否逐字显示
   - 验证工具调用状态指示器正常显示

2. **章节阅读**：
   - 访问 `/novels/:id/read`，验证章节内容正确渲染
   - 点击"上一章/下一章"验证导航正常
   - 使用键盘 ← → 键切换章节
   - 验证阅读进度自动更新到服务端（检查 `reading_progress` 表）

3. **AI 助手面板**：
   - 验证面板折叠/展开动画流畅
   - 验证拖拽调整宽度功能
   - 验证选中文字后提问功能
   - 验证消息历史持久化和恢复

4. **整体流程测试**：
   - 上传小说 → 点击"阅读" → 浏览章节 → 侧边栏提问 → 观察流式回复 → 进度自动更新
