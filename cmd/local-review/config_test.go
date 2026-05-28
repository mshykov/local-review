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

func TestConfigCommand_MasksBasicAuthInProviderBaseURL(t *testing.T) {
	// Credentials embedded in basic-auth userinfo (`user:pass@host`)
	// must NOT survive a `config` dump — same standard as the api_key
	// field, which has always been masked. Pre-fix the value was
	// printed verbatim.
	out := runConfigCmdIn(t, `
provider:
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

func TestConfigCommand_MasksQueryStringInProviderBaseURL(t *testing.T) {
	// Some providers accept the key as a query-string parameter.
	// That belongs in an env var, not in a shareable config dump.
	out := runConfigCmdIn(t, `
provider:
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

func TestConfigCommand_PlainProviderBaseURLRoundtrips(t *testing.T) {
	// The common case: an unauthenticated URL stays exactly itself.
	// Sanitization must not corrupt valid configs.
	out := runConfigCmdIn(t, `
provider:
  base_url: http://localhost:11434/v1
  model: qwen2.5
`)
	if !strings.Contains(out, "http://localhost:11434/v1") {
		t.Errorf("plain URL must roundtrip; got:\n%s", out)
	}
}

func TestConfigCommand_MasksCredsInLLMsBaseURL(t *testing.T) {
	// The unified v0.14 agent model puts the same `base_url:` field
	// under `llms.<name>:`. Masking must apply there too — the
	// `provider:` block isn't the only leak surface.
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
