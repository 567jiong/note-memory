package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// EmbeddingRequest is the request body for the embeddings API.
type EmbeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

// EmbeddingResponse is the response from the embeddings API.
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

const embeddingModel = "text-embedding-3-small"

// Embedding generates an embedding vector for a single text.
func (c *Client) Embedding(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := c.EmbeddingBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return embeddings[0], nil
}

// EmbeddingBatch generates embedding vectors for multiple texts.
// OpenAPI text-embedding-3-small supports up to 2048 inputs per batch.
func (c *Client) EmbeddingBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Batch to avoid API limits (100 per batch is safe)
	const batchSize = 100
	var allEmbeddings [][]float32

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		batchResult, err := c.embeddingRequest(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("batch [%d:%d]: %w", i, end, err)
		}
		allEmbeddings = append(allEmbeddings, batchResult...)
	}

	return allEmbeddings, nil
}

func (c *Client) embeddingRequest(ctx context.Context, inputs []string) ([][]float32, error) {
	model := c.embeddingModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL := c.embeddingBaseURL
	if baseURL == "" {
		baseURL = c.baseURL
	}

	reqBody := EmbeddingRequest{
		Model: model,
		Input: inputs,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	apiKey := c.embeddingAPIKey
	if apiKey == "" {
		apiKey = c.apiKey
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error (status %d) from %s: %s", resp.StatusCode, url, string(respBytes))
	}

	var embResp EmbeddingResponse
	if err := json.Unmarshal(respBytes, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if embResp.Error != nil {
		return nil, fmt.Errorf("API error: %s (%s)", embResp.Error.Message, embResp.Error.Type)
	}

	result := make([][]float32, len(embResp.Data))
	for _, d := range embResp.Data {
		if d.Index >= len(result) {
			continue
		}
		result[d.Index] = d.Embedding
	}

	return result, nil
}
