package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/pgvector/pgvector-go"
)

// Novel represents a novel/book.
type Novel struct {
	ID            int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	Title         string    `json:"title" gorm:"type:varchar(500);not null"`
	Author        string    `json:"author" gorm:"type:varchar(200);default:''"`
	TotalChapters int       `json:"total_chapters" gorm:"default:0"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Novel) TableName() string { return "novels" }

// Chapter represents a single chapter in a novel.
type Chapter struct {
	ID            int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID       int64     `json:"novel_id" gorm:"index:idx_chapters_novel_number"`
	ChapterNumber int       `json:"chapter_number" gorm:"index:idx_chapters_novel_number"`
	Title         string    `json:"title" gorm:"type:varchar(500);default:''"`
	Content       string    `json:"content" gorm:"type:text;not null"`
	Summary       string    `json:"summary" gorm:"type:text;default:''"`
	Characters    JSONB            `json:"characters" gorm:"type:jsonb;default:'[]'"`
	Events        JSONB            `json:"events" gorm:"type:jsonb;default:'[]'"`
	Embedding     *pgvector.Vector `json:"-" gorm:"type:vector(1024)"`
	CreatedAt     time.Time        `json:"created_at"`
}

func (Chapter) TableName() string { return "chapters" }

// ReadingProgress tracks where the user is in a novel.
type ReadingProgress struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID        int64     `json:"novel_id" gorm:"uniqueIndex"`
	CurrentChapter int       `json:"current_chapter" gorm:"not null;default:1"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (ReadingProgress) TableName() string { return "reading_progress" }

// Recap stores a generated recap for a specific novel + progress point.
type Recap struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID        int64     `json:"novel_id" gorm:"uniqueIndex:idx_recap_novel_chapter"`
	CurrentChapter int       `json:"current_chapter" gorm:"uniqueIndex:idx_recap_novel_chapter"`
	RecapContent   string    `json:"recap_content" gorm:"type:text;not null"`
	CreatedAt      time.Time `json:"created_at"`
}

func (Recap) TableName() string { return "recaps" }

// QACache stores cached Q&A results for a specific novel + progress.
type QACache struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID        int64     `json:"novel_id" gorm:"index:idx_qa_novel_chapter"`
	CurrentChapter int       `json:"current_chapter" gorm:"index:idx_qa_novel_chapter"`
	Question       string    `json:"question" gorm:"type:text;not null"`
	Answer         string    `json:"answer" gorm:"type:text;not null"`
	CreatedAt      time.Time `json:"created_at"`
}

func (QACache) TableName() string { return "qa_cache" }

// EntityAlias maps nicknames/aliases to canonical character names for search expansion.
type EntityAlias struct {
	ID            int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID       int64  `json:"novel_id" gorm:"uniqueIndex:idx_entity_alias_novel"`
	CanonicalName string `json:"canonical_name" gorm:"type:varchar(200)"`
	Alias         string `json:"alias" gorm:"type:varchar(200);uniqueIndex:idx_entity_alias_novel"`
}

func (EntityAlias) TableName() string { return "entity_aliases" }

// ChapterChunk represents a content chunk within a chapter for fine-grained embedding search.
type ChapterChunk struct {
	ID         int64           `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID    int64           `json:"novel_id" gorm:"index:idx_chunks_novel"`
	ChapterID  int64           `json:"chapter_id" gorm:"index:idx_chunks_chapter"`
	ChunkIndex int             `json:"chunk_index" gorm:"not null;default:0"`
	Content    string          `json:"content" gorm:"type:text;not null"`
	Embedding  *pgvector.Vector `json:"-" gorm:"type:vector(1024)"`
	CharStart  int             `json:"char_start" gorm:"not null;default:0"`
	CharEnd    int             `json:"char_end" gorm:"not null;default:0"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (ChapterChunk) TableName() string { return "chapter_chunks" }

// AliasInfo holds canonical name and all its aliases (for search expansion).
type AliasInfo struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

// HybridSearchResult holds a ranked search result from hybrid search.
type HybridSearchResult struct {
	Chapter    Chapter `json:"chapter"`
	SemScore   float64 `json:"semantic_score"`
	TextScore  float64 `json:"text_score"`
	FinalScore float64 `json:"final_score"`
}

// --- Character / Event structs for JSONB serialization ---

type CharacterInfo struct {
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases,omitempty"`
	Status         string   `json:"status,omitempty"`
	FirstAppearance int     `json:"first_appearance,omitempty"`
}

type EventInfo struct {
	Title        string   `json:"title"`
	Participants []string `json:"participants,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Impact       string   `json:"impact,omitempty"`
	ChapterNum   int      `json:"chapter_num,omitempty"`
}

// --- JSONB type for GORM ---

type JSONB []byte

func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return []byte("[]"), nil
	}
	return []byte(j), nil
}

func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = []byte("[]")
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to scan JSONB: type assertion to []byte failed")
	}
	*j = make([]byte, len(bytes))
	copy(*j, bytes)
	return nil
}

// MarshalCharacters serializes a slice of CharacterInfo to JSONB.
func MarshalCharacters(chars []CharacterInfo) (JSONB, error) {
	b, err := json.Marshal(chars)
	if err != nil {
		return nil, err
	}
	return JSONB(b), nil
}

// UnmarshalCharacters deserializes JSONB to a slice of CharacterInfo.
func UnmarshalCharacters(j JSONB) ([]CharacterInfo, error) {
	if len(j) == 0 {
		return []CharacterInfo{}, nil
	}
	var chars []CharacterInfo
	if err := json.Unmarshal(j, &chars); err != nil {
		return nil, err
	}
	return chars, nil
}

// MarshalEvents serializes a slice of EventInfo to JSONB.
func MarshalEvents(events []EventInfo) (JSONB, error) {
	b, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	return JSONB(b), nil
}

// UnmarshalEvents deserializes JSONB to a slice of EventInfo.
func UnmarshalEvents(j JSONB) ([]EventInfo, error) {
	if len(j) == 0 {
		return []EventInfo{}, nil
	}
	var events []EventInfo
	if err := json.Unmarshal(j, &events); err != nil {
		return nil, err
	}
	return events, nil
}
