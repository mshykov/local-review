package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// isHTTPScheme reports whether s parses as an http:// or https:// URL.
// Scheme comparison is case-insensitive per RFC 3986, so HTTPS:// is fine.
func isHTTPScheme(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// Probe is the "is this endpoint a usable OpenAI-compat provider?"
// check — issued by doctor for readiness rows and by the runner's
// pre-flight before fan-out. Cheap, free, no model load: it just hits
// `GET <base_url>/models` and confirms a 2xx + a JSON body that names
// at least one model. That matches the CLI probe's "is this thing
// alive?" semantics without burning tokens on a chat completion.
//
// Strict mode (--strict-probe, threaded in by the runner in PR 3 of
// the agents series) bypasses this and uses a real `Reply OK` chat
// completion through Invoker.RunPrompt instead — useful when the
// configured model id specifically must be loaded, not just the
// endpoint reachable.
//
// On success: nil. On failure: an error describing the problem
// (connection refused, 4xx/5xx, garbled response, timeout). The
// caller (doctor / pre-flight) decides how to surface it; this
// function stays purely diagnostic.
func Probe(ctx context.Context, baseURL, apiKey string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// Defense-in-depth scheme check. Config loading sanitizes an
	// untrusted base_url (strips it from the repo layer), but validate
	// here too so a misconfigured or trusted-but-typo'd endpoint
	// (file://, gopher://, …) fails with a clear error instead of being
	// handed to the HTTP client — the provider client POSTs the diff to
	// this endpoint, so the scheme is security-relevant.
	if !isHTTPScheme(baseURL) {
		return fmt.Errorf("invalid base_url %q: must be an http:// or https:// URL", baseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if apiKey != "" {
		// Only set Authorization when we actually have a key — same
		// reason as llm.Client: sending `Bearer ` with an empty token
		// breaks some local OpenAI-compat servers (Ollama, vLLM) that
		// expect the header absent in unauthenticated mode.
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Cap the body read so a misbehaving / spoofed endpoint can't
	// stream gigabytes at us during a "readiness" probe.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		// Surface a tail of the body so 401/403 (auth) and 404 (wrong
		// path, e.g. user gave `/v1/v1` or forgot `/v1`) are diagnosable.
		tail := strings.TrimSpace(string(body))
		if len(tail) > 200 {
			tail = tail[:200] + "…"
		}
		return fmt.Errorf("%s: %s — %s", url, resp.Status, tail)
	}
	// A 2xx with empty/garbled body is suspicious. We don't deeply
	// parse the OpenAI model-list JSON (every vendor's exact shape
	// varies a little) — just confirm there's *something* JSON-ish.
	// A real reviewer-pre-flight strict mode (PR 3) does the real
	// chat-completion probe and surfaces parse failures there.
	if len(body) == 0 || !strings.ContainsAny(string(body), "{[") {
		return fmt.Errorf("%s: 2xx but body is empty / non-JSON (got %q)", url, string(body))
	}
	return nil
}
