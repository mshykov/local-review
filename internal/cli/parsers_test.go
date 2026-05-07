package cli

import (
	"strings"
	"testing"
)

func TestParseClaudeJSON_Success(t *testing.T) {
	// Anthropic's documented shape from `claude --output-format json`:
	// see https://docs.anthropic.com/en/docs/claude-code/sdk
	body := []byte(`{"type":"result","subtype":"success","result":"# Review\n## Major\n- bug","usage":{"input_tokens":12300,"output_tokens":4500,"cache_read_input_tokens":2000}}`)
	text, usage := parseClaudeJSON(body)
	if !strings.Contains(text, "# Review") {
		t.Errorf("text not extracted: %q", text)
	}
	if usage.InputTokens != 12300 {
		t.Errorf("InputTokens = %d, want 12300", usage.InputTokens)
	}
	if usage.OutputTokens != 4500 {
		t.Errorf("OutputTokens = %d, want 4500", usage.OutputTokens)
	}
}

func TestParseClaudeJSON_FallbackOnInvalidJSON(t *testing.T) {
	// If we somehow get plain text instead of JSON (a future CLI
	// schema change that drops --output-format json without
	// erroring, say), don't lose the review — pass it through as
	// text with zero usage. This is NOT a fallback path for older
	// CLIs lacking the flag; those exit non-zero and never reach
	// this parser. See ClaudeInvoker.run.
	plain := []byte("# Review\n## Major\n- bug\n")
	text, usage := parseClaudeJSON(plain)
	if text != string(plain) {
		t.Errorf("plain text not preserved: %q", text)
	}
	if !usage.IsZero() {
		t.Errorf("expected zero usage on plain-text fallback, got %+v", usage)
	}
}

func TestParseClaudeJSON_FallbackOnMissingResult(t *testing.T) {
	// Valid JSON but unexpected shape (e.g. an error envelope or a
	// future schema). Treat as opaque — return raw, zero usage.
	body := []byte(`{"type":"error","message":"oops"}`)
	text, usage := parseClaudeJSON(body)
	if text != string(body) {
		t.Errorf("unexpected JSON should pass through as text, got %q", text)
	}
	if !usage.IsZero() {
		t.Errorf("expected zero usage on unrecognised shape, got %+v", usage)
	}
}

func TestParseClaudeJSON_EmptyResultPreservesUsage(t *testing.T) {
	body := []byte(`{"type":"result","subtype":"success","result":"","usage":{"input_tokens":1200,"output_tokens":300}}`)
	text, usage := parseClaudeJSON(body)
	if text != "" {
		t.Errorf("empty result should stay empty, got %q", text)
	}
	if usage.InputTokens != 1200 || usage.OutputTokens != 300 {
		t.Errorf("usage = %+v, want {1200, 300}", usage)
	}
}

func TestParseGeminiJSON_ShapeA(t *testing.T) {
	// Newer gemini-cli structure with stats.models.<id>.tokens
	body := []byte(`{"response":"# Review\n- finding","stats":{"models":{"gemini-2.5-pro":{"tokens":{"prompt":15000,"candidates":3000}}}}}`)
	text, usage := parseGeminiJSON(body)
	if !strings.Contains(text, "# Review") {
		t.Errorf("text not extracted: %q", text)
	}
	if usage.InputTokens != 15000 {
		t.Errorf("InputTokens = %d, want 15000", usage.InputTokens)
	}
	if usage.OutputTokens != 3000 {
		t.Errorf("OutputTokens = %d, want 3000", usage.OutputTokens)
	}
}

func TestParseGeminiJSON_ShapeA_EmptyResponsePreservesUsage(t *testing.T) {
	body := []byte(`{"response":"","stats":{"models":{"gemini-2.5-pro":{"tokens":{"prompt":15000,"candidates":3000}}}}}`)
	text, usage := parseGeminiJSON(body)
	if text != "" {
		t.Errorf("empty response should stay empty, got %q", text)
	}
	if usage.InputTokens != 15000 || usage.OutputTokens != 3000 {
		t.Errorf("usage = %+v, want {15000, 3000}", usage)
	}
}

func TestParseGeminiJSON_ShapeB(t *testing.T) {
	// Older Vertex-style usageMetadata shape
	body := []byte(`{"text":"# Review","usageMetadata":{"promptTokenCount":15000,"candidatesTokenCount":3000}}`)
	text, usage := parseGeminiJSON(body)
	if text != "# Review" {
		t.Errorf("text not extracted: %q", text)
	}
	if usage.InputTokens != 15000 || usage.OutputTokens != 3000 {
		t.Errorf("usage = %+v, want {15000, 3000}", usage)
	}
}

func TestParseGeminiJSON_ShapeB_EmptyTextPreservesUsage(t *testing.T) {
	body := []byte(`{"text":"","usageMetadata":{"promptTokenCount":15000,"candidatesTokenCount":3000}}`)
	text, usage := parseGeminiJSON(body)
	if text != "" {
		t.Errorf("empty text should stay empty, got %q", text)
	}
	if usage.InputTokens != 15000 || usage.OutputTokens != 3000 {
		t.Errorf("usage = %+v, want {15000, 3000}", usage)
	}
}

func TestParseGeminiJSON_FallbackOnUnknownShape(t *testing.T) {
	// Unknown JSON shape — neither A nor B. Fall back to raw text +
	// zero usage. The user gets the review (whatever's there); they
	// just lose token visibility for this run.
	body := []byte(`{"weird":"shape"}`)
	text, usage := parseGeminiJSON(body)
	if text != string(body) {
		t.Errorf("unknown shape should pass through, got %q", text)
	}
	if !usage.IsZero() {
		t.Errorf("expected zero usage on unknown shape, got %+v", usage)
	}
}

func TestParseGeminiJSON_FallbackOnPlainText(t *testing.T) {
	// If we ever see plain text instead of JSON (schema drift, wrapper,
	// etc.), preserve the review text and just lose token visibility.
	plain := []byte("# Review\n- finding\n")
	text, usage := parseGeminiJSON(plain)
	if text != string(plain) {
		t.Errorf("plain text not preserved: %q", text)
	}
	if !usage.IsZero() {
		t.Errorf("expected zero usage on plain-text fallback, got %+v", usage)
	}
}

func TestParseCodexStdoutTokens_SplitShape(t *testing.T) {
	// codex v0.128+: "tokens: <in> input, <out> output"
	stdout := `[2026-05-07T12:00:00Z] OpenAI Codex v0.128.0
[2026-05-07T12:00:01Z] running review
[2026-05-07T12:00:42Z] tokens: 12,345 input, 6,789 output
[2026-05-07T12:00:42Z] session complete`
	usage := parseCodexStdoutTokens(stdout)
	if usage.InputTokens != 12345 {
		t.Errorf("InputTokens = %d, want 12345", usage.InputTokens)
	}
	if usage.OutputTokens != 6789 {
		t.Errorf("OutputTokens = %d, want 6789", usage.OutputTokens)
	}
}

func TestParseCodexStdoutTokens_LegacyTotalShape(t *testing.T) {
	// Older codex: "tokens used: <total>" (no input/output split).
	// We fold the total into InputTokens (so Total() math works)
	// AND set TotalOnly so the formatter renders "Nk total" rather
	// than the misleading "Nk in / 0 out" — the model produced
	// output, we just don't know how much.
	stdout := `[2026-05-04T12:00:00Z] tokens used: 18000
[2026-05-04T12:00:00Z] done`
	usage := parseCodexStdoutTokens(stdout)
	if usage.InputTokens != 18000 {
		t.Errorf("InputTokens = %d, want 18000 (legacy total folded here)", usage.InputTokens)
	}
	if usage.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (legacy can't attribute)", usage.OutputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true so formatter renders 'total' not 'in/out'")
	}
	if usage.Total() != 18000 {
		t.Errorf("Total() = %d, want 18000 (sum should still work for aggregation)", usage.Total())
	}
}

func TestParseCodexStdoutTokens_SplitShapeNotTotalOnly(t *testing.T) {
	// Modern codex with split shape must NOT set TotalOnly — we
	// have real input vs output numbers and the formatter should
	// render "Nk in / Mk out" honestly.
	stdout := "tokens: 100 input, 50 output"
	usage := parseCodexStdoutTokens(stdout)
	if usage.TotalOnly {
		t.Errorf("split-shape codex output should not set TotalOnly: got %+v", usage)
	}
}

func TestParseCodexStdoutTokens_NoMatch(t *testing.T) {
	// Future codex version drops the line entirely. Better to
	// return zero usage than to fabricate.
	stdout := "no metadata at all"
	usage := parseCodexStdoutTokens(stdout)
	if !usage.IsZero() {
		t.Errorf("expected zero on no-match, got %+v", usage)
	}
}

func TestAtoiNoCommas(t *testing.T) {
	cases := map[string]int{
		"":          0,
		"123":       123,
		"12,345":    12345,
		"1,234,567": 1234567,
		"abc":       0, // parse failure → 0, not panic
	}
	for in, want := range cases {
		if got := atoiNoCommas(in); got != want {
			t.Errorf("atoiNoCommas(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTokenUsage_IsZero(t *testing.T) {
	cases := []struct {
		name  string
		usage TokenUsage
		want  bool
	}{
		{"both zero", TokenUsage{InputTokens: 0, OutputTokens: 0}, true},
		{"input only", TokenUsage{InputTokens: 100, OutputTokens: 0}, false},
		{"output only", TokenUsage{InputTokens: 0, OutputTokens: 100}, false},
		{"both nonzero", TokenUsage{InputTokens: 100, OutputTokens: 200}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.IsZero(); got != tc.want {
				t.Errorf("IsZero() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTokenUsage_Total(t *testing.T) {
	u := TokenUsage{InputTokens: 100, OutputTokens: 200}
	if got := u.Total(); got != 300 {
		t.Errorf("Total() = %d, want 300", got)
	}
}
