package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		cliName  string
		wantName string
	}{
		{
			name:     "claude CLI detection",
			cliName:  "claude",
			wantName: "claude",
		},
		{
			name:     "gemini CLI detection",
			cliName:  "gemini",
			wantName: "gemini",
		},
		{
			name:     "codex CLI detection",
			cliName:  "codex",
			wantName: "codex",
		},
		{
			name:     "non-existent CLI",
			cliName:  "nonexistent-cli-12345",
			wantName: "nonexistent-cli-12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := Detect(tt.cliName)

			if llm.Name != tt.wantName {
				t.Errorf("Detect() name = %v, want %v", llm.Name, tt.wantName)
			}

			// For non-existent CLI, Available should be false
			if tt.cliName == "nonexistent-cli-12345" && llm.Available {
				t.Errorf("Detect() Available = true for non-existent CLI, want false")
			}
		})
	}
}

func TestDetectAll(t *testing.T) {
	llms := DetectAll()

	// Should return exactly 4 LLMs (claude, gemini, codex, antigravity).
	// antigravity (the `agy` binary) was added in the v0.10.x Gemini-CLI
	// succession work — Google's Gemini CLI stops serving 2026-06-18.
	if len(llms) != 4 {
		t.Errorf("DetectAll() returned %d LLMs, want 4", len(llms))
	}

	// Check that all expected names are present
	expectedNames := map[string]bool{
		"claude":      false,
		"gemini":      false,
		"codex":       false,
		"antigravity": false,
	}

	for _, llm := range llms {
		if _, ok := expectedNames[llm.Name]; ok {
			expectedNames[llm.Name] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("DetectAll() missing expected LLM: %s", name)
		}
	}
}

func TestDetect_UnknownVersionMarksUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub uses /bin/sh; skip on windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecli")
	// Stub binary that prints no recognizable version, so detectVersion
	// returns "unknown" even though LookPath finds the binary.
	script := "#!/bin/sh\necho 'no version here'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir)

	llm := Detect("fakecli")
	if llm.Path == "" {
		t.Fatalf("expected LookPath to succeed for stub, got empty path")
	}
	if llm.Version != "unknown" {
		t.Fatalf("expected version=unknown, got %q", llm.Version)
	}
	if llm.Available {
		t.Errorf("Available = true for unknown-version CLI, want false")
	}
}

func TestIsReviewCapable(t *testing.T) {
	// The review-capable CLIs may join the fan-out; antigravity is
	// detected but excluded (its agentic --print can't produce a clean
	// review — see the 2026-05 dogfood note in invoker.go). This pins
	// the gate so a future "add agy to the fan-out" change has to
	// consciously flip it.
	tests := map[string]bool{
		"claude":      true,
		"gemini":      true,
		"codex":       true,
		"antigravity": false,
		"unknown":     true, // unknown names default to capable (no false-exclusion of custom agents)
	}
	for name, want := range tests {
		if got := IsReviewCapable(name); got != want {
			t.Errorf("IsReviewCapable(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "version with v prefix",
			output: "claude v2.1.0",
			want:   "2.1.0",
		},
		{
			name:   "version without v prefix",
			output: "gemini 0.40.0",
			want:   "0.40.0",
		},
		{
			name:   "version with colon",
			output: "version: 0.128.0",
			want:   "0.128.0",
		},
		{
			name:   "gh version output",
			output: "gh version 2.40.1 (2024-01-01)",
			want:   "2.40.1",
		},
		{
			name:   "multiline output",
			output: "OpenAI Codex CLI\nVersion: v0.128.0\nBuild: abc123",
			want:   "0.128.0",
		},
		{
			name:   "short version",
			output: "version 1.2",
			want:   "1.2",
		},
		{
			name:   "no version found",
			output: "no version here",
			want:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersion(tt.output)
			if got != tt.want {
				t.Errorf("parseVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
