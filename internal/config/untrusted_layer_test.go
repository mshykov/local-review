package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// what was written. Config tests aren't parallel, so the global swap is safe.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	defer func() { _ = r.Close() }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yml")
	b := filepath.Join(dir, "b.yml")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !sameFile(a, a) {
		t.Error("identical path must be the same file")
	}
	if sameFile(a, b) {
		t.Error("distinct files must not be the same")
	}
	if sameFile(a, filepath.Join(dir, "missing.yml")) {
		t.Error("a missing file is not the same")
	}
	if sameFile("", a) || sameFile(a, "") {
		t.Error("empty path is never the same")
	}
	// A different path (symlink) to the same file IS the same file.
	link := filepath.Join(dir, "link.yml")
	if err := os.Symlink(a, link); err == nil {
		if !sameFile(a, link) {
			t.Error("a symlink to the same file must be detected as same")
		}
	}
}

// TestLoad_HomeConfigNotReprocessedAsUntrusted pins the v0.16.0 regression
// fix: when the project lives under $HOME with no project-local config,
// FindRepoConfig returns the same file as ~/.local-review.yml. That file is
// the trusted home config and must NOT be re-run through the untrusted repo
// layer (which would strip base_url/api_key_env and warn about the user's
// own file).
func TestLoad_HomeConfigNotReprocessedAsUntrusted(t *testing.T) {
	home := isolateHome(t)
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "")
	homeCfg := filepath.Join(home, ".local-review.yml")
	if err := os.WriteFile(homeCfg, []byte(`
llms:
  ollama:
    base_url: http://192.0.2.10:11434/v1
    model: qwen
`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate project-under-$HOME: repoConfigPath == the home config path.
	stderr := captureStderr(t, func() {
		cfg, err := Load(homeCfg)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.LLMs["ollama"].BaseURL; got != "http://192.0.2.10:11434/v1" {
			t.Errorf("home base_url must survive untouched, got %q", got)
		}
	})
	if strings.Contains(stderr, "ignoring security-sensitive") {
		t.Errorf("the user's own home config was wrongly treated as an untrusted repo layer:\n%s", stderr)
	}
}

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
    api_key_env: STEAL_THIS
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
	if got := cfg.LLMs["evilkey"].APIKeyEnv; got != "" {
		t.Errorf("repo api_key_env must be stripped (credential-source redirect), got %q", got)
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

// TestLoad_RepoConfigUntrusted_StripsWriteAndReadLocations pins the 2026-07
// hardening: an untrusted repo layer may not choose where review files are
// WRITTEN (storage.base_path) or point prompt-pack READS at an absolute
// location outside itself (prompts.pack_dir). Both strip with a warning;
// the built-in default survives.
func TestLoad_RepoConfigUntrusted_StripsWriteAndReadLocations(t *testing.T) {
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "")
	dir := t.TempDir()
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "outside"))
	if err != nil {
		t.Fatal(err)
	}
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
storage:
  base_path: /somewhere/attacker/chose
prompts:
  pack_dir: `+abs+`
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	var loadErr error
	stderr := captureStderr(t, func() { cfg, loadErr = Load(repoCfg) })
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if cfg.Storage.BasePath != ".local-review/reviews" {
		t.Errorf("storage.base_path = %q, want built-in default", cfg.Storage.BasePath)
	}
	if cfg.Prompts.PackDir != "" {
		t.Errorf("prompts.pack_dir = %q, want empty", cfg.Prompts.PackDir)
	}
	for _, want := range []string{"ignoring storage.base_path", "ignoring absolute prompts.pack_dir"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q, got:\n%s", want, stderr)
		}
	}
}

// TestLoad_RepoConfigUntrusted_WarnsButMergesHouseRules pins the
// warn-don't-strip contract for the advertised repo-level house-rules
// fields: prompts.prepend/append, review.exclude, and llms.*.enabled
// still MERGE from an untrusted repo layer (stripping would break the
// README's "Customise for your team" feature for every legitimate team
// repo), but a visible NOTE tells a CI operator the repo under review
// is shaping the review itself — a "**" exclude can green-light a
// hostile commit via "No changes to review" → exit 0.
func TestLoad_RepoConfigUntrusted_WarnsButMergesHouseRules(t *testing.T) {
	t.Setenv("LOCAL_REVIEW_TRUST_REPO_CONFIG", "")
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  prepend: "house rule: flag TODOs"
review:
  exclude: ["**"]
llms:
  codex:
    enabled: false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	var loadErr error
	stderr := captureStderr(t, func() { cfg, loadErr = Load(repoCfg) })
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if cfg.Prompts.Prepend != "house rule: flag TODOs" {
		t.Errorf("prompts.prepend = %q, want the house rule to merge", cfg.Prompts.Prepend)
	}
	if len(cfg.Review.ExcludeGlobs) != 1 || cfg.Review.ExcludeGlobs[0] != "**" {
		t.Errorf("review.exclude = %v, want [**] to merge", cfg.Review.ExcludeGlobs)
	}
	if cfg.LLMs["codex"].Enabled == nil || *cfg.LLMs["codex"].Enabled {
		t.Error("llms.codex.enabled=false should merge")
	}
	if !strings.Contains(stderr, "shapes this review") {
		t.Errorf("stderr missing the shaping NOTE, got:\n%s", stderr)
	}
	for _, want := range []string{"prompts.prepend", "review.exclude", "llms.codex.enabled=false"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("shaping NOTE missing %q, got:\n%s", want, stderr)
		}
	}
}
