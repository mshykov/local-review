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
	"net/url"
	"strings"
	"time"
)

// isLocalURL returns true when raw is a URL whose host points at the
// local machine (localhost, 127.0.0.0/8, ::1, 0.0.0.0). Used to skip
// the API-key requirement for Ollama / vLLM-style local-only setups
// where the provider doesn't authenticate. Any non-local URL falls
// through to the regular auth check.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	// Also catch any 127.x.x.x — kept narrow on purpose; refusing
	// to extend to RFC1918 ranges because a corporate API gateway
	// at 10.0.0.5 still authenticates and shouldn't bypass the check.
	return strings.HasPrefix(host, "127.")
}

// maxResponseBytes caps how much we'll read from a chat-completions
// response. A typical review response is well under 100 KB; 10 MB is
// generous headroom for an unusually verbose model and small enough
// to crash the CLI with a clear error rather than exhaust RAM if a
// provider streams unbounded data, hangs mid-transmission, or is
// spoofed by a man-in-the-middle on the configured base URL.
const maxResponseBytes = 10 * 1024 * 1024

// Client wraps a base URL + API key. Construct once, reuse.
type Client struct {
	BaseURL string
	APIKey  string
	// APIKeyEnv is the env var the key was sourced from; used in the empty-key error.
	APIKeyEnv string
	Model     string
	HTTP      *http.Client
}

// New returns a Client with a sensible default timeout.
func New(baseURL, apiKey, apiKeyEnv, model string, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		APIKeyEnv: apiKeyEnv,
		Model:     model,
		HTTP:      &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
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
	if c.APIKey == "" && !isLocalURL(c.BaseURL) {
		// Local-review init's "Ollama" preset writes a config with no
		// api_key_env line because Ollama doesn't authenticate. A blank
		// LOCAL_REVIEW_API_KEY (the legacy default) used to crash the
		// review here despite the provider needing no key. Skip the
		// check when base_url points at localhost/127.0.0.1/::1/0.0.0.0;
		// any non-local URL still requires a key.
		envName := c.APIKeyEnv
		if envName == "" {
			envName = "LOCAL_REVIEW_API_KEY"
		}
		return "", fmt.Errorf("no API key: $%s is unset or empty\n         run `local-review init` to set up a provider, or `export %s=...`\n         or run `local-review review` if you have LLM CLIs installed (see `local-review doctor`)", envName, envName)
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
	if c.APIKey != "" {
		// Only set Authorization when we actually have a key. Sending
		// `Authorization: Bearer ` with an empty token breaks some
		// local OpenAI-compatible servers (Ollama, vLLM) that expect
		// the header to be absent in unauthenticated mode. The empty-
		// key case is reachable via the isLocalURL bypass above.
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	// LimitReader at maxResponseBytes+1 so we can distinguish "exactly
	// at the cap" from "ran past the cap" — if we read >max bytes the
	// provider returned more than we'll trust.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if int64(len(respBody)) > maxResponseBytes {
		return "", fmt.Errorf("llm %s: response exceeded %d-byte cap (possible runaway provider or spoofed endpoint)", c.BaseURL, maxResponseBytes)
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
