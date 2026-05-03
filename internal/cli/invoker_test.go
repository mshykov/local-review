package cli

import (
	"context"
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
	// This is a unit test with a non-existent path (will fail as expected)
	// Real integration testing happens in manual testing phase
	invoker := &CodexInvoker{path: "/nonexistent/codex"}

	ctx := context.Background()
	diff := "sample diff"

	_, err := invoker.Review(ctx, diff)

	// Should error because the path doesn't exist
	if err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestGeminiInvoker_Review(t *testing.T) {
	invoker := &GeminiInvoker{path: "/nonexistent/gemini"}

	ctx := context.Background()
	diff := "sample diff"

	_, err := invoker.Review(ctx, diff)

	// Should error because the path doesn't exist
	if err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestClaudeInvoker_Review(t *testing.T) {
	invoker := &ClaudeInvoker{path: "/nonexistent/claude"}

	ctx := context.Background()
	diff := "sample diff"

	_, err := invoker.Review(ctx, diff)

	// Should error because the path doesn't exist
	if err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}
