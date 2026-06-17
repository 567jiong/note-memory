package memory

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// ChatSessionManager orchestrates session lifecycle and message history.
// It combines a ChatHistoryStore with a MemoryStrategy to manage
// conversation context across multiple turns.
type ChatSessionManager struct {
	store    ChatHistoryStore
	strategy MemoryStrategy
}

// NewChatSessionManager creates a new manager. Default strategy is BufferWindow(K=10).
func NewChatSessionManager(store ChatHistoryStore, strategy MemoryStrategy) *ChatSessionManager {
	if strategy == nil {
		strategy = NewBufferWindow(10)
	}
	return &ChatSessionManager{store: store, strategy: strategy}
}

// CreateSession creates a new empty chat session.
func (m *ChatSessionManager) CreateSession(ctx context.Context, sessionID string, novelID int64, title string) (*SessionInfo, error) {
	return m.store.CreateSession(ctx, sessionID, novelID, title)
}

// GetSession returns session metadata.
func (m *ChatSessionManager) GetSession(ctx context.Context, sessionID string) (*SessionInfo, error) {
	return m.store.GetSession(ctx, sessionID)
}

// ListSessions returns all sessions for a novel, newest first.
func (m *ChatSessionManager) ListSessions(ctx context.Context, novelID int64) ([]SessionInfo, error) {
	return m.store.ListSessions(ctx, novelID)
}

// DeleteSession removes a session and its messages.
func (m *ChatSessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	return m.store.DeleteSession(ctx, sessionID)
}

// LoadHistory loads messages for a session, applying the memory strategy
// to keep context within limits. System messages should be prepended by the caller.
func (m *ChatSessionManager) LoadHistory(ctx context.Context, sessionID string) ([]*schema.Message, error) {
	msgs, err := m.store.ReadMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return m.strategy.ProcessMessages(msgs), nil
}

// LoadFullHistory reads ALL stored messages without applying the window strategy.
// Used when appending new messages to the complete history before saving.
func (m *ChatSessionManager) LoadFullHistory(ctx context.Context, sessionID string) ([]*schema.Message, error) {
	return m.store.ReadMessages(ctx, sessionID)
}

// AppendTurn appends this turn's messages to the full session history and saves.
// The store always keeps the complete history; LoadHistory applies the window on read.
func (m *ChatSessionManager) AppendTurn(ctx context.Context, sessionID string, turnMsgs []*schema.Message) error {
	if len(turnMsgs) == 0 {
		return nil
	}
	existing, err := m.store.ReadMessages(ctx, sessionID)
	if err != nil {
		return err
	}
	allMsgs := append(existing, turnMsgs...)
	return m.store.WriteMessages(ctx, sessionID, allMsgs)
}
