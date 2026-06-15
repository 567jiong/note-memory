package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino-ext/components/model/ark"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
)

// ModelConfig holds the configuration for creating Eino models.
type ModelConfig struct {
	// Chat
	APIKey  string
	BaseURL string
	Model   string

	// Embedding (optional, falls back to Chat config)
	EmbeddingAPIKey  string
	EmbeddingBaseURL string
	EmbeddingModel   string
}

// NewChatModel creates an Eino ToolCallingChatModel via Ark (OpenAI-compatible).
func NewChatModel(ctx context.Context, cfg ModelConfig) (einomodel.ToolCallingChatModel, error) {
	m, err := ark.NewChatModel(ctx, &ark.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("create ark chat model: %w", err)
	}
	return m, nil
}

// NewEmbedder creates an Eino Embedder via OpenAI component.
func NewEmbedder(ctx context.Context, cfg ModelConfig) (embedding.Embedder, error) {
	apiKey := cfg.EmbeddingAPIKey
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	baseURL := cfg.EmbeddingBaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}

	e, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   cfg.EmbeddingModel,
	})
	if err != nil {
		return nil, fmt.Errorf("create openai embedder: %w", err)
	}
	return e, nil
}
