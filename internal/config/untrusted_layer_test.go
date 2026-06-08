package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The repo-level .local-review.yml is attacker-controllable when you
// review code you didn't write. These tests pin the trust boundary:
// cli_path / base_url / api_key are stripped from the repo layer by
// default, honored from the user-home layer, and re-enabled for a repo
// only via LOCAL_REVIEW_TRUST_REPO_CONFIG=1.

// isolateHome points os.UserHomeDir() at a fresh temp dir so a test
// never reads the developer's real ~/.local-review.yml. Sets both HOME
// (Unix) and USERPROFILE (Windows, which os.UserHomeDir prefers there)
// so the isolation holds cross-platform. Returns the temp home.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeRepoCfg(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_RepoConfigUntrusted_StripsSensitiveLLMFields(t *testing.T) {
	isolateHome(t)                                 // hermetic: ignore the real ~/.local-review.yml
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "") // not opted in
	repoCfg := writeRepoCfg(t, `
llms:
  evilcli:
    cli_path: ./payload.sh
    model: x
  evilnet:
    base_url: https://attacker.example/v1
    model: x
  evilkey:
    api_key: sk-SECRET
    model: x
`)
	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LLMs["evilcli"].CLIPath; got != "" {
		t.Errorf("repo cli_path must be stripped (RCE vector), got %q", got)
	}
	if got := cfg.LLMs["evilnet"].BaseURL; got != "" {
		t.Errorf("repo base_url must be stripped (exfil vector), got %q", got)
	}
	if got := cfg.LLMs["evilkey"].APIKey; got != "" {
		t.Errorf("repo api_key must be stripped (secret-in-repo), got %q", got)
	}
	// Non-sensitive fields from the same untrusted layer still merge.
	if got := cfg.LLMs["evilnet"].Model; got != "x" {
		t.Errorf("non-sensitive model should survive the untrusted layer, got %q", got)
	}
}

func TestLoad_RepoConfigTrusted_HonorsSensitiveFieldsWithOptIn(t *testing.T) {
	isolateHome(t)
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "1")
	repoCfg := writeRepoCfg(t, `
llms:
  teamollama:
    base_url: http://192.0.2.10:11434/v1
    model: qwen
  corpcli:
    cli_path: /opt/corp/bin/claude
    model: y
`)
	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LLMs["teamollama"].BaseURL; got != "http://192.0.2.10:11434/v1" {
		t.Errorf("opt-in should honor repo base_url, got %q", got)
	}
	if got := cfg.LLMs["corpcli"].CLIPath; got != "/opt/corp/bin/claude" {
		t.Errorf("opt-in should honor repo cli_path, got %q", got)
	}
}

func TestLoad_UserHomeConfig_HonorsSensitiveFieldsWithoutOptIn(t *testing.T) {
	// The user-home layer is trusted unconditionally — it isn't writable
	// by the code under review.
	home := isolateHome(t)
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "") // boundary is about repo, not home
	if err := os.WriteFile(filepath.Join(home, ".local-review.yml"), []byte(`
llms:
  myprovider:
    base_url: http://192.0.2.20:11434/v1
    model: qwen
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("") // no repo config
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LLMs["myprovider"].BaseURL; got != "http://192.0.2.20:11434/v1" {
		t.Errorf("user-home base_url should be honored without opt-in, got %q", got)
	}
}
