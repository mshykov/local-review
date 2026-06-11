package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Happy path — a /v1/models endpoint returning the canonical
// OpenAI-style envelope. Probe should succeed and not consult auth.
func TestProbe_OK(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("probe must hit /models, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"qwen2.5-coder:7b"}]}`))
	}))
	t.Cleanup(s.Close)
	if err := Probe(context.Background(), s.URL, "", 2*time.Second); err != nil {
		t.Fatalf("Probe: %v", err)
	}
}

// Defense-in-depth: a non-http(s) base_url must be rejected before any
// request is built — never hand file:// / gopher:// to the HTTP client.
func TestProbe_RejectsNonHTTPScheme(t *testing.T) {
	for _, bad := range []string{"file:///etc/passwd", "gopher://host/1", "ftp://host", "/etc/passwd", "host:1234/v1"} {
		if err := Probe(context.Background(), bad, "", time.Second); err == nil {
			t.Errorf("Probe(%q) = nil, want scheme-validation error", bad)
		}
	}
}

// With an API key configured, the probe must send Authorization.
// Local Ollama doesn't care, but cloud providers reject without it.
func TestProbe_SendsAuthorizationWhenKeyProvided(t *testing.T) {
	var gotAuth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(s.Close)
	if err := Probe(context.Background(), s.URL, "sk-test", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization header: want %q, got %q", "Bearer sk-test", gotAuth)
	}
}

// Without an API key, the Authorization header must NOT be sent.
// Some local OpenAI-compat servers (older Ollama, vLLM) reject the
// bare `Bearer ` header — same reason llm.Client omits it when empty.
func TestProbe_OmitsAuthorizationWhenNoKey(t *testing.T) {
	var gotAuth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(s.Close)
	if err := Probe(context.Background(), s.URL, "", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization must be absent when no key configured; got %q", gotAuth)
	}
}

// 4xx must surface a usable diagnostic (status + body tail) so the
// user sees WHY it failed — typical causes: wrong path (no /v1
// prefix), wrong key, model gating.
func TestProbe_Returns4xxWithBodyTail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid_api_key"}}`))
	}))
	t.Cleanup(s.Close)
	err := Probe(context.Background(), s.URL, "sk-bad", 2*time.Second)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error must include the HTTP status: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_api_key") {
		t.Errorf("error must surface the body tail so the cause is diagnosable: %v", err)
	}
}

// A connection refused / unroutable host must fail clearly within the
// configured timeout, not hang the doctor.
func TestProbe_UnreachableHostErrors(t *testing.T) {
	// 127.0.0.1:1 is reliably refused (no listener); fast fail expected.
	start := time.Now()
	err := Probe(context.Background(), "http://127.0.0.1:1/v1", "", 2*time.Second)
	if err == nil {
		t.Fatal("expected error on unreachable host")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("probe took %v; should fail fast on refused connection", elapsed)
	}
}

// 2xx with empty body is suspicious — flag it instead of reporting
// "ready" on a partial / spoofed endpoint.
func TestProbe_2xxWithEmptyBodyRejected(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Empty body
	}))
	t.Cleanup(s.Close)
	err := Probe(context.Background(), s.URL, "", 2*time.Second)
	if err == nil {
		t.Fatal("2xx with empty body must NOT report ready — it's almost certainly a misconfigured proxy / WAF")
	}
}
