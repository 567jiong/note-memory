package handler

import (
	"net/http"
	"note-memory/internal/model"
	"note-memory/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
)

type NovelHandler struct {
	novelSvc *service.NovelService
	recapSvc *service.RecapService
}

func NewNovelHandler(novelSvc *service.NovelService, recapSvc *service.RecapService) *NovelHandler {
	return &NovelHandler{novelSvc: novelSvc, recapSvc: recapSvc}
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

	// Trigger async AI parsing
	h.novelSvc.StartParse(result.Novel.ID)

	c.JSON(http.StatusOK, gin.H{
		"novel":   result.Novel,
		"message": "小说上传成功，正在后台进行 AI 解析...",
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
	c.JSON(http.StatusOK, gin.H{"message": "AI 解析已开始"})
}

// GenerateRecap generates a reading recovery recap.
func (h *NovelHandler) GenerateRecap(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	recap, err := h.recapSvc.GenerateRecap(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成回顾失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recap": recap})
}

// GetRecap returns a cached recap.
func (h *NovelHandler) GetRecap(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	progress, err := h.novelSvc.GetProgress(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先设置阅读进度"})
		return
	}

	recap, err := h.recapSvc.GetCachedRecap(id, progress.CurrentChapter)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "暂无回顾，请先生成"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recap": recap})
}
