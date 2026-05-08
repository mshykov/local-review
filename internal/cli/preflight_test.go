package cli

import (
	"strings"
	"testing"
)

func TestEstimatePromptPayload_IncludesWrapperAndSeparator(t *testing.T) {
	// The invokers wrap systemPrompt with buildReviewPrompt
	// (appends multiLLMOutputOverride) and prepend "\n\n# Diff\n\n"
	// before the diff. Preflight has to estimate the *actual* sent
	// payload, not just systemPrompt+diff in isolation — pre-fix,
	// estimate undercounted by ~1k tokens for an empty prompt
	// (the override alone is ~400 chars / ~115 tokens), letting
	// near-the-limit diffs pass that would 4xx in the real call.
	systemPrompt := "review this"
	diff := "--- a/x\n+++ b/x\n@@\n+hi\n"
	rawSum := EstimateTokens(systemPrompt) + EstimateTokens(diff)
	wrapped := EstimatePromptPayload(systemPrompt, diff)
	if wrapped <= rawSum {
		t.Errorf("EstimatePromptPayload should be larger than raw sum (it includes the wrapper + separator); got wrapped=%d, raw=%d", wrapped, rawSum)
	}
}

func TestEstimateTokens_UpperBoundCeil(t *testing.T) {
	// 36 bytes / 3.5 = 10.28… — math.Ceil → 11 tokens. Pre-fix
	// int() truncated to 10, contradicting the "conservative /
	// upper bound" claim in the doc comment. The bytes-just-above-
	// a-multiple-of-3.5 case is what the gate actually cares about
	// (a single extra byte should round UP to one more token).
	got := EstimateTokens(strings.Repeat("a", 36))
	if got != 11 {
		t.Errorf("EstimateTokens(36 bytes) = %d, want 11 (Ceil(36/3.5))", got)
	}
	// 35 bytes / 3.5 = exactly 10. Both Floor and Ceil agree here;
	// just makes sure we didn't introduce off-by-one for the exact
	// case.
	if got := EstimateTokens(strings.Repeat("a", 35)); got != 10 {
		t.Errorf("EstimateTokens(35 bytes) = %d, want 10", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		minTokens int
		maxTokens int
	}{
		{"empty string", "", 0, 0},
		{"35 chars (≈10 tokens)", strings.Repeat("a", 35), 9, 11},
		{"3500 chars (≈1000 tokens)", strings.Repeat("a", 3500), 990, 1010},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateTokens(tc.input)
			if got < tc.minTokens || got > tc.maxTokens {
				t.Errorf("EstimateTokens(%d chars) = %d, want range [%d, %d]",
					len(tc.input), got, tc.minTokens, tc.maxTokens)
			}
		})
	}
}

func TestContextWindow_KnownAgents(t *testing.T) {
	// Sanity-check the table — these floors should match what's
	// documented in the function's doc comment. If a vendor shrinks
	// a window and we need to lower one of these, that's a deliberate
	// change and this test is the canary.
	tests := map[string]int{
		"claude": 200_000,
		"gemini": 1_000_000,
		"codex":  128_000,
	}
	for name, want := range tests {
		if got := ContextWindow(name); got != want {
			t.Errorf("ContextWindow(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestContextWindow_UnknownAgent(t *testing.T) {
	// 0 is the "don't preflight" sentinel. The filter should pass
	// unknown agents through unchanged so a future agent doesn't get
	// silently dropped on rollout.
	if got := ContextWindow("future-llm"); got != 0 {
		t.Errorf("ContextWindow(unknown) = %d, want 0", got)
	}
}

func TestPreflightFilter_SmallDiffFitsAll(t *testing.T) {
	active := []LLM{
		{Name: "claude"},
		{Name: "gemini"},
		{Name: "codex"},
	}
	prompt := "review this diff"
	diff := strings.Repeat("a", 100) // ≈30 tokens
	kept, skipped, _ := PreflightFilter(active, prompt, diff)
	if len(kept) != 3 {
		t.Errorf("small diff should keep all 3 agents, got %d", len(kept))
	}
	if len(skipped) != 0 {
		t.Errorf("small diff should skip none, got %d", len(skipped))
	}
}

func TestPreflightFilter_LargeDiffSkipsCodexFirst(t *testing.T) {
	active := []LLM{
		{Name: "claude"},
		{Name: "gemini"},
		{Name: "codex"},
	}
	// 500K bytes ≈ 143K tokens. Above codex (128K) but well below
	// claude (200K) and gemini (1M). Codex should be the only one
	// skipped.
	diff := strings.Repeat("a", 500_000)
	kept, skipped, est := PreflightFilter(active, "", diff)
	if est < 130_000 || est > 150_000 {
		t.Errorf("estimate (%d) outside expected band ~143k for 500K bytes", est)
	}
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2 (claude + gemini)", len(kept))
	}
	if len(skipped) != 1 || skipped[0].Name != "codex" {
		t.Errorf("expected exactly codex skipped, got %+v", skipped)
	}
}

func TestPreflightFilter_HugeDiffSkipsClaudeAndCodex(t *testing.T) {
	active := []LLM{
		{Name: "claude"},
		{Name: "gemini"},
		{Name: "codex"},
	}
	// 800K bytes ≈ 228K tokens. Above claude (200K) and codex
	// (128K), below gemini (1M). Only gemini should survive.
	diff := strings.Repeat("a", 800_000)
	kept, skipped, _ := PreflightFilter(active, "", diff)
	if len(kept) != 1 || kept[0].Name != "gemini" {
		t.Errorf("kept = %+v, want only gemini", kept)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %d, want 2 (claude + codex)", len(skipped))
	}
}

func TestPreflightFilter_OversizedSkipsAll(t *testing.T) {
	active := []LLM{
		{Name: "claude"},
		{Name: "gemini"},
		{Name: "codex"},
	}
	// 5M bytes ≈ 1.4M tokens. Above all three windows (claude 200K,
	// gemini 1M, codex 128K). Caller is expected to surface this
	// and error out before fan-out.
	diff := strings.Repeat("a", 5_000_000)
	kept, skipped, _ := PreflightFilter(active, "", diff)
	if len(kept) != 0 {
		t.Errorf("kept = %d, want 0 (all should skip)", len(kept))
	}
	if len(skipped) != 3 {
		t.Errorf("skipped = %d, want 3", len(skipped))
	}
}

func TestPreflightFilter_UnknownAgentPassesThrough(t *testing.T) {
	active := []LLM{
		{Name: "claude"},
		{Name: "future-llm"}, // not in our table
	}
	// Diff that would skip claude (220K tokens) — but future-llm has
	// ContextWindow=0 so it's treated as "we don't know, let it
	// decide". Should be kept regardless of size.
	diff := strings.Repeat("a", 770_000)
	kept, _, _ := PreflightFilter(active, "", diff)
	hasFutureLLM := false
	for _, a := range kept {
		if a.Name == "future-llm" {
			hasFutureLLM = true
		}
	}
	if !hasFutureLLM {
		t.Errorf("future-llm should pass through preflight unchanged, got kept = %+v", kept)
	}
}

func TestPreflightFilter_SafetyMarginBlocksJustUnderLimit(t *testing.T) {
	// 700K bytes ≈ 200K tokens — exactly at claude's window. With
	// the 10K response margin, this should skip claude.
	active := []LLM{{Name: "claude"}}
	diff := strings.Repeat("a", 700_000)
	kept, skipped, _ := PreflightFilter(active, "", diff)
	if len(kept) != 0 {
		t.Errorf("at-limit diff should be skipped due to response margin, got kept = %+v", kept)
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %d, want 1", len(skipped))
	}
}

func TestFormatSkipReason_HasActionableHint(t *testing.T) {
	s := SkippedAgent{Name: "claude", PromptDiffTokens: 280_000, ContextWindow: 200_000}
	got := FormatSkipReason(s)
	for _, want := range []string{"local-review commit HEAD", "local-review staged", "claude"} {
		if !strings.Contains(got, want) {
			t.Errorf("skip reason missing %q: %s", want, got)
		}
	}
}

func TestSkipSummary_EmptyWhenNoneSkipped(t *testing.T) {
	if got := SkipSummary(nil); got != "" {
		t.Errorf("empty input should give empty output, got %q", got)
	}
}

func TestSkipSummary_OneLinePerAgent(t *testing.T) {
	skipped := []SkippedAgent{
		{Name: "claude", PromptDiffTokens: 280_000, ContextWindow: 200_000},
		{Name: "codex", PromptDiffTokens: 280_000, ContextWindow: 128_000},
	}
	got := SkipSummary(skipped)
	if !strings.Contains(got, "claude") || !strings.Contains(got, "codex") {
		t.Errorf("summary missing one of the agent names: %s", got)
	}
	if strings.Count(got, "⚠") != 2 {
		t.Errorf("expected 2 warning lines, got %s", got)
	}
}
