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
	Relations     JSONB            `json:"relations" gorm:"type:jsonb;default:'[]'"`
	Techniques    JSONB            `json:"techniques" gorm:"type:jsonb;default:'[]'"`
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

// EntityEmbedding stores a rich description + vector for semantic entity matching.
// This allows finding "韩立" when searching for "韩跑跑" or "掌天瓶持有者" via cosine similarity,
// handling aliases, titles, and descriptive phrases that exact string matching would miss.
type EntityEmbedding struct {
	ID         int64           `json:"id" gorm:"primaryKey;autoIncrement"`
	NovelID    int64           `json:"novel_id" gorm:"uniqueIndex:idx_entity_emb_novel_name"`
	EntityName string          `json:"entity_name" gorm:"type:varchar(200);uniqueIndex:idx_entity_emb_novel_name"`
	EntityType string          `json:"entity_type" gorm:"type:varchar(50);not null;default:'character'"`
	Description string         `json:"description" gorm:"type:text;not null"`
	Embedding  *pgvector.Vector `json:"-" gorm:"type:vector(1024)"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func (EntityEmbedding) TableName() string { return "entity_embeddings" }

// HybridSearchResult holds a ranked search result from hybrid search.
type HybridSearchResult struct {
	Chapter    Chapter `json:"chapter"`
	SemScore   float64 `json:"semantic_score"`
	TextScore  float64 `json:"text_score"`
	FinalScore float64 `json:"final_score"`
}

// --- Character / Event structs for JSONB serialization ---

type CharacterInfo struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases,omitempty"`
	Type            string   `json:"type,omitempty"`  // AI-classified role: 主角/重要配角/配角/反派/路人
	Status          string   `json:"status,omitempty"`
	Realm           string   `json:"realm,omitempty"`  // LLM-extracted realm name (e.g. "筑基期")
	FirstAppearance int      `json:"first_appearance,omitempty"`
}

type EventInfo struct {
	Title        string   `json:"title"`
	Participants []string `json:"participants,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Impact       string   `json:"impact,omitempty"`
	ChapterNum   int      `json:"chapter_num,omitempty"`
}

// CharacterRelation describes a relationship between two characters extracted from a chapter.
type CharacterRelation struct {
	FromName     string `json:"from_name"`
	ToName       string `json:"to_name"`
	RelationType string `json:"relation_type"`      // MASTER_OF, FRIEND_OF, ENEMY_OF, LOVE_INTEREST, BELONGS_TO
	SinceChapter int    `json:"since_chapter"`
	EndedChapter int    `json:"ended_chapter,omitempty"` // 0 = ongoing
	TriggerEvent string `json:"trigger_event,omitempty"` // Event title that caused this relationship
	Description  string `json:"description,omitempty"`   // Brief context
}

// TechniqueInfo describes a cultivation technique extracted from a chapter.
type TechniqueInfo struct {
	Name        string `json:"name"`                  // e.g. "青元剑诀"
	Level       string `json:"level,omitempty"`       // e.g. "第一层", "第九层"
	Practitioner string `json:"practitioner"`         // Character who learns/uses it
	Action      string `json:"action"`                // "习得", "突破", "施展"
	ChapterNum  int    `json:"chapter_num"`
	Description string `json:"description,omitempty"` // Brief context
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

// MarshalRelations serializes a slice of CharacterRelation to JSONB.
func MarshalRelations(relations []CharacterRelation) (JSONB, error) {
	b, err := json.Marshal(relations)
	if err != nil {
		return nil, err
	}
	return JSONB(b), nil
}

// UnmarshalRelations deserializes JSONB to a slice of CharacterRelation.
func UnmarshalRelations(j JSONB) ([]CharacterRelation, error) {
	if len(j) == 0 {
		return []CharacterRelation{}, nil
	}
	var relations []CharacterRelation
	if err := json.Unmarshal(j, &relations); err != nil {
		return nil, err
	}
	return relations, nil
}

// MarshalTechniques serializes a slice of TechniqueInfo to JSONB.
func MarshalTechniques(techniques []TechniqueInfo) (JSONB, error) {
	b, err := json.Marshal(techniques)
	if err != nil {
		return nil, err
	}
	return JSONB(b), nil
}

// UnmarshalTechniques deserializes JSONB to a slice of TechniqueInfo.
func UnmarshalTechniques(j JSONB) ([]TechniqueInfo, error) {
	if len(j) == 0 {
		return []TechniqueInfo{}, nil
	}
	var techniques []TechniqueInfo
	if err := json.Unmarshal(j, &techniques); err != nil {
		return nil, err
	}
	return techniques, nil
}
