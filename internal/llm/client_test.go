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
	if !strings.Contains(msg, "local-review review") {
		t.Errorf("error should suggest `local-review review` as alternative, got: %v", err)
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

func TestComplete_LocalURLOmitsAuthorizationHeaderOnEmptyKey(t *testing.T) {
	// Pre-fix: even with an empty key (the local-URL bypass), we
	// still set `Authorization: Bearer ` on the outgoing request.
	// Some local OpenAI-compat servers (Ollama, vLLM) reject or
	// log-spam on an empty bearer token; the absent-header form is
	// what they expect for unauthenticated mode.
	m := newMockServer(t, okResponse("ok"))
	c := New(m.server.URL, "", "LOCAL_REVIEW_API_KEY", "ollama-model", 5)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.lastRequest.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header should be absent on empty key, got %q", got)
	}
}

func TestComplete_NonEmptyKeyStillSetsAuthorizationHeader(t *testing.T) {
	// Sanity: the header conditional must not regress the normal path.
	m := newMockServer(t, okResponse("ok"))
	c := New(m.server.URL, "sk-real", "LOCAL_REVIEW_API_KEY", "gpt-4", 5)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := m.lastRequest.Header.Get("Authorization"), "Bearer sk-real"; got != want {
		t.Errorf("Authorization: want %q, got %q", want, got)
	}
}

func TestComplete_LocalURLSkipsKeyRequirement(t *testing.T) {
	// Ollama / vLLM at localhost don't authenticate; the init wizard's
	// Ollama preset deliberately omits api_key_env. Pre-fix we still
	// rejected with "no API key" for those URLs. Now: localhost-shaped
	// base_url + empty key proceeds to the actual call (which the test
	// mock satisfies).
	m := newMockServer(t, okResponse("ok-local"))
	// Replace the mock URL's host with 127.0.0.1 explicitly to exercise
	// the local-host detector. httptest already binds there.
	c := New(m.server.URL, "", "LOCAL_REVIEW_API_KEY", "ollama-model", 5)
	got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err != nil {
		t.Fatalf("local URL with empty key should succeed, got %v", err)
	}
	if got != "ok-local" {
		t.Errorf("response: got %q, want ok-local", got)
	}
}

func TestIsLocalURL(t *testing.T) {
	// v0.10.4 widened the bypass to RFC1918 LAN ranges so users
	// running Ollama on a LAN server (e.g. `provider.base_url:
	// http://192.168.1.50:11434/v1`) don't have to set a dummy
	// api_key. Auth still fires when api_key IS explicitly
	// configured — see the c.APIKey != "" guard in Complete; this
	// helper only gates the "no key set + non-local URL = error"
	// path.
	cases := []struct {
		in   string
		want bool
		why  string
	}{
		// Loopback + named-local — pre-v0.10.4 baseline.
		{"http://localhost:11434/v1", true, "named-localhost"},
		{"http://127.0.0.1:8080/v1", true, "ipv4-loopback"},
		{"http://127.99.0.1/v1", true, "ipv4-loopback-alias-127.0.0.0_8"},
		{"http://[::1]:8000/v1", true, "ipv6-loopback"},
		{"http://0.0.0.0:11434/v1", true, "ipv4-wildcard-documented-local"},

		// Private LAN — RFC1918, the v0.10.4 widening.
		{"http://10.0.0.5:8080/v1", true, "rfc1918-10_8"},
		{"http://10.255.255.254/v1", true, "rfc1918-10_8-upper-edge"},
		{"http://172.16.0.1/v1", true, "rfc1918-172.16_12-lower-edge"},
		{"http://172.31.255.254/v1", true, "rfc1918-172.16_12-upper-edge"},
		{"http://192.168.1.10/v1", true, "rfc1918-192.168_16"},
		{"http://192.168.1.50:11434/v1", true, "ollama-on-lan-canonical"},

		// Edges of 172.16/12 — must NOT match outside the /12 window.
		{"http://172.15.255.254/v1", false, "172.15-outside-12"},
		{"http://172.32.0.1/v1", false, "172.32-outside-12"},

		// Link-local APIPA + IPv6 unique-local + link-local.
		{"http://169.254.1.1/v1", true, "ipv4-link-local-169.254_16"},
		{"http://[fd00::1]:11434/v1", true, "ipv6-unique-local-fc00_7"},
		{"http://[fe80::1]:11434/v1", true, "ipv6-link-local-fe80_10"},

		// Public — must require auth.
		{"https://api.openai.com/v1", false, "public-fqdn-openai"},
		{"https://api.anthropic.com/v1", false, "public-fqdn-anthropic"},
		{"http://8.8.8.8/v1", false, "public-ipv4"},
		{"http://[2001:db8::1]/v1", false, "ipv6-documentation-block-treated-remote"},

		// Empty / malformed / mDNS — fail closed (require auth).
		{"", false, "empty"},
		{"::not a url::", false, "garbage"},
		{"http://corp.local/v1", false, "mDNS-.local-can-resolve-anywhere-require-auth"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			if got := isLocalURL(tc.in); got != tc.want {
				t.Errorf("isLocalURL(%q) = %v, want %v (%s)", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

func TestComplete_RejectsOversizedResponse(t *testing.T) {
	// A spoofed/runaway provider must not OOM the CLI by streaming
	// unbounded bytes. The cap is 10 MB; write 12 MB and confirm we
	// get a clear error instead of silently buffering 12 MB of garbage.
	m := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 12 MB of `x` — well past the 10 MB cap.
		// errcheck/linter cleanliness: the write may short-circuit when
		// the client side detects the cap and closes mid-stream, which
		// is the whole point of this test. Discard the return.
		_, _ = w.Write(make([]byte, 12*1024*1024))
	})
	c := New(m.server.URL, "sk-x", "LOCAL_REVIEW_API_KEY", "gpt-4", 30)

	_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "x"}}, false)
	if err == nil {
		t.Fatal("expected oversize error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error should mention size cap, got: %v", err)
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
