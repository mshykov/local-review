package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runConfigCmdIn runs the `config` cobra command after chdir'ing into
// a temp dir seeded with the given .local-review.yml content, and
// returns the command's stdout. Mirrors the production code path:
// the cobra Command shares the loadConfig() entrypoint that the real
// binary uses, so URL-masking changes can't drift between unit-test
// and real invocation.
func runConfigCmdIn(t *testing.T, yamlContent string) string {
	t.Helper()
	// These tests inject security-sensitive fields (base_url) via the
	// REPO-level .local-review.yml to exercise the `config` printer's
	// masking. Repo-layer base_url is untrusted-and-stripped by default
	// (see config.Load), so opt this synthetic repo into trust — masking
	// only matters for a value that's actually honored.
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "1")
	// Hermetic: point the user-home lookup at an empty temp dir so the
	// dump reflects only the synthetic repo config, not the developer's
	// real ~/.local-review.yml. os.UserHomeDir() reads $HOME (Unix) /
	// %USERPROFILE% (Windows).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".local-review.yml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origCwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cmd := configCmd(&sharedFlags{})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config command: %v", err)
	}
	return out.String()
}

func TestConfigCommand_MasksBasicAuthInLLMsBaseURL(t *testing.T) {
	// Credentials embedded in basic-auth userinfo (`user:pass@host`)
	// must NOT survive a `config` dump — same standard as the api_key
	// field, which has always been masked. Pre-fix the value was
	// printed verbatim.
	//
	// v0.15 dropped the v0 `provider:` block; the same masking
	// contract now applies to every `llms.<name>.base_url` value.
	out := runConfigCmdIn(t, `
llms:
  openai:
    base_url: https://leak-user:leak-pass@api.openai.com/v1
    model: gpt-4o
`)
	if strings.Contains(out, "leak-user") || strings.Contains(out, "leak-pass") {
		t.Errorf("basic-auth credentials must be stripped from config output; got:\n%s", out)
	}
	if !strings.Contains(out, "api.openai.com/v1") {
		t.Errorf("scheme+host+path must survive sanitization; got:\n%s", out)
	}
}

func TestConfigCommand_MasksQueryStringInLLMsBaseURL(t *testing.T) {
	// Some providers accept the key as a query-string parameter.
	// That belongs in an env var, not in a shareable config dump.
	out := runConfigCmdIn(t, `
llms:
  openai:
    base_url: https://api.example.test/v1?api_key=sk-LEAKED
    model: gpt-4o
`)
	if strings.Contains(out, "sk-LEAKED") || strings.Contains(out, "api_key=") {
		t.Errorf("query-string credentials must be stripped from config output; got:\n%s", out)
	}
	if !strings.Contains(out, "api.example.test/v1") {
		t.Errorf("scheme+host+path must survive sanitization; got:\n%s", out)
	}
}

// TestConfigCommand_PlainLLMsBaseURLRoundtrips (below) covers the
// plain-URL roundtrip path. The v0.14 separate "Provider" variant
// is gone as of v0.15 — there's nothing left to differentiate.

func TestConfigCommand_MasksCredsInExtraLLMsBaseURL(t *testing.T) {
	// Symmetric coverage: a second `llms.<name>.base_url` entry
	// (different name, different vendor) must also be masked. Pins
	// that the printer iterates the LLMs map rather than masking
	// only a known key.
	out := runConfigCmdIn(t, `
llms:
  cloud:
    base_url: https://creduser:credpass@api.together.xyz/v1?api_key=sk-LLM-LEAK
    model: meta-llama/Llama-3-70b
`)
	for _, secret := range []string{"creduser", "credpass", "sk-LLM-LEAK", "api_key="} {
		if strings.Contains(out, secret) {
			t.Errorf("llms.cloud.base_url leaked %q in config output; got:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "api.together.xyz/v1") {
		t.Errorf("scheme+host+path must survive sanitization for llms entries; got:\n%s", out)
	}
}

func TestConfigCommand_PlainLLMsBaseURLRoundtrips(t *testing.T) {
	// Symmetric roundtrip check for the llms.<name>.base_url branch.
	out := runConfigCmdIn(t, `
llms:
  ollama:
    base_url: http://localhost:11434/v1
    model: qwen2.5
`)
	if !strings.Contains(out, "http://localhost:11434/v1") {
		t.Errorf("plain llms.ollama.base_url must roundtrip; got:\n%s", out)
	}
}

// TestConfigCommand_PrintsConfigSources pins the "# Config sources:"
// diagnostic block: users need `config` to answer "which
// .local-review.yml is this folder actually using, and was it
// trusted?" — the resolved YAML alone can't (2026-07 dogfood).
func TestConfigCommand_PrintsConfigSources(t *testing.T) {
	out := runConfigCmdIn(t, `
review:
  max_findings: 7
`)
	if !strings.Contains(out, "# Config sources") {
		t.Fatalf("missing config-sources block:\n%s", out)
	}
	for _, want := range []string{"built-in defaults", "home", "repo", "loaded (trusted)", "CLI flags"} {
		if !strings.Contains(out, want) {
			t.Errorf("config-sources block missing %q:\n%s", want, out)
		}
	}
	// The harness's fake $HOME has no config file — that layer must
	// say so rather than claiming it loaded.
	if !strings.Contains(out, "(not found)") {
		t.Errorf("expected home layer to report (not found) under the fake $HOME:\n%s", out)
	}
}
