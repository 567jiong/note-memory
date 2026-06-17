package memory

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// PostgresStore implements ChatHistoryStore using GORM + PostgreSQL.
// Messages are gob-encoded into a BYTEA column.
type PostgresStore struct {
	db *gorm.DB
}

func NewPostgresStore(db *gorm.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// AutoMigrate creates the chat_sessions and chat_messages tables.
func (s *PostgresStore) AutoMigrate() error {
	return s.db.AutoMigrate(&chatSession{}, &chatMessage{})
}

// ---- Session CRUD ----

func (s *PostgresStore) CreateSession(ctx context.Context, sessionID string, novelID int64, title string) (*SessionInfo, error) {
	now := time.Now()
	row := chatSession{
		ID:        sessionID,
		NovelID:   novelID,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return row.toInfo(), nil
}

func (s *PostgresStore) GetSession(ctx context.Context, sessionID string) (*SessionInfo, error) {
	var row chatSession
	if err := s.db.WithContext(ctx).Where("id = ?", sessionID).First(&row).Error; err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return row.toInfo(), nil
}

func (s *PostgresStore) ListSessions(ctx context.Context, novelID int64) ([]SessionInfo, error) {
	var rows []chatSession
	if err := s.db.WithContext(ctx).
		Where("novel_id = ?", novelID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	infos := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		infos = append(infos, *r.toInfo())
	}
	return infos, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).Where("id = ?", sessionID).Delete(&chatSession{}).Error
}

// ---- Message storage ----

func (s *PostgresStore) ReadMessages(ctx context.Context, sessionID string) ([]*schema.Message, error) {
	var row chatMessage
	if err := s.db.WithContext(ctx).Where("session_id = ?", sessionID).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // no messages yet
		}
		return nil, fmt.Errorf("read messages: %w", err)
	}
	return decodeMessages(row.Messages)
}

func (s *PostgresStore) WriteMessages(ctx context.Context, sessionID string, msgs []*schema.Message) error {
	data, err := encodeMessages(msgs)
	if err != nil {
		return fmt.Errorf("encode messages: %w", err)
	}

	row := chatMessage{
		SessionID: sessionID,
		Messages:  data,
		UpdatedAt: time.Now(),
	}
	// Update session timestamp
	s.db.WithContext(ctx).Model(&chatSession{}).Where("id = ?", sessionID).
		Update("updated_at", time.Now())

	return s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Assign(row).
		FirstOrCreate(&row).Error
}

// ---- DB Models ----

type chatSession struct {
	ID        string    `gorm:"column:id;primaryKey;type:text"`
	NovelID   int64     `gorm:"column:novel_id;not null;index"`
	Title     string    `gorm:"column:title;type:text;not null;default:''"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (chatSession) TableName() string { return "chat_sessions" }

func (s *chatSession) toInfo() *SessionInfo {
	return &SessionInfo{
		ID:        s.ID,
		NovelID:   s.NovelID,
		Title:     s.Title,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

type chatMessage struct {
	SessionID string    `gorm:"column:session_id;primaryKey;type:text"`
	Messages  []byte    `gorm:"column:messages;type:bytea;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (chatMessage) TableName() string { return "chat_messages" }

// ---- gob encode/decode ----

func encodeMessages(msgs []*schema.Message) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msgs); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeMessages(b []byte) ([]*schema.Message, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var msgs []*schema.Message
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}
