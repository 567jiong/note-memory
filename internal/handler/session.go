package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"note-memory/internal/memory"
	"note-memory/internal/service/qa"

	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateSession creates a new chat session for a novel.
func (h *NovelHandler) CreateSession(c *gin.Context) {
	novelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	_ = c.ShouldBindJSON(&req)

	sessionID := uuid.New().String()
	info, err := h.sessionMgr.CreateSession(c.Request.Context(), sessionID, novelID, req.Title)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建会话失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"session": info})
}

// ListSessions returns all chat sessions for a novel.
func (h *NovelHandler) ListSessions(c *gin.Context) {
	novelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	sessions, err := h.sessionMgr.ListSessions(c.Request.Context(), novelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取会话列表失败: " + err.Error()})
		return
	}
	if sessions == nil {
		sessions = []memory.SessionInfo{}
	}

	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// GetSessionMessages returns the messages for a session (for frontend display).
func (h *NovelHandler) GetSessionMessages(c *gin.Context) {
	sessionID := c.Param("sid")

	msgs, err := h.sessionMgr.LoadHistory(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取消息失败: " + err.Error()})
		return
	}

	// Convert to frontend-friendly format (only user + assistant text messages)
	type frontendMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var out []frontendMsg
	for _, m := range msgs {
		if m == nil {
			continue
		}
		switch m.Role {
		case schema.User:
			out = append(out, frontendMsg{Role: string(schema.User), Content: m.Content})
		case schema.Assistant:
			// Skip tool-call-only messages (no text content)
			if m.Content != "" {
				out = append(out, frontendMsg{Role: string(schema.Assistant), Content: m.Content})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"messages": out})
}

// DeleteSession deletes a session and its messages.
func (h *NovelHandler) DeleteSession(c *gin.Context) {
	sessionID := c.Param("sid")

	if err := h.sessionMgr.DeleteSession(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除会话失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AskInSession handles Q&A within a chat session, with message history.
func (h *NovelHandler) AskInSession(c *gin.Context) {
	novelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的小说 ID"})
		return
	}

	sessionID := c.Param("sid")

	var req struct {
		Question string `json:"question"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Question == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 question"})
		return
	}

	// Load history
	history, err := h.sessionMgr.LoadHistory(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载会话失败: " + err.Error()})
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

	result, err := h.qaSvc.AskQuestionWithHistory(c.Request.Context(), novelID, history, req.Question, func(evt qa.StreamEvent) {
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
		return
	}

	// Append this turn's messages to the full history
	if result != nil && len(result.Messages) > 0 {
		if saveErr := h.sessionMgr.AppendTurn(c.Request.Context(), sessionID, result.Messages); saveErr != nil {
			c.SSEvent("error", fmt.Sprintf(`{"type":"error","content":"保存会话失败: %s"}`, saveErr.Error()))
			flusher.Flush()
		}
	}
}
