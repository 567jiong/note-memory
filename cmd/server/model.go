package main

import (
	"context"
	"log"
	"note-memory/internal/config"
	"note-memory/internal/service/search"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino-ext/components/model/ark"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/embedding"
)

func newChatModel(cfg config.OpenAIConfig) einomodel.ToolCallingChatModel {
	m, err := ark.NewChatModel(context.Background(), &ark.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	})
	if err != nil {
		log.Fatalf("ChatModel 初始化失败: %v", err)
	}
	return m
}

func newReranker(cfg config.RerankConfig) search.Reranker {
	return search.NewHTTPReranker(cfg.APIKey, cfg.BaseURL, cfg.Model)
}

func newEmbedder(cfg config.OpenAIConfig) embedding.Embedder {
	apiKey := cfg.EmbeddingAPIKey
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	baseURL := cfg.EmbeddingBaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}

	e, err := openai.NewEmbedder(context.Background(), &openai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   cfg.EmbeddingModel,
	})
	if err != nil {
		log.Fatalf("Embedder 初始化失败: %v", err)
	}
	return e
}
