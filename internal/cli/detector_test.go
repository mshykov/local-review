package cli

import (
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
			name:     "gh CLI detection",
			cliName:  "gh",
			wantName: "gh",
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

	// Should return exactly 4 LLMs (claude, gemini, codex, gh)
	if len(llms) != 4 {
		t.Errorf("DetectAll() returned %d LLMs, want 4", len(llms))
	}

	// Check that all expected names are present
	expectedNames := map[string]bool{
		"claude": false,
		"gemini": false,
		"codex":  false,
		"gh":     false,
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
