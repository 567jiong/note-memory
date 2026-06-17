package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"note-memory/internal/model"
	"note-memory/internal/service/novel"
	"note-memory/internal/service/qa"
	"note-memory/internal/service/search"
	"strconv"

	"github.com/gin-gonic/gin"
)

type NovelHandler struct {
	novelSvc  *novel.Service
	qaSvc     *qa.Service
	searchSvc *search.Service
}

func NewNovelHandler(
	novelSvc *novel.Service,
	qaSvc *qa.Service,
	searchSvc *search.Service,
) *NovelHandler {
	return &NovelHandler{novelSvc: novelSvc, qaSvc: qaSvc, searchSvc: searchSvc}
}

// Upload handles TXT file upload.
func (h *NovelHandler) Upload(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择要上传的 TXT 文件"})
		return
	}
	defer file.Close()

	result, err := h.novelSvc.Upload(c.Request.Context(), file, header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "上传失败: " + err.Error()})
		return
	}

	h.novelSvc.StartParse(result.Novel.ID)

	c.JSON(http.StatusOK, gin.H{
		"novel":   result.Novel,
		"message": "小说上传成功，正在后台进行 AI 解析（总结 + embedding + 全文索引 + 别名）...",
	})
}

// List returns all novels.
func (h *NovelHandler) List(c *gin.Context) {
	novels, err := h.novelSvc.ListNovels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if novels == nil {
		novels = []model.Novel{}
	}
	c.JSON(http.StatusOK, gin.H{"novels": novels})
}

// Get returns a novel with chapters and progress.
func (h *NovelHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	novel, chapters, err := h.novelSvc.GetNovel(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "小说不存在"})
		return
	}

	progress, err := h.novelSvc.GetProgress(id)
	if err != nil {
		progress = nil
	}

	c.JSON(http.StatusOK, gin.H{
		"novel":    novel,
		"chapters": chapters,
		"progress": progress,
	})
}

// UpdateProgress updates reading progress.
func (h *NovelHandler) UpdateProgress(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	var req struct {
		CurrentChapter int `json:"current_chapter"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 current_chapter"})
		return
	}

	if err := h.novelSvc.UpdateProgress(id, req.CurrentChapter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "进度已更新", "current_chapter": req.CurrentChapter})
}

// TriggerParse triggers AI parsing.
func (h *NovelHandler) TriggerParse(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	h.novelSvc.StartParse(id)
	c.JSON(http.StatusOK, gin.H{"message": "AI 解析已开始（总结 + embedding + 全文索引）"})
}

// GenerateRecap generates a reading recovery recap.
// AskQuestion handles spoiler-free Q&A with SSE streaming.
func (h *NovelHandler) AskQuestion(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	var req struct {
		Question string `json:"question"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Question == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 question"})
		return
	}

	// SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	_, err = h.qaSvc.AskQuestion(c.Request.Context(), id, req.Question, func(evt qa.StreamEvent) {
		data, _ := json.Marshal(evt)
		switch evt.Type {
		case "done":
			c.SSEvent("done", string(data))
		case "error":
			c.SSEvent("error", string(data))
		default:
			c.SSEvent(evt.Type, string(data))
		}
		flusher.Flush()
	})

	if err != nil {
		c.SSEvent("error", fmt.Sprintf(`{"type":"error","content":"%s"}`, err.Error()))
		flusher.Flush()
	}
}

// ResyncGraph re-syncs existing chapter data to Neo4j without re-running AI.
func (h *NovelHandler) ResyncGraph(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	if err := h.novelSvc.ResyncGraph(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "图谱重同步失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "知识图谱重同步完成"})
}

// GetChapterContent returns the full content of a single chapter.
func (h *NovelHandler) GetChapterContent(c *gin.Context) {
	novelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	chapterNumber, err := strconv.Atoi(c.Param("number"))
	if err != nil || chapterNumber < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的章节号"})
		return
	}

	chapter, err := h.novelSvc.GetChapterContent(novelID, chapterNumber)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "章节不存在"})
		return
	}

	// Also get novel info for navigation context
	novel, _, err := h.novelSvc.GetNovel(novelID)
	if err != nil {
		novel = nil
	}

	c.JSON(http.StatusOK, gin.H{
		"chapter": chapter,
		"novel":   novel,
	})
}

// SearchChapters performs hybrid semantic+full-text search within a novel.
func (h *NovelHandler) SearchChapters(c *gin.Context) {
	novelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供搜索关键词 q"})
		return
	}

	// Get reading progress for spoiler-free boundary
	progress, err := h.novelSvc.GetProgress(novelID)
	maxChapter := 0
	if err == nil && progress != nil {
		maxChapter = progress.CurrentChapter
	}

	results, err := h.searchSvc.HybridSearch(c.Request.Context(), query, novelID, maxChapter, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "搜索失败: " + err.Error()})
		return
	}

	if results == nil {
		results = []model.HybridSearchResult{}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}
