package main

import (
	"note-memory/internal/handler"
	"note-memory/internal/middleware"

	"github.com/gin-gonic/gin"
)

func setupRouter(h *handler.NovelHandler, port int) *gin.Engine {
	r := gin.Default()
	r.MaxMultipartMemory = 64 << 20
	r.Use(middleware.CORS())

	api := r.Group("/api")
	{
		api.POST("/novels", h.Upload)
		api.GET("/novels", h.List)
		api.GET("/novels/:id", h.Get)
		api.PUT("/novels/:id/progress", h.UpdateProgress)
		api.POST("/novels/:id/parse", h.TriggerParse)
		api.POST("/novels/:id/resync-graph", h.ResyncGraph)
		api.GET("/novels/:id/search", h.SearchChapters)
		api.GET("/novels/:id/chapters/:number", h.GetChapterContent)

		// Chat sessions
		api.POST("/novels/:id/sessions", h.CreateSession)
		api.GET("/novels/:id/sessions", h.ListSessions)
		api.DELETE("/novels/:id/sessions/:sid", h.DeleteSession)
		api.GET("/novels/:id/sessions/:sid/messages", h.GetSessionMessages)
		api.POST("/novels/:id/sessions/:sid/ask", h.AskInSession)
	}

	r.LoadHTMLGlob("web/templates/*")
	r.Static("/static", "web/static")
	r.GET("/", func(c *gin.Context) { c.HTML(200, "index.html", nil) })
	r.GET("/novels/:id", func(c *gin.Context) { c.HTML(200, "novel.html", nil) })
	r.GET("/novels/:id/read", func(c *gin.Context) { c.HTML(200, "read.html", nil) })

	return r
}
