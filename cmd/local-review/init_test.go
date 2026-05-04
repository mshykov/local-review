package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runInitTo runs the wizard with the given keystrokes, writing to a
// temp-dir target file. Returns stdout, the file content (empty if no
// file was written), and any error from runInit.
func runInitTo(t *testing.T, input string, force bool) (stdout, fileContent string, target string, err error) {
	t.Helper()
	dir := t.TempDir()
	target = filepath.Join(dir, ".local-review.yml")
	out := &bytes.Buffer{}
	in := strings.NewReader(input)
	err = runInit(out, in, target, force)

	if b, readErr := os.ReadFile(target); readErr == nil {
		fileContent = string(b)
	}
	return out.String(), fileContent, target, err
}

func TestInit_OpenAIDefaultPath(t *testing.T) {
	// 1) provider 1 (OpenAI), 2) accept default model, 3) accept default env var,
	// 4) accept default severity, 5) accept default max_findings, 6) confirm write
	input := "1\n\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("init failed: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "✓ Wrote") {
		t.Errorf("expected success message, got:\n%s", stdout)
	}
	for _, want := range []string{
		"base_url: https://api.openai.com/v1",
		"model: gpt-4o-mini",
		"api_key_env: OPENAI_API_KEY",
		"min_severity: warning",
		"max_findings: 20",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config missing %q\nfull config:\n%s", want, content)
		}
	}
}

func TestInit_OllamaSkipsAPIKeyEnv(t *testing.T) {
	// Choice 4 = Ollama (preset list: OpenAI, Mistral, DeepSeek, Ollama, Other).
	// Then accept defaults, confirm write.
	input := "4\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("init failed: %v\nstdout:\n%s", err, stdout)
	}
	if strings.Contains(content, "api_key_env:") {
		t.Errorf("Ollama should not require an API key env var:\n%s", content)
	}
	if !strings.Contains(content, "base_url: http://localhost:11434/v1") {
		t.Errorf("expected Ollama base_url:\n%s", content)
	}
	if strings.Contains(stdout, "Set your API key:") {
		t.Errorf("Ollama path should not prompt to set API key:\n%s", stdout)
	}
}

func TestInit_CustomProviderRequiresBaseURL(t *testing.T) {
	// Choice 5 = Other; then leave base URL blank.
	input := "5\n\n"
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "base URL is required") {
		t.Errorf("expected base-URL-required error, got: %v", err)
	}
}

func TestInit_InvalidProviderChoice(t *testing.T) {
	input := "99\n"
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "choice must be") {
		t.Errorf("expected invalid-choice error, got: %v", err)
	}
}

func TestInit_InvalidSeverity(t *testing.T) {
	// OpenAI defaults, then enter garbage for severity.
	input := "1\n\n\nbogus\n"
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("expected severity validation error, got: %v", err)
	}
}

func TestInit_InvalidMaxFindings(t *testing.T) {
	// OpenAI defaults, accept severity, then non-numeric max findings.
	input := "1\n\n\n\nNaN\n"
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "max findings") {
		t.Errorf("expected max-findings validation error, got: %v", err)
	}
}

func TestInit_AbortsOnConfirmDecline(t *testing.T) {
	// Pick OpenAI, accept defaults, then say "n" at the final write prompt.
	input := "1\n\n\n\n\nn\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("init returned error on graceful abort: %v", err)
	}
	if content != "" {
		t.Errorf("declining write should not have created a file, got:\n%s", content)
	}
	if !strings.Contains(stdout, "Aborted") {
		t.Errorf("expected abort message, got:\n%s", stdout)
	}
}

func TestInit_RefusesToOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(target, []byte("# pre-existing content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Default answer to overwrite is "n" — empty input accepts default.
	out := &bytes.Buffer{}
	in := strings.NewReader("\n")
	if err := runInit(out, in, target, false); err != nil {
		t.Fatalf("init returned error on graceful skip: %v", err)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected abort message, got:\n%s", out.String())
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), "pre-existing content") {
		t.Errorf("existing file was modified:\nerr=%v\ncontent=%s", err, got)
	}
}

func TestInit_ForceOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(target, []byte("# pre-existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// --force=true: skip overwrite confirmation entirely.
	// Pick OpenAI, accept defaults, confirm write.
	input := "1\n\n\n\n\ny\n"
	out := &bytes.Buffer{}
	in := strings.NewReader(input)
	if err := runInit(out, in, target, true); err != nil {
		t.Fatalf("init failed under --force: %v\nstdout:\n%s", err, out.String())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "pre-existing") {
		t.Errorf("--force should have replaced the file:\n%s", string(got))
	}
	if !strings.Contains(string(got), "base_url: https://api.openai.com/v1") {
		t.Errorf("--force write produced unexpected content:\n%s", string(got))
	}
}

func TestInit_ForceWithoutExistingFileWritesNormally(t *testing.T) {
	// --force should be a no-op when there's nothing to overwrite.
	dir := t.TempDir()
	target := filepath.Join(dir, ".local-review.yml")
	input := "1\n\n\n\n\ny\n"
	out := &bytes.Buffer{}
	in := strings.NewReader(input)
	if err := runInit(out, in, target, true); err != nil {
		t.Fatalf("init with --force on non-existent file failed: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), "base_url: https://api.openai.com/v1") {
		t.Errorf("expected fresh OpenAI config, got err=%v\ncontent=%s", err, got)
	}
}

func TestInit_RefusesIfTargetIsDirectory(t *testing.T) {
	// If the target path resolves to an existing directory, the wizard
	// should fail loudly rather than try to write through it.
	dir := t.TempDir()
	if err := runInit(&bytes.Buffer{}, strings.NewReader(""), dir, false); err == nil {
		t.Errorf("expected refusal when target is a directory, got nil")
	} else if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"local", ".local-review.yml", false},
		{"", ".local-review.yml", false},
		{"LOCAL", ".local-review.yml", false},
		{"bogus", "", true},
	}
	for _, tt := range tests {
		got, err := resolveTarget(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("resolveTarget(%q) expected error, got %q", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveTarget(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("resolveTarget(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// "global" should produce a path under the user home dir; we just
	// check it ends with the right filename (varies by env).
	got, err := resolveTarget("global")
	if err != nil {
		t.Fatalf(`resolveTarget("global"): %v`, err)
	}
	if !strings.HasSuffix(got, "/.local-review.yml") {
		t.Errorf(`resolveTarget("global") = %q, want suffix "/.local-review.yml"`, got)
	}
}

func TestRenderConfig_Shape(t *testing.T) {
	yml := renderConfig("https://api.openai.com/v1", "gpt-4o-mini", "OPENAI_API_KEY", "warning", 20)
	for _, want := range []string{
		"# .local-review.yml — generated by `local-review init`.",
		"provider:",
		"  base_url: https://api.openai.com/v1",
		"  model: gpt-4o-mini",
		"  api_key_env: OPENAI_API_KEY",
		"  timeout_seconds: 60",
		"review:",
		"  min_severity: warning",
		"  max_findings: 20",
		"  exclude:",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("renderConfig missing %q\nfull output:\n%s", want, yml)
		}
	}
}

func TestRenderConfig_OmitsAPIKeyEnvWhenEmpty(t *testing.T) {
	yml := renderConfig("http://localhost:11434/v1", "qwen2.5-coder:7b", "", "warning", 20)
	if strings.Contains(yml, "api_key_env:") {
		t.Errorf("api_key_env should be omitted when empty:\n%s", yml)
	}
}
