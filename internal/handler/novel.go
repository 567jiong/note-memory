package handler

import (
	"net/http"
	"note-memory/internal/model"
	"note-memory/internal/service/chapter"
	"note-memory/internal/service/novel"
	"note-memory/internal/service/qa"
	"note-memory/internal/service/recap"
	"note-memory/internal/service/search"
	"strconv"

	"github.com/gin-gonic/gin"
)

type NovelHandler struct {
	novelSvc  *novel.Service
	recapSvc  *recap.Service
	qaSvc     *qa.Service
	searchSvc *search.Service
}

func NewNovelHandler(
	novelSvc *novel.Service,
	recapSvc *recap.Service,
	qaSvc *qa.Service,
	searchSvc *search.Service,
) *NovelHandler {
	return &NovelHandler{novelSvc: novelSvc, recapSvc: recapSvc, qaSvc: qaSvc, searchSvc: searchSvc}
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

// TriggerFillEmbeddings triggers backfill of empty embedding fields from chapter content.
func (h *NovelHandler) TriggerFillEmbeddings(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	h.novelSvc.FillEmbeddings(id)
	c.JSON(http.StatusOK, gin.H{"message": "Embedding 回填已开始（使用章节内容，≤400字截断）"})
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

// AskQuestion handles spoiler-free Q&A.
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

	answer, err := h.qaSvc.AskQuestion(c.Request.Context(), id, req.Question)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "问答失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"question": req.Question, "answer": answer})
}

// Search performs hybrid semantic + full-text search on chapters.
func (h *NovelHandler) Search(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供搜索关键词 q"})
		return
	}

	progress, err := h.novelSvc.GetProgress(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先设置阅读进度"})
		return
	}

	results, err := h.searchSvc.HybridSearch(c.Request.Context(), query, id, progress.CurrentChapter, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "搜索失败: " + err.Error()})
		return
	}

	type sr struct {
		Chapter    model.Chapter `json:"chapter"`
		SemScore   float64       `json:"semantic_score"`
		TextScore  float64       `json:"text_score"`
		FinalScore float64       `json:"final_score"`
	}
	out := make([]sr, 0, len(results))
	for _, r := range results {
		out = append(out, sr{
			Chapter:    r.Chapter,
			SemScore:   r.SemScore,
			TextScore:  r.TextScore,
			FinalScore: r.FinalScore,
		})
	}

	c.JSON(http.StatusOK, gin.H{"results": out, "query": query, "mode": "hybrid"})
}

// Ensure chapter import is used for the type reference via novel.Service.
var _ = (*chapter.Service)(nil)
