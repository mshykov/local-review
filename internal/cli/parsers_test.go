package cli

import (
	"strings"
	"testing"
)

func TestParseCopilotStderrTokens(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		wantIn   int
		wantOut  int
		wantZero bool
	}{
		{
			name:    "real summary line with k suffix and cached aside",
			stderr:  "\n\nChanges    +0 -0\nRequests   1 Premium (4s)\nTokens     ↑ 16.9k (1.5k cached) • ↓ 20 (13 reasoning)\n",
			wantIn:  16900,
			wantOut: 20,
		},
		{
			name:    "both sides plain integers",
			stderr:  "Tokens     ↑ 512 • ↓ 103\n",
			wantIn:  512,
			wantOut: 103,
		},
		{
			name:    "m suffix scales to millions",
			stderr:  "Tokens ↑ 1.2m ↓ 3.4k",
			wantIn:  1200000,
			wantOut: 3400,
		},
		{
			name:    "multiple token lines: last (cumulative) wins",
			stderr:  "Tokens ↑ 1.0k ↓ 5\nTokens ↑ 16.9k ↓ 149\n",
			wantIn:  16900,
			wantOut: 149,
		},
		{
			name:     "no tokens line degrades to zero",
			stderr:   "Requests 1 Premium (4s)\n",
			wantZero: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCopilotStderrTokens(tt.stderr)
			if tt.wantZero {
				if !got.IsZero() {
					t.Errorf("expected zero usage, got %+v", got)
				}
				return
			}
			if got.InputTokens != tt.wantIn || got.OutputTokens != tt.wantOut {
				t.Errorf("got in=%d out=%d, want in=%d out=%d", got.InputTokens, got.OutputTokens, tt.wantIn, tt.wantOut)
			}
		})
	}
}

func TestParseClaudeJSON_Success(t *testing.T) {
	// Anthropic's documented shape from `claude --output-format json`:
	// see https://docs.anthropic.com/en/docs/claude-code/sdk
	//
	// v0.7.1: cache_read + cache_creation tokens are summed into
	// InputTokens. Pre-fix the displayed "in" excluded both, so a
	// re-review of the same diff (almost everything served from
	// cache) collapsed to single-digit input — read as broken on a
	// real ~10k-token prompt.
	body := []byte(`{"type":"result","subtype":"success","result":"# Review\n## Major\n- bug","usage":{"input_tokens":12300,"output_tokens":4500,"cache_read_input_tokens":2000,"cache_creation_input_tokens":500}}`)
	text, usage := parseClaudeJSON(body)
	if !strings.Contains(text, "# Review") {
		t.Errorf("text not extracted: %q", text)
	}
	const wantIn = 12300 + 2000 + 500 // input + cache_read + cache_creation
	if usage.InputTokens != wantIn {
		t.Errorf("InputTokens = %d, want %d (input+cache_read+cache_creation)", usage.InputTokens, wantIn)
	}
	if usage.OutputTokens != 4500 {
		t.Errorf("OutputTokens = %d, want 4500", usage.OutputTokens)
	}
}

func TestParseClaudeJSON_HighCacheRatio(t *testing.T) {
	// The exact regression that prompted the v0.7.1 fix: a re-review
	// of the same diff. Almost the entire prompt is served from
	// cache — `input_tokens` (new spend) is in the single digits
	// while `cache_read_input_tokens` carries the full prompt size.
	// Pre-fix this rendered as "9 in / 5.2k out" which read as a
	// broken parser; post-fix the user sees "10k in / 5.2k out"
	// — the actual prompt size they sent.
	body := []byte(`{"type":"result","subtype":"success","result":"# Review","usage":{"input_tokens":9,"output_tokens":5200,"cache_read_input_tokens":10000,"cache_creation_input_tokens":0}}`)
	_, usage := parseClaudeJSON(body)
	const wantIn = 9 + 10000
	if usage.InputTokens != wantIn {
		t.Errorf("InputTokens = %d, want %d (cache_read should be summed in)", usage.InputTokens, wantIn)
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
	// Hypothetical/future "tokens: <in> input, <out> output" shape.
	// v0.128 doesn't actually emit this (see _NewlineShape test
	// below), but if/when codex starts splitting, we don't want
	// to lose the signal — pattern stays in the matcher chain.
	stdout := `[2026-05-07T12:00:00Z] OpenAI Codex v0.128.0
[2026-05-07T12:00:01Z] running review
[2026-05-07T12:00:42Z] tokens: 12,345 input, 6,789 output
[2026-05-07T12:00:42Z] session complete`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 12345 {
		t.Errorf("InputTokens = %d, want 12345", usage.InputTokens)
	}
	if usage.OutputTokens != 6789 {
		t.Errorf("OutputTokens = %d, want 6789", usage.OutputTokens)
	}
}

func TestParseCodexStdoutTokens_NewlineShape(t *testing.T) {
	// Real codex v0.128 stdout — verified by running `codex exec`
	// against the live binary on 2026-05-07. The "tokens used"
	// label and the number are on *separate lines*, no colon between.
	// Pre-v0.7.1 our regex required a colon and missed this entirely;
	// when it did "match" something, it was usually a context-window
	// indicator elsewhere in the banner ("Total tokens: 800") which
	// produced nonsense "800 total" output on real runs.
	stdout := `OpenAI Codex v0.128.0 (research preview)
--------
workdir: /Users/x/Projects/local-review
model: gpt-5.5
session id: 019e0266-9d5b-7551-821f-f3ce0d822c97
--------
codex
hello
tokens used
2,415
hello`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 2415 {
		t.Errorf("InputTokens = %d, want 2415 (newline-shape total)", usage.InputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true — newline shape gives no input/output split")
	}
}

func TestParseCodexStdoutTokens_PrefersLastMatch(t *testing.T) {
	// Same-pattern case: assistant prose contains "tokens used\n123"
	// (newline shape) before the real "tokens used\n2,415" summary.
	// Latest-position-wins picks the summary.
	stdout := `codex
The function reports "tokens used
123"
when the user has used 123 tokens.
tokens used
2,415
hello`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 2415 {
		t.Errorf("InputTokens = %d, want 2415 (latest match, not first)", usage.InputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true")
	}
}

func TestParseCodexStdoutTokens_CrossPatternPrecedence(t *testing.T) {
	// Cross-pattern case: assistant prose contains split-shape text
	// ("tokens: 100 input, 20 output") while the real summary is
	// newline-shape ("tokens used\n2,415"). Pre-fix the matcher
	// tried codexSplitRE first and returned on hit — so the
	// assistant's "100 input, 20 output" wins despite occurring
	// EARLIER in stdout than the real summary. Latest-position-
	// across-all-patterns fixes this: the summary at greater byte
	// offset beats the split-shape match at lower offset.
	stdout := `codex
The model output was: tokens: 100 input, 20 output
tokens used
2,415
hello`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 2415 {
		t.Errorf("InputTokens = %d, want 2415 (real summary), not %d (assistant split-shape)",
			usage.InputTokens, 100)
	}
	if usage.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (newline shape has no split)", usage.OutputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true (newline shape, not split)")
	}
}

func TestParseCodexStdoutTokens_StripsTrailingDuplicate(t *testing.T) {
	// The exact regression that prompted v0.7.2. Codex exec writes
	// the assistant reply, then the real "tokens used" summary, then
	// duplicates the reply at the very end (it's the same content
	// the --output-last-message tempfile holds). If the reply
	// contains pattern-shaped text — say, quoted from a test fixture
	// in the reviewed diff — the duplicated trailing copy outranks
	// the real summary via latest-position. Real-world hit: the
	// v0.7 audit run printed "codex ✓ · 100 in / 20 out" on a multi-
	// thousand-token diff because the reviewed code contained a
	// fixture string `tokens: 100 input, 20 output`.
	//
	// Fix: pass the response text to the parser so it can strip
	// the trailing duplicate before scanning. Real summary then
	// becomes the rightmost match again.
	response := `Here is my review.
Note: the test fixture says tokens: 100 input, 20 output but that's not real usage.`
	stdout := `codex
` + response + `
tokens used
54,321
` + response // codex's trailing duplicate
	usage := parseCodexStdoutTokens(stdout, response)
	if usage.InputTokens != 54321 {
		t.Errorf("InputTokens = %d, want 54321 (real summary), not 100 (fixture quote in trailing duplicate)", usage.InputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true (newline shape — codex v0.128 doesn't split)")
	}
}

func TestParseCodexStdoutTokens_StripIsNoOpWhenResponseEmpty(t *testing.T) {
	// Test fixtures hand-build stdout without a separate response
	// payload. Empty-response should skip the strip and not break
	// the existing test corpus. Pin the no-op contract.
	stdout := `tokens: 100 input, 50 output`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Errorf("got %+v, want {100, 50}", usage)
	}
}

func TestParseCodexStdoutTokens_StripIsNoOpWhenResponseNotASuffix(t *testing.T) {
	// Iteration-2 self-review caught a major bug in v0.7.2's first
	// stripTrailingDuplicate: when codex outputs the response only
	// ONCE (no trailing duplicate), LastIndex would still find the
	// streamed copy and cut everything after — including the real
	// summary. Fix: only strip when response IS the suffix of
	// combined (after trimming trailing whitespace).
	//
	// Stdout layout: <streamed reply> → <real summary>. No
	// duplicate at the end. Pre-fix this returned zero usage;
	// post-fix it returns the real summary.
	stdout := `codex
hello
tokens used
2,415`
	usage := parseCodexStdoutTokens(stdout, "hello")
	if usage.InputTokens != 2415 {
		t.Errorf("InputTokens = %d, want 2415 — strip should be no-op when response is not the trailing suffix", usage.InputTokens)
	}
	if !usage.TotalOnly {
		t.Errorf("TotalOnly = false, want true (newline shape)")
	}
}

func TestParseCodexStdoutTokens_StripIsNoOpWhenResponseAppearsOnce(t *testing.T) {
	// Iteration-3 self-review caught: the v0.7.2 second-try strip
	// (suffix-only) still over-stripped when the response equals
	// the entire end of stdout but appears only once. Example: a
	// future codex format that puts the reply at the end with no
	// streamed copy, and the only parseable token text happens to
	// be inside that reply. Pre-fix would strip the reply and lose
	// the only candidate. Post-fix: require an earlier occurrence
	// before stripping.
	stdout := `tokens: 100 input, 20 output
some response`
	usage := parseCodexStdoutTokens(stdout, "some response")
	if usage.InputTokens != 100 || usage.OutputTokens != 20 {
		t.Errorf("InputTokens=%d, OutputTokens=%d, want 100/20 — strip should be no-op when response appears only once",
			usage.InputTokens, usage.OutputTokens)
	}
}

func TestParseCodexStdoutTokens_StripIsNoOpWhenResponseAbsent(t *testing.T) {
	// If the response can't be located in combined (codex format
	// change, prefix/suffix mismatch from codex re-formatting), the
	// strip should be a no-op rather than mangle the input. Pin
	// graceful degradation.
	stdout := `tokens used
2,415`
	usage := parseCodexStdoutTokens(stdout, "completely different text not in stdout")
	if usage.InputTokens != 2415 {
		t.Errorf("InputTokens = %d, want 2415 (response-not-found should skip strip)", usage.InputTokens)
	}
}

func TestParseCodexStdoutTokens_AssistantSplitOnly(t *testing.T) {
	// Edge case: assistant prose contains split-shape text and
	// there's NO real session summary anywhere (e.g., codex was
	// killed mid-run, output truncated). We have only the
	// assistant's split-shape match — return it. There's nothing
	// better to fall back to, and "no usage at all" would be
	// less useful than the imperfect signal we do have.
	stdout := `codex
The model said tokens: 100 input, 20 output earlier`
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.InputTokens != 100 || usage.OutputTokens != 20 {
		t.Errorf("InputTokens=%d, OutputTokens=%d, want 100/20 (only candidate)",
			usage.InputTokens, usage.OutputTokens)
	}
}

func TestParseCodexStdoutTokens_DoesNotMatchContextIndicators(t *testing.T) {
	// Pre-v0.7.1 the permissive regex matched any "tokens:" or
	// "Total tokens:" line — including context-window indicators
	// near the start of the banner. A user reported "codex ✓ ·
	// 800 total" on a real review where the actual usage was much
	// higher; the 800 came from a "Total tokens: 800" context-
	// remaining line, not the session summary. Pin that we don't
	// match the misleading shapes.
	cases := []struct {
		name   string
		stdout string
	}{
		{"context-tokens prefix", "Available context tokens: 800\nhello world"},
		{"total-tokens prefix", "Total tokens: 800\nhello world"},
		{"tokens-without-input-or-used", "tokens: 800\nhello world"},
		{"capitalised tokens-no-used", "Tokens: 800 remaining"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := parseCodexStdoutTokens(tc.stdout, "")
			if !usage.IsZero() {
				t.Errorf("%s should not match, got %+v", tc.name, usage)
			}
		})
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
	usage := parseCodexStdoutTokens(stdout, "")
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
	usage := parseCodexStdoutTokens(stdout, "")
	if usage.TotalOnly {
		t.Errorf("split-shape codex output should not set TotalOnly: got %+v", usage)
	}
}

func TestParseCodexStdoutTokens_NoMatch(t *testing.T) {
	// Future codex version drops the line entirely. Better to
	// return zero usage than to fabricate.
	stdout := "no metadata at all"
	usage := parseCodexStdoutTokens(stdout, "")
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
