// Package llm is a minimal HTTP client for the OpenAI chat-completions
// API. We deliberately avoid pulling in any vendor SDK — every major
// provider (OpenAI, Anthropic via /v1/chat/completions, Together,
// Groq, OpenRouter, Ollama, vLLM) speaks this dialect.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps a base URL + API key. Construct once, reuse.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// New returns a Client with a sensible default timeout.
func New(baseURL, apiKey, model string, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// Message is one turn in the chat.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request body for /v1/chat/completions.
type request struct {
	Model          string       `json:"model"`
	Messages       []Message    `json:"messages"`
	ResponseFormat *responseFmt `json:"response_format,omitempty"`
	Temperature    float32      `json:"temperature,omitempty"`
}

type responseFmt struct {
	Type string `json:"type"` // "json_object"
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends a chat completion and returns the assistant's text.
//
// jsonMode requests structured JSON output via response_format. Most
// providers respect this; for those that don't, the system prompt
// should still steer the model to JSON.
func (c *Client) Complete(ctx context.Context, msgs []Message, jsonMode bool) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("no API key configured (set LOCAL_REVIEW_API_KEY or provider.api_key)")
	}

	req := request{
		Model:       c.Model,
		Messages:    msgs,
		Temperature: 0.2, // low temp for review consistency
	}
	if jsonMode {
		req.ResponseFormat = &responseFmt{Type: "json_object"}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to extract a useful message from the JSON error envelope
		var parsed response
		_ = json.Unmarshal(respBody, &parsed)
		if parsed.Error != nil {
			return "", fmt.Errorf("llm %s: %s", resp.Status, parsed.Error.Message)
		}
		return "", fmt.Errorf("llm %s: %s", resp.Status, string(respBody))
	}

	var out response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}
	return out.Choices[0].Message.Content, nil
}
