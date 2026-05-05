package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNew_DefaultTimeout(t *testing.T) {
	c := New("https://api.example.com", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 0)
	if c.HTTP.Timeout != 60*time.Second {
		t.Errorf("default timeout: want 60s, got %v", c.HTTP.Timeout)
	}

	c = New("https://api.example.com", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", -5)
	if c.HTTP.Timeout != 60*time.Second {
		t.Errorf("negative timeout should fall back to 60s, got %v", c.HTTP.Timeout)
	}
}

func TestNew_CustomTimeout(t *testing.T) {
	c := New("https://api.example.com", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 30)
	if c.HTTP.Timeout != 30*time.Second {
		t.Errorf("custom timeout: want 30s, got %v", c.HTTP.Timeout)
	}
}

func TestNew_StripsTrailingSlashFromBaseURL(t *testing.T) {
	c := New("https://api.example.com/v1/", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 0)
	if c.BaseURL != "https://api.example.com/v1" {
		t.Errorf("trailing slash not stripped: got %q", c.BaseURL)
	}
	c = New("https://api.example.com/v1", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 0)
	if c.BaseURL != "https://api.example.com/v1" {
		t.Errorf("clean URL altered: got %q", c.BaseURL)
	}
}

func TestComplete_NoAPIKeyReturnsError(t *testing.T) {
	// The error must (a) name the configured env var so users see the
	// right knob to set, and (b) point at `multi` mode as an alternative
	// for users who only have CLI auth.
	c := New("https://api.example.com", "", "OPENAI_API_KEY", "gpt-4", 0)
	_, err := c.Complete(context.Background(), nil, false)
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no API key") {
		t.Errorf("missing 'no API key' phrase: %v", err)
	}
	if !strings.Contains(msg, "OPENAI_API_KEY") {
		t.Errorf("error should name the configured env var, got: %v", err)
	}
	if !strings.Contains(msg, "local-review init") {
		t.Errorf("error should suggest `local-review init`, got: %v", err)
	}
	if !strings.Contains(msg, "local-review multi") {
		t.Errorf("error should suggest `local-review multi` as alternative, got: %v", err)
	}
}

func TestComplete_NoAPIKeyFallsBackToLegacyEnvName(t *testing.T) {
	// When APIKeyEnv is empty (e.g., older callers or buggy config),
	// the error should fall back to the legacy default rather than
	// printing "$" with no name.
	c := New("https://api.example.com", "", "", "gpt-4", 0)
	_, err := c.Complete(context.Background(), nil, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "LOCAL_REVIEW_API_KEY") {
		t.Errorf("empty APIKeyEnv should fall back to LOCAL_REVIEW_API_KEY, got: %v", err)
	}
}

// mockServer captures the last request received by the test server so
// tests can inspect headers/body. Single-request use only — the
// last* fields aren't synchronized for concurrent reads.
type mockServer struct {
	server      *httptest.Server
	lastRequest *http.Request
	lastBody    []byte
}

func newMockServer(t *testing.T, handler http.HandlerFunc) *mockServer {
	t.Helper()
	m := &mockServer{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		m.lastBody = body
		m.lastRequest = r
		handler(w, r)
	}))
	t.Cleanup(m.server.Close)
	return m
}

// Standard happy-path response handler. Encodes content via %q so
// callers can pass arbitrary strings (quotes, backslashes, newlines).
func okResponse(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, content)
	}
}

func TestComplete_SendsExpectedRequestShape(t *testing.T) {
	m := newMockServer(t, okResponse("hello"))
	c := New(m.server.URL, "sk-test", "LOCAL_REVIEW_API_KEY", "gpt-4o-mini", 5)

	got, err := c.Complete(context.Background(), []Message{
		{Role: "system", Content: "you are a reviewer"},
		{Role: "user", Content: "review this"},
	}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("content: want %q, got %q", "hello", got)
	}

	// URL: should append /chat/completions
	if !strings.HasSuffix(m.lastRequest.URL.Path, "/chat/completions") {
		t.Errorf("path: want suffix /chat/completions, got %q", m.lastRequest.URL.Path)
	}

	// Headers
	if m.lastRequest.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: %q", m.lastRequest.Header.Get("Content-Type"))
	}
	if got, want := m.lastRequest.Header.Get("Authorization"), "Bearer sk-test"; got != want {
		t.Errorf("Authorization: want %q, got %q", want, got)
	}

	// Body shape
	var body map[string]any
	if err := json.Unmarshal(m.lastBody, &body); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if body["model"] != "gpt-4o-mini" {
		t.Errorf("model: %v", body["model"])
	}
	// float32 → JSON → float64 round-trip can drift in low bits; compare
	// with tolerance instead of exact equality.
	if temp, ok := body["temperature"].(float64); !ok || math.Abs(temp-0.2) > 1e-6 {
		t.Errorf("temperature: want ~0.2 for review consistency, got %v", body["temperature"])
	}
	if _, present := body["response_format"]; present {
		t.Error("response_format should be omitted when jsonMode=false")
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages: want 2, got %v", body["messages"])
	}
}

func TestComplete_JSONModeIncludesResponseFormat(t *testing.T) {
	m := newMockServer(t, okResponse("{}"))
	c := New(m.server.URL, "sk-test", "LOCAL_REVIEW_API_KEY", "gpt-4o-mini", 5)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(m.lastBody, &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing/wrong type: %v", body["response_format"])
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type: want json_object, got %v", rf["type"])
	}
}

func TestComplete_4xxWithJSONErrorEnvelope(t *testing.T) {
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"auth_error"}}`))
	})
	c := New(m.server.URL, "sk-bad", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error should surface server message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include status, got: %v", err)
	}
}

func TestComplete_4xxWithNonJSONBody(t *testing.T) {
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timeout"))
	})
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected error for 502, got nil")
	}
	// Should fall back to raw body when no JSON envelope
	if !strings.Contains(err.Error(), "upstream timeout") {
		t.Errorf("error should include raw body, got: %v", err)
	}
}

func TestComplete_EmptyChoicesReturnsError(t *testing.T) {
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	})
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComplete_MalformedJSONResponse(t *testing.T) {
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	})
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComplete_RespectsContextCancellation(t *testing.T) {
	// Server intentionally slow so the context fires first.
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{}`))
		}
	})
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 30)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Complete(ctx, []Message{{Role: "user", Content: "x"}}, false)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	// Tight enough to catch real cancellation regressions, loose enough
	// to not flake on slow CI runners.
	if elapsed > 300*time.Millisecond {
		t.Errorf("Complete didn't honor ctx cancellation promptly; took %v", elapsed)
	}
}

// errRoundTripper is a stub transport that always returns a fixed error,
// giving tests a deterministic network failure without depending on any
// particular port being unbound.
type errRoundTripper struct{ err error }

func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, e.err
}

func TestComplete_NetworkErrorIncludesURL(t *testing.T) {
	c := New("http://127.0.0.1:9999", "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)
	// Inject a stub transport so the failure is deterministic and does not
	// depend on whether port 9999 happens to be unbound in the test environment.
	c.HTTP.Transport = &errRoundTripper{err: errors.New("connection refused")}

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:9999") {
		t.Errorf("error should include base URL for debugging, got: %v", err)
	}
}

func TestComplete_BodyContainsMessages(t *testing.T) {
	m := newMockServer(t, okResponse("ok"))
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)

	msgs := []Message{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hello"},
	}
	_, err := c.Complete(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(m.lastBody, &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(body.Messages) != 2 {
		t.Fatalf("messages: want 2, got %d", len(body.Messages))
	}
	if body.Messages[0].Role != "system" || body.Messages[0].Content != "be terse" {
		t.Errorf("system message wrong: %+v", body.Messages[0])
	}
	if body.Messages[1].Role != "user" || body.Messages[1].Content != "hello" {
		t.Errorf("user message wrong: %+v", body.Messages[1])
	}
}
