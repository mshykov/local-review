package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer returns a chat-completions mock that echoes a fixed
// reply + usage. Captures the last request body so individual tests
// can assert on the wire shape.
func fakeServer(t *testing.T, content string, promptToks, completionToks int) (*httptest.Server, *[]byte) {
	t.Helper()
	var lastBody []byte
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read req body: %v", err)
		}
		lastBody = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"choices":[{"message":{"role":"assistant","content":%q}}],
			"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}
		}`, content, promptToks, completionToks, promptToks+completionToks)
	}))
	t.Cleanup(s.Close)
	return s, &lastBody
}

// Review pins the wire-shape contract: a system+user message pair,
// no response_format header (the prompt drives output), correct
// model id forwarded. This is the canonical multi-LLM call shape.
func TestInvoker_Review_SendsSystemAndUserMessagesWithoutJSONMode(t *testing.T) {
	s, body := fakeServer(t, "## Review\n- bug found", 1200, 80)
	p := New("qwen", s.URL, "", "", "qwen2.5-coder:7b", 5)

	text, usage, err := p.Review(context.Background(), "you are a reviewer", "diff --git a/foo.go ...")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(text, "bug found") {
		t.Errorf("text not returned verbatim: %q", text)
	}
	if usage.InputTokens != 1200 || usage.OutputTokens != 80 {
		t.Errorf("usage mapping wrong: got %+v, want {In:1200, Out:80}", usage)
	}

	var req map[string]any
	if err := json.Unmarshal(*body, &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if req["model"] != "qwen2.5-coder:7b" {
		t.Errorf("model not forwarded: %v", req["model"])
	}
	if _, present := req["response_format"]; present {
		t.Error("response_format must NOT be set — the prompt drives output format, not the wire header (avoids contradicting the multi-LLM markdown override)")
	}
	msgs, ok := req["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages: want 2 (system+user), got %v", req["messages"])
	}
	if m, _ := msgs[0].(map[string]any); m["role"] != "system" {
		t.Errorf("first msg role: want system, got %v", m["role"])
	}
	if m, _ := msgs[1].(map[string]any); m["role"] != "user" {
		t.Errorf("second msg role: want user, got %v", m["role"])
	}
}

// RunPrompt is the merger / audit path: a single user message, no
// system prompt. Used when the caller has already built the full
// prompt and just wants the model's reply.
func TestInvoker_RunPrompt_SendsSingleUserMessage(t *testing.T) {
	s, body := fakeServer(t, "OK", 5, 1)
	p := New("qwen", s.URL, "", "", "qwen2.5-coder:7b", 5)

	text, _, err := p.RunPrompt(context.Background(), "Reply OK")
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if text != "OK" {
		t.Errorf("text: want OK, got %q", text)
	}

	var req map[string]any
	if err := json.Unmarshal(*body, &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if m, _ := msgs[0].(map[string]any); m["role"] != "user" {
		t.Errorf("RunPrompt should send a single user message; got role=%v", m["role"])
	}
}

// A provider that omits the `usage` object (some partial / older
// OpenAI-compat implementations) must degrade to zero tokens, NOT
// fail the call — same contract as the CLI invokers when their
// structured output is missing.
func TestInvoker_MissingUsageDegradesToZeroNotError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No "usage" field.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	t.Cleanup(s.Close)
	p := New("qwen", s.URL, "", "", "qwen2.5-coder:7b", 5)

	text, usage, err := p.Review(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("missing usage should not error, got: %v", err)
	}
	if text != "hi" {
		t.Errorf("text: want hi, got %q", text)
	}
	if !usage.IsZero() {
		t.Errorf("missing usage should map to zero TokenUsage, got %+v", usage)
	}
}

// Some providers return only `total_tokens` and leave the split
// fields zero (older Ollama, partial OpenAI-compat). The invoker must
// fold the total into InputTokens with TotalOnly=true so the user
// still sees a usable count — same pattern agents.TokenUsage already
// supports for codex pre-v0.128.
func TestInvoker_TotalOnlyUsageMapsWithTotalOnlyFlag(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// total_tokens only; prompt_tokens / completion_tokens omitted.
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"hi"}}],
			"usage":{"total_tokens":4096}
		}`))
	}))
	t.Cleanup(s.Close)
	p := New("qwen", s.URL, "", "", "qwen2.5-coder:7b", 5)

	_, usage, err := p.Review(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !usage.TotalOnly {
		t.Errorf("total-only usage must set TotalOnly=true; got %+v", usage)
	}
	if usage.InputTokens != 4096 {
		t.Errorf("total should be folded into InputTokens; got InputTokens=%d (want 4096)", usage.InputTokens)
	}
	if usage.OutputTokens != 0 {
		t.Errorf("OutputTokens must stay 0 when only total is known; got %d", usage.OutputTokens)
	}
}

// Provider HTTP failures must surface with the agent name prefix so a
// multi-provider roster line attributes correctly (e.g.
// `qwen ✗ <reason>` not just `✗ <reason>`).
func TestInvoker_ErrorIncludesAgentName(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"backend down"}}`))
	}))
	t.Cleanup(s.Close)
	p := New("deepseek", s.URL, "", "", "deepseek-coder-v2:16b", 5)

	_, _, err := p.Review(context.Background(), "sys", "diff")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "deepseek") {
		t.Errorf("error must carry agent name for attribution: %v", err)
	}
}

// Output is trimmed — providers commonly return a trailing newline,
// and the rest of the pipeline (formatters, persisters) prefers a
// clean string.
func TestInvoker_TrimsTrailingWhitespace(t *testing.T) {
	s, _ := fakeServer(t, "  hello world  \n\n", 1, 1)
	p := New("qwen", s.URL, "", "", "m", 5)
	text, _, err := p.Review(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Errorf("want trimmed 'hello world', got %q", text)
	}
}
