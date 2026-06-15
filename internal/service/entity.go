package service

import (
	"context"
	"fmt"
	"log"
	"note-memory/internal/agent"
	"note-memory/internal/model"
	"note-memory/internal/repository"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/schema"
	"github.com/pgvector/pgvector-go"
)

// EntityService manages entity embeddings for semantic entity matching.
// It generates rich text descriptions for entities and stores them as vectors
// so that users can search for characters by any alias, title, or descriptive phrase.
type EntityService struct {
	chapterRepo *repository.ChapterRepo
	chatModel   einomodel.ToolCallingChatModel
	embedder    embedding.Embedder
}

func NewEntityService(chapterRepo *repository.ChapterRepo, chatModel einomodel.ToolCallingChatModel, embedder embedding.Embedder) *EntityService {
	return &EntityService{
		chapterRepo: chapterRepo,
		chatModel:   chatModel,
		embedder:    embedder,
	}
}


// UpsertEntityFromChapter generates or updates an entity embedding for a character
// based on the latest chapter's extracted info. Called per-chapter after AI summarization.
func (s *EntityService) UpsertEntityFromChapter(ctx context.Context, novelID int64, char model.CharacterInfo) error {
	if char.Name == "" {
		return nil
	}

	// Gather existing info (aliases, prior description) for enrichment
	existing, _ := s.chapterRepo.ListAliases(novelID)
	var knownAliases []string
	for _, a := range existing {
		if a.Name == char.Name {
			knownAliases = a.Aliases
			break
		}
	}
	// Merge with current chapter's aliases
	aliasSet := make(map[string]bool)
	for _, a := range knownAliases {
		aliasSet[a] = true
	}
	aliasSet[char.Name] = true
	for _, a := range char.Aliases {
		aliasSet[a] = true
	}
	var allAliases []string
	for a := range aliasSet {
		allAliases = append(allAliases, a)
	}

	// Build description via LLM
	userPrompt := fmt.Sprintf(`人物名称：%s
别名列表：%s
当前状态：%s
首次出场章节：%d`,
		char.Name, strings.Join(allAliases, "、"), char.Status, char.FirstAppearance)

	msg, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(agent.EntityDescriptionPrompt()),
		schema.UserMessage(userPrompt),
	}, einomodel.WithTemperature(0.5), einomodel.WithMaxTokens(400))
	if err != nil {
		return fmt.Errorf("generate description for %s: %w", char.Name, err)
	}

	description := strings.TrimSpace(msg.Content)
	if description == "" {
		// Fallback: use name + aliases as minimal description
		description = fmt.Sprintf("%s，别名%s", char.Name, strings.Join(allAliases, "、"))
	}

	return s.storeEntityEmbedding(ctx, novelID, char.Name, "character", description)
}

// storeEntityEmbedding generates a vector for the description and upserts the record.
func (s *EntityService) storeEntityEmbedding(ctx context.Context, novelID int64, name, entityType, description string) error {
	vecs, err := s.embedder.EmbedStrings(ctx, []string{description})
	if err != nil || len(vecs) == 0 {
		return fmt.Errorf("embed entity %s: %w", name, err)
	}
	vec := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		vec[i] = float32(v)
	}

	pv := pgvector.NewVector(vec)
	ent := &model.EntityEmbedding{
		NovelID:     novelID,
		EntityName:  name,
		EntityType:  entityType,
		Description: description,
		Embedding:   &pv,
		UpdatedAt:   time.Now(),
	}

	if err := s.chapterRepo.UpsertEntityEmbedding(ent); err != nil {
		return fmt.Errorf("upsert entity embedding for %s: %w", name, err)
	}

	log.Printf("[entity] stored embedding for %s (%d chars)", name, len([]rune(description)))
	return nil
}

// SearchEntities performs vector similarity search on entity embeddings.
// Returns entity names that semantically match the query (useful for alias/identity resolution).
func (s *EntityService) SearchEntities(ctx context.Context, query string, novelID int64, topK int) ([]string, error) {
	vecs, err := s.embedder.EmbedStrings(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return nil, fmt.Errorf("embed search query: %w", err)
	}
	vec := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		vec[i] = float32(v)
	}

	results, err := s.chapterRepo.SearchEntityEmbeddings(novelID, pgvector.NewVector(vec), topK)
	if err != nil {
		return nil, fmt.Errorf("search entity embeddings: %w", err)
	}

	var names []string
	for _, r := range results {
		names = append(names, r.EntityName)
	}
	return names, nil
}
