package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Reranker re-scores document passages against a query using a cross-encoder model.
// Implementations call an external reranking API.
type Reranker interface {
	// Rerank scores each document against the query and returns relevance scores
	// in the same order as the input documents slice.
	Rerank(ctx context.Context, query string, documents []string) ([]float64, error)
}

// HTTPReranker calls an OpenAI-compatible rerank API.
//
// Request:  POST {baseURL}/rerank
// Body:     {"model": "...", "query": "...", "documents": ["..."]}
// Response: {"results": [{"index": 0, "relevance_score": 0.98}, ...]}
//
// Compatible with SiliconFlow, Cohere, VoyageAI, Jina AI rerank endpoints.
type HTTPReranker struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewHTTPReranker creates a new cross-encoder reranker client.
// Returns nil if apiKey is empty (reranker disabled, RRF-only fallback).
func NewHTTPReranker(apiKey, baseURL, model string) *HTTPReranker {
	if apiKey == "" {
		return nil
	}
	return &HTTPReranker{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// Rerank calls the rerank API and returns relevance scores in document order.
func (r *HTTPReranker) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if len(documents) == 0 {
		return nil, nil
	}

	body := rerankRequest{
		Model:     r.model,
		Query:     query,
		Documents: documents,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	url := r.baseURL + "/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("rerank API returned %d: %s", resp.StatusCode, string(b))
	}

	var result rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}

	// Build scores array in document order.
	scores := make([]float64, len(documents))
	for _, r := range result.Results {
		if r.Index >= 0 && r.Index < len(scores) {
			scores[r.Index] = r.RelevanceScore
		}
	}
	return scores, nil
}
