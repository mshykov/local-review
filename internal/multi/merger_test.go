package multi

import (
	"strings"
	"testing"
)

func TestBuildMergeInput_TruncatesOversizeReviews(t *testing.T) {
	// A verbose or hallucinating reviewer can dump a multi-megabyte
	// payload that would blow the merger's context window if we
	// concatenated it verbatim. Pin the cap.
	huge := strings.Repeat("x", MaxReviewBytesForMerge*3)
	results := []ReviewResult{
		{LLM: "claude", Output: "small finding"},
		{LLM: "gemini", Output: huge},
	}
	in := BuildMergeInput(results, 2)
	if len(in.Reviews) != 2 {
		t.Fatalf("want 2 reviews, got %d", len(in.Reviews))
	}
	// Find the gemini one — order is preserved by appearance.
	var gem ReviewContent
	for _, r := range in.Reviews {
		if r.LLM == "gemini" {
			gem = r
		}
	}
	if len(gem.Content) > MaxReviewBytesForMerge {
		t.Errorf("oversize review not truncated: len=%d, cap=%d", len(gem.Content), MaxReviewBytesForMerge)
	}
	if !strings.Contains(gem.Content, "truncated") {
		t.Errorf("truncation marker missing from clipped content")
	}
}

func TestBuildMergeInput_PassesNormalReviewsUnchanged(t *testing.T) {
	body := "## Major Issues\n- file:42 — race condition.\n"
	results := []ReviewResult{{LLM: "claude", Output: body}}
	in := BuildMergeInput(results, 2)
	if in.Reviews[0].Content != body {
		t.Errorf("non-oversize review was modified — got:\n%s", in.Reviews[0].Content)
	}
}

func TestBuildMergeInput_SkipsEmptyOutputs(t *testing.T) {
	// Existing behavior preserved: a failed review with empty Output
	// is dropped from the merge input rather than fed as an empty
	// stub the merger has to interpret.
	results := []ReviewResult{
		{LLM: "claude", Output: "real finding"},
		{LLM: "gemini", Output: ""},
		{LLM: "codex", Output: "another"},
	}
	in := BuildMergeInput(results, 2)
	if len(in.Reviews) != 2 {
		t.Errorf("want 2 reviews (empty dropped), got %d", len(in.Reviews))
	}
	if in.LLMNames != "claude, codex" {
		t.Errorf("LLMNames = %q, want %q", in.LLMNames, "claude, codex")
	}
}
