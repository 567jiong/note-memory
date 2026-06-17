package memory

import (
	"context"
	"time"

	"github.com/cloudwego/eino/schema"
)

// SessionInfo is the metadata for a chat session.
type SessionInfo struct {
	ID        string    `json:"id"`
	NovelID   int64     `json:"novel_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatHistoryStore persists and restores conversation history, inspired by
// LangChain's BaseChatMessageHistory. System messages are never stored —
// they are injected at runtime.
type ChatHistoryStore interface {
	// Session CRUD
	CreateSession(ctx context.Context, sessionID string, novelID int64, title string) (*SessionInfo, error)
	GetSession(ctx context.Context, sessionID string) (*SessionInfo, error)
	ListSessions(ctx context.Context, novelID int64) ([]SessionInfo, error)
	DeleteSession(ctx context.Context, sessionID string) error

	// Message storage (gob-encoded []*schema.Message)
	ReadMessages(ctx context.Context, sessionID string) ([]*schema.Message, error)
	WriteMessages(ctx context.Context, sessionID string, msgs []*schema.Message) error
}
