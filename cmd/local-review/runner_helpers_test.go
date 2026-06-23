package main

import (
	"testing"

	"github.com/mshykov/local-review/internal/multi"
)

// blockingMD / cleanMD are minimal merged-report fixtures that exercise
// review.IsBlockingMarkdown the same way the runner does at runtime.
const (
	blockingMD = "## Critical Issues\n\n- **runner.go:42** — buffer overflow when input is very large\n  Fix: bounds-check before write.\n"
	cleanMD    = "## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n"
)

func blockingResult() multi.ReviewResult {
	return multi.ReviewResult{LLM: "claude", Output: blockingMD}
}
func cleanResult() multi.ReviewResult { return multi.ReviewResult{LLM: "claude", Output: cleanMD} }

// TestDecideExitGate pins the security-critical ordering invariant: the
// per-LLM blocking scan runs even when the merged report is empty, so a
// merge-step failure with blocking per-LLM findings still trips exit 2
// (never collapses to the exit-1 that pre-commit hooks let through).
func TestDecideExitGate(t *testing.T) {
	tests := []struct {
		name            string
		merged          string
		results         []multi.ReviewResult
		wantBlock       bool
		wantUnavailable bool
	}{
		{
			name:            "empty merged + blocking per-LLM still blocks (the regression guard)",
			merged:          "",
			results:         []multi.ReviewResult{blockingResult()},
			wantBlock:       true,
			wantUnavailable: false,
		},
		{
			name:            "whitespace-only merged + blocking per-LLM still blocks",
			merged:          "   \n\t ",
			results:         []multi.ReviewResult{blockingResult()},
			wantBlock:       true,
			wantUnavailable: false,
		},
		{
			name:            "empty merged + clean per-LLM is merge-unavailable (exit 1, not blocked)",
			merged:          "",
			results:         []multi.ReviewResult{cleanResult()},
			wantBlock:       false,
			wantUnavailable: true,
		},
		{
			name:            "blocking merged + clean per-LLM blocks",
			merged:          blockingMD,
			results:         []multi.ReviewResult{cleanResult()},
			wantBlock:       true,
			wantUnavailable: false,
		},
		{
			name:            "clean merged + blocking per-LLM blocks (truncation defence)",
			merged:          cleanMD,
			results:         []multi.ReviewResult{blockingResult()},
			wantBlock:       true,
			wantUnavailable: false,
		},
		{
			name:            "clean merged + clean per-LLM passes",
			merged:          cleanMD,
			results:         []multi.ReviewResult{cleanResult()},
			wantBlock:       false,
			wantUnavailable: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideExitGate(tc.merged, tc.results)
			if got.block != tc.wantBlock {
				t.Errorf("block: got %v, want %v", got.block, tc.wantBlock)
			}
			if got.mergeUnavailable != tc.wantUnavailable {
				t.Errorf("mergeUnavailable: got %v, want %v", got.mergeUnavailable, tc.wantUnavailable)
			}
			// A run can never be both blocking AND merge-unavailable.
			if got.block && got.mergeUnavailable {
				t.Errorf("block and mergeUnavailable are mutually exclusive, got both true")
			}
		})
	}
}
