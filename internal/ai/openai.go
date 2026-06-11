package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps the OpenAI-compatible API.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the request body for the chat completions API.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// ChatResponse is the response from the chat completions API.
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// NewClient creates a new OpenAI-compatible client.
func NewClient(apiKey, baseURL, model string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Chat sends a chat completion request and returns the response.
func (c *Client) Chat(ctx context.Context, messages []Message, temperature float64, maxTokens int) (string, error) {
	reqBody := ChatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("API error: %s (%s)", chatResp.Error.Message, chatResp.Error.Type)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// ChatSimple is a convenience wrapper for single-turn chat.
func (c *Client) ChatSimple(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	return c.Chat(ctx, messages, 0.7, 2000)
}
