package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
			name:     "antigravity invoker",
			llm:      LLM{Name: "antigravity", Path: "/usr/bin/agy"},
			wantNil:  false,
			wantType: "*cli.AntigravityInvoker",
		},
		{
			name:     "copilot invoker",
			llm:      LLM{Name: "copilot", Path: "/usr/bin/copilot"},
			wantNil:  false,
			wantType: "*cli.CopilotInvoker",
		},
		{
			// Provider agents are discriminated by BaseURL (not by name —
			// names are free-form for providers), so a free-form name +
			// BaseURL set must dispatch to *provider.Invoker. Pins PR 2
			// of the agents series.
			name:     "provider invoker (free-form name + base_url)",
			llm:      LLM{Name: "qwen", BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:7b"},
			wantNil:  false,
			wantType: "*provider.Invoker",
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
				} else if gotType := fmt.Sprintf("%T", invoker); gotType != tt.wantType {
					// Pin the dispatch: a non-nil invoker of the WRONG
					// concrete type (e.g. NewInvoker routing "codex" to
					// the gemini invoker) would otherwise pass silently.
					t.Errorf("NewInvoker() type = %s, want %s", gotType, tt.wantType)
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
	var _ Invoker = (*AntigravityInvoker)(nil)
	var _ Invoker = (*CopilotInvoker)(nil)
	// Antigravity must also expose its partial stderr (probe surfaces
	// the OAuth "Authentication required" message on timeout).
	var _ PartialStderrCapturer = (*AntigravityInvoker)(nil)
	// Copilot too — the probe surfaces its stderr diagnostic on timeout.
	var _ PartialStderrCapturer = (*CopilotInvoker)(nil)
}

func TestAntigravityInvoker_Review(t *testing.T) {
	invoker := &AntigravityInvoker{path: "/nonexistent/agy"}

	if _, _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestAntigravityInvoker_RejectsOversizedPrompt(t *testing.T) {
	// The prompt rides on argv (agy doesn't read stdin), so run()
	// enforces a 256 KiB ceiling to fail loud instead of letting exec
	// emit a cryptic "argument list too long". A >256 KiB prompt must
	// be rejected before exec is even attempted — note the path is
	// nonexistent, so reaching exec would surface a DIFFERENT error.
	invoker := &AntigravityInvoker{path: "/nonexistent/agy"}
	big := strings.Repeat("x", (256<<10)+1)
	_, _, err := invoker.RunPrompt(context.Background(), big)
	if err == nil {
		t.Fatal("expected oversized-prompt rejection, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected size-limit error, got: %v", err)
	}
}

func TestCopilotInvoker_Review(t *testing.T) {
	invoker := &CopilotInvoker{path: "/nonexistent/copilot"}

	if _, _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestCopilotInvoker_RejectsOversizedPrompt(t *testing.T) {
	// The prompt rides on argv (`-p <text>`), so run() enforces a
	// 256 KiB ceiling to fail loud instead of letting exec emit a
	// cryptic "argument list too long". Rejection must happen before
	// exec — the path is nonexistent, so reaching exec would surface a
	// DIFFERENT error.
	invoker := &CopilotInvoker{path: "/nonexistent/copilot"}
	big := strings.Repeat("x", (256<<10)+1)
	_, _, err := invoker.RunPrompt(context.Background(), big)
	if err == nil {
		t.Fatal("expected oversized-prompt rejection, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected size-limit error, got: %v", err)
	}
}

func TestCodexInvoker_Review(t *testing.T) {
	// Unit test with a non-existent path (will fail as expected). Real
	// integration testing happens in manual testing phase.
	invoker := &CodexInvoker{path: "/nonexistent/codex"}

	if _, _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestGeminiInvoker_Review(t *testing.T) {
	invoker := &GeminiInvoker{path: "/nonexistent/gemini"}

	if _, _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
		t.Error("Expected error with non-existent path, got nil")
	}
}

func TestClaudeInvoker_Review(t *testing.T) {
	invoker := &ClaudeInvoker{path: "/nonexistent/claude"}

	if _, _, err := invoker.Review(context.Background(), "", "sample diff"); err == nil {
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

// TestCopilotArgs_ToolsDisabledContract pins the copilot security
// contract from docs/security.md: the diff embedded in the prompt is
// attacker-controllable, so copilot MUST run with its tool whitelist
// emptied (`--available-tools=`) and without blocking on questions
// (`--no-ask-user`). Of the three documented runtime security controls,
// this was the only one without a regression test (2026-07 SecOps
// audit) — a refactor dropping either flag would silently re-arm
// prompt-injection-driven tool use.
func TestCopilotArgs_ToolsDisabledContract(t *testing.T) {
	for _, model := range []string{"", "gpt-5"} {
		args := copilotArgs("review this diff", model)
		var hasToolsDisabled, hasNoAskUser bool
		for _, a := range args {
			if a == "--available-tools=" {
				hasToolsDisabled = true
			}
			if a == "--no-ask-user" {
				hasNoAskUser = true
			}
			if a == "--allow-all-tools" {
				t.Fatalf("copilot argv must never contain --allow-all-tools, got %v", args)
			}
		}
		if !hasToolsDisabled {
			t.Errorf("copilot argv (model=%q) missing --available-tools= (empty whitelist): %v", model, args)
		}
		if !hasNoAskUser {
			t.Errorf("copilot argv (model=%q) missing --no-ask-user: %v", model, args)
		}
	}
}

// writeFakeClaude drops an executable shell script that mimics the claude
// CLI's --print --output-format json contract: emits the given payload on
// stdout and exits with the given code. Same fake-binary pattern as
// detector_test.go.
func writeFakeClaude(t *testing.T, payload string, exitCode int) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + payload + "'\nexit " + fmt.Sprint(exitCode) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestClaudeInvoker_SurfacesStructuredErrorOnExit1 locks the WIRING of
// claudeResultError into the exit-1 branch: the surfaced error must be
// the vendor's diagnosis (front of the payload), not ClassifyExit's
// output tail (2026-07 dogfood: an expired-login 401 rendered as
// trailing JSON metadata in the pre-flight probe).
func TestClaudeInvoker_SurfacesStructuredErrorOnExit1(t *testing.T) {
	payload := `{"type":"result","is_error":true,"api_error_status":401,"result":"Failed to authenticate. API Error: 401 Invalid authentication credentials","service_tier":"standard"}`
	inv := &ClaudeInvoker{path: writeFakeClaude(t, payload, 1)}
	_, _, err := inv.RunPrompt(context.Background(), "Reply OK")
	if err == nil {
		t.Fatal("expected an error from exit status 1")
	}
	if !strings.Contains(err.Error(), "Failed to authenticate") || !strings.Contains(err.Error(), "claude login") {
		t.Errorf("error must carry the vendor diagnosis + re-login hint, got: %v", err)
	}
	if strings.Contains(err.Error(), "service_tier") {
		t.Errorf("error must not be the raw JSON tail, got: %v", err)
	}
}

// TestClaudeInvoker_ErrorPayloadOnExit0IsNotAReview locks the exit-0
// guard: is_error:true with a zero exit must be an error, never returned
// as the review text (which would be persisted as a review file).
func TestClaudeInvoker_ErrorPayloadOnExit0IsNotAReview(t *testing.T) {
	payload := `{"type":"result","is_error":true,"api_error_status":429,"result":"Rate limited"}`
	inv := &ClaudeInvoker{path: writeFakeClaude(t, payload, 0)}
	text, _, err := inv.RunPrompt(context.Background(), "Reply OK")
	if err == nil {
		t.Fatalf("expected an error, got review text %q", text)
	}
	if !strings.Contains(err.Error(), "Rate limited") {
		t.Errorf("error must carry the vendor diagnosis, got: %v", err)
	}
}
