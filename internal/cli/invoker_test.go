package cli

import (
	"context"
	"strings"
	"testing"
)

func TestNewInvoker(t *testing.T) {
	tests := []struct {
		name     string
		llm      LLM
		wantNil  bool
		wantType string
	}{
		{
			name:     "codex invoker",
			llm:      LLM{Name: "codex", Path: "/usr/bin/codex"},
			wantNil:  false,
			wantType: "*cli.CodexInvoker",
		},
		{
			name:     "gemini invoker",
			llm:      LLM{Name: "gemini", Path: "/usr/bin/gemini"},
			wantNil:  false,
			wantType: "*cli.GeminiInvoker",
		},
		{
			name:     "claude invoker",
			llm:      LLM{Name: "claude", Path: "/usr/bin/claude"},
			wantNil:  false,
			wantType: "*cli.ClaudeInvoker",
		},
		{
			name:    "unknown invoker",
			llm:     LLM{Name: "unknown", Path: "/usr/bin/unknown"},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invoker := NewInvoker(tt.llm)

			if tt.wantNil {
				if invoker != nil {
					t.Errorf("NewInvoker() = %v, want nil", invoker)
				}
			} else {
				if invoker == nil {
					t.Errorf("NewInvoker() = nil, want non-nil")
				}
			}
		})
	}
}

func TestInvokerInterface(t *testing.T) {
	// Verify that all invoker types implement the Invoker interface
	var _ Invoker = (*CodexInvoker)(nil)
	var _ Invoker = (*GeminiInvoker)(nil)
	var _ Invoker = (*ClaudeInvoker)(nil)
}

func TestCodexInvoker_Review(t *testing.T) {
	// Unit test with a non-existent path (will fail as expected). Real
	// integration testing happens in manual testing phase.
	invoker := &CodexInvoker{path: "/nonexistent/codex"}

	if _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestGeminiInvoker_Review(t *testing.T) {
	invoker := &GeminiInvoker{path: "/nonexistent/gemini"}

	if _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestClaudeInvoker_Review(t *testing.T) {
	invoker := &ClaudeInvoker{path: "/nonexistent/claude"}

	if _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	// Pin the contract every Review() depends on:
	//   - Empty systemPrompt → falls back to the generic 4-bullet prompt
	//     (defends against tests / out-of-tree callers that haven't
	//     learned to pass a pack).
	//   - Non-empty systemPrompt → that pack content is included verbatim.
	//   - The markdown-output override is always appended so multi-LLM
	//     agents emit prose the merger can consolidate, regardless of
	//     what the pack itself prescribes for output format (the packs
	//     mandate JSON for the single-LLM path).
	if got := buildReviewPrompt(""); !contains(got, "code reviewer") || !contains(got, "markdown") {
		t.Errorf("empty systemPrompt should yield generic prompt + markdown override, got:\n%s", got)
	}
	pack := "## Custom Pack\n- Look for SQL injection.\n"
	got := buildReviewPrompt(pack)
	if !contains(got, "Custom Pack") {
		t.Errorf("pack content not preserved verbatim:\n%s", got)
	}
	if !contains(got, "Do NOT return JSON") {
		t.Errorf("markdown-output override not appended:\n%s", got)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
