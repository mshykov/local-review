package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mshykov/local-review/internal/config"
	"gopkg.in/yaml.v3"
)

// TestRenderConfig_ReproducesAllDefaultExcludeGlobs pins the sync between the
// wizard's emitted exclude list and config.Defaults(). The cascade merge
// replaces ExcludeGlobs WHOLESALE, so a wizard-generated config must reproduce
// every built-in default (it may add more, e.g. node_modules). If a new
// default lands in config.Defaults() but not in renderConfig, the wizard would
// silently drop it — this test catches that drift.
func TestRenderConfig_ReproducesAllDefaultExcludeGlobs(t *testing.T) {
	yml := renderConfig("provider", "https://x/v1", "m", "ENV", "warning", 20)
	var parsed struct {
		Review struct {
			Exclude []string `yaml:"exclude"`
		} `yaml:"review"`
	}
	if err := unmarshalYAML([]byte(yml), &parsed); err != nil {
		t.Fatalf("generated config is not valid YAML: %v", err)
	}
	got := make(map[string]bool, len(parsed.Review.Exclude))
	for _, g := range parsed.Review.Exclude {
		got[g] = true
	}
	for _, want := range config.Defaults().Review.ExcludeGlobs {
		if !got[want] {
			t.Errorf("wizard config omits built-in default exclude %q — merge replaces wholesale so it would be lost; add it to renderConfig", want)
		}
	}
}

// unmarshalYAML wraps yaml.Unmarshal so the round-trip test reads
// naturally without an extra import everywhere.
func unmarshalYAML(b []byte, v any) error { return yaml.Unmarshal(b, v) }

// presetMenuChoice returns the 1-based menu number for the preset with
// the given presetName, derived from providerPresets so tests don't break
// when the menu is reordered or new providers are inserted (CLAUDE.md rule
// 9: encode the invariant — "pick the Ollama preset" — not a magic number).
func presetMenuChoice(t *testing.T, name string) string {
	t.Helper()
	for i, p := range providerPresets {
		if p.presetName == name {
			return strconv.Itoa(i + 1)
		}
	}
	t.Fatalf("no provider preset named %q", name)
	return ""
}

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
		`base_url: "https://api.openai.com/v1"`,
		`model: "gpt-4o-mini"`,
		`api_key_env: "OPENAI_API_KEY"`,
		`min_severity: "warning"`,
		"max_findings: 20",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config missing %q\nfull config:\n%s", want, content)
		}
	}
}

func TestInit_AnthropicPreset(t *testing.T) {
	// Choice 2 = Anthropic. Then accept defaults, confirm write.
	input := "2\n\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("init failed: %v\nstdout:\n%s", err, stdout)
	}
	for _, want := range []string{
		`base_url: "https://api.anthropic.com/v1"`,
		`api_key_env: "ANTHROPIC_API_KEY"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("Anthropic config missing %q\nfull config:\n%s", want, content)
		}
	}
}

func TestInit_OllamaSkipsAPIKeyEnv(t *testing.T) {
	// Pick the Ollama preset (no API key), accept defaults, confirm write.
	input := presetMenuChoice(t, "ollama") + "\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("init failed: %v\nstdout:\n%s", err, stdout)
	}
	if strings.Contains(content, "api_key_env:") {
		t.Errorf("Ollama should not require an API key env var:\n%s", content)
	}
	if !strings.Contains(content, `base_url: "http://localhost:11434/v1"`) {
		t.Errorf("expected Ollama base_url:\n%s", content)
	}
	if strings.Contains(stdout, "Set your API key:") {
		t.Errorf("Ollama path should not prompt to set API key:\n%s", stdout)
	}
}

func TestInit_CustomProviderRePromptsForBaseURL(t *testing.T) {
	// Pick the "Other" (custom) preset; first base URL blank (re-prompt),
	// then valid URL. Then accept default model, env var, severity,
	// max-findings, confirm.
	input := presetMenuChoice(t, "provider") + "\n\nhttps://my-llm.example.com/v1\n\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("expected re-prompt to recover, got: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "(required)") {
		t.Errorf("expected (required) hint when blank base URL given, got:\n%s", stdout)
	}
	if !strings.Contains(content, `base_url: "https://my-llm.example.com/v1"`) {
		t.Errorf("expected custom base_url in config:\n%s", content)
	}
}

func TestInit_CustomProviderGivesUpOnRepeatedBlankBaseURL(t *testing.T) {
	// Pick the "Other" (custom) preset. Five blank answers in a row should
	// give up gracefully.
	input := presetMenuChoice(t, "provider") + "\n" + strings.Repeat("\n", 6)
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "too many empty answers") {
		t.Errorf("expected give-up error after max empty answers, got: %v", err)
	}
}

func TestInit_InvalidProviderChoiceRePrompts(t *testing.T) {
	// Bad choice "99" should re-prompt and accept "1" on the second try.
	// Inputs after that: model, key env, severity, max-findings, confirm.
	input := "99\n1\n\n\n\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("expected re-prompt to recover, got: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "choice must be") {
		t.Errorf("expected friendly error in output, got:\n%s", stdout)
	}
	if !strings.Contains(content, `base_url: "https://api.openai.com/v1"`) {
		t.Errorf("recovery should have produced an OpenAI config:\n%s", content)
	}
}

func TestInit_InvalidProviderChoiceGivesUpAfterMaxRetries(t *testing.T) {
	// Five bad answers in a row should give up rather than loop forever.
	input := strings.Repeat("99\n", 6)
	_, _, _, err := runInitTo(t, input, false)
	if err == nil || !strings.Contains(err.Error(), "too many invalid") {
		t.Errorf("expected give-up error after max retries, got: %v", err)
	}
}

func TestInit_InvalidSeverityRePrompts(t *testing.T) {
	// OpenAI defaults, then "bogus" severity, then valid "warning".
	input := "1\n\n\nbogus\nwarning\n\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("expected re-prompt to recover, got: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "must be one of") {
		t.Errorf("expected friendly error in output, got:\n%s", stdout)
	}
	if !strings.Contains(content, `min_severity: "warning"`) {
		t.Errorf("expected warning severity in config:\n%s", content)
	}
}

func TestInit_InvalidMaxFindingsRePrompts(t *testing.T) {
	// OpenAI defaults, severity ok, then "NaN", then "20".
	input := "1\n\n\n\nNaN\n20\ny\n"
	stdout, content, _, err := runInitTo(t, input, false)
	if err != nil {
		t.Fatalf("expected re-prompt to recover, got: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "must be a positive integer") {
		t.Errorf("expected friendly error in output, got:\n%s", stdout)
	}
	if !strings.Contains(content, "max_findings: 20") {
		t.Errorf("expected max_findings in config:\n%s", content)
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
	// Seed the existing file with broader perms so we actually exercise
	// the post-write Chmod path (os.WriteFile alone doesn't tighten
	// perms on overwrite).
	if err := os.WriteFile(target, []byte("# pre-existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --force=true: skip BOTH the overwrite confirmation and the final
	// write confirmation. Inputs are just the answered questions.
	input := "1\n\n\n\n\n"
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
	if !strings.Contains(string(got), `base_url: "https://api.openai.com/v1"`) {
		t.Errorf("--force write produced unexpected content:\n%s", string(got))
	}
	// Regression: --force overwriting an existing file must lock the
	// mode back to 0600. os.WriteFile alone won't do this — we Chmod
	// explicitly in runInit.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode after --force overwrite: got %o, want 600", info.Mode().Perm())
	}
}

func TestInit_ForceWithoutExistingFileWritesNormally(t *testing.T) {
	// --force should be a no-op when there's nothing to overwrite.
	// Final-write confirmation also skipped, so no trailing y.
	dir := t.TempDir()
	target := filepath.Join(dir, ".local-review.yml")
	input := "1\n\n\n\n\n"
	out := &bytes.Buffer{}
	in := strings.NewReader(input)
	if err := runInit(out, in, target, true); err != nil {
		t.Fatalf("init with --force on non-existent file failed: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), `base_url: "https://api.openai.com/v1"`) {
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
	// Use filepath.Base so the assertion is portable: filepath.Join
	// returns OS-specific separators (\\ on Windows, / on Unix).
	if filepath.Base(got) != ".local-review.yml" {
		t.Errorf(`resolveTarget("global") = %q, want basename ".local-review.yml"`, got)
	}
}

func TestRenderConfig_Shape(t *testing.T) {
	yml := renderConfig("openai", "https://api.openai.com/v1", "gpt-4o-mini", "OPENAI_API_KEY", "warning", 20)
	for _, want := range []string{
		"# .local-review.yml — generated by `local-review init`.",
		"llms:",
		"  openai:",
		`    base_url: "https://api.openai.com/v1"`,
		`    model: "gpt-4o-mini"`,
		`    api_key_env: "OPENAI_API_KEY"`,
		"    timeout_seconds: 60",
		"review:",
		`  min_severity: "warning"`,
		"  max_findings: 20",
		"  exclude:",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("renderConfig missing %q\nfull output:\n%s", want, yml)
		}
	}
	// v0.15 hard guarantee: the wizard MUST NOT emit the legacy
	// top-level `provider:` block (a stale v0.14 wizard would be
	// caught by Load's migration error, but better to never produce
	// it in the first place).
	for _, illegal := range []string{"\nprovider:\n", "provider:\n  base_url"} {
		if strings.Contains(yml, illegal) {
			t.Errorf("renderConfig produced removed `provider:` block fragment %q:\n%s", illegal, yml)
		}
	}
}

func TestRenderConfig_QuotesProtectAgainstYAMLEdgeCases(t *testing.T) {
	// Inputs containing #, leading reserved chars, or other YAML-special
	// content should still produce a valid YAML config that parses back
	// to the original string values.
	yml := renderConfig(
		"openai",
		"https://api.example.com/v1",
		"gpt-4o-mini#preview", // # is a YAML comment marker if unquoted
		"MY_KEY",
		"warning",
		20,
	)
	if !strings.Contains(yml, `model: "gpt-4o-mini#preview"`) {
		t.Errorf("model with # not properly quoted:\n%s", yml)
	}
	// Round-trip via yaml.v3 to confirm it parses cleanly.
	type llmCfg struct {
		Model string `yaml:"model"`
	}
	type cfg struct {
		LLMs map[string]llmCfg `yaml:"llms"`
	}
	var c cfg
	if err := unmarshalYAML([]byte(yml), &c); err != nil {
		t.Fatalf("rendered YAML failed to parse: %v\n%s", err, yml)
	}
	if c.LLMs["openai"].Model != "gpt-4o-mini#preview" {
		t.Errorf("model round-trip failed: got %q, want %q", c.LLMs["openai"].Model, "gpt-4o-mini#preview")
	}
}

func TestRenderConfig_OmitsAPIKeyEnvWhenEmpty(t *testing.T) {
	yml := renderConfig("ollama", "http://localhost:11434/v1", "qwen2.5-coder:7b", "", "warning", 20)
	if strings.Contains(yml, "api_key_env:") {
		t.Errorf("api_key_env should be omitted when empty:\n%s", yml)
	}
}

func TestProviderPresets_NamesAreYAMLSafe(t *testing.T) {
	// v0.15 self-review (codex): presetName is emitted verbatim as a
	// YAML key under `llms:`. Currently every preset name is a
	// lowercase ASCII identifier so the output is unambiguous, but
	// nothing in renderConfig validates that. This test pins the
	// invariant — a future contributor who adds a preset with a
	// space, colon, or other YAML-special character will trip it
	// here rather than producing a stealth-malformed config.
	yamlKey := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	for _, p := range providerPresets {
		if !yamlKey.MatchString(p.presetName) {
			t.Errorf("preset %q has unsafe presetName %q — must match %s so it round-trips as a YAML key without quoting", p.label, p.presetName, yamlKey.String())
		}
	}
}

func TestRenderConfig_PresetNameDrivesAgentKey(t *testing.T) {
	// Each preset's free-form name becomes the `llms.<name>:` key
	// in the output. v0.15 added this — pre-fix the wizard always
	// wrote `provider:`, which gave users no signal that the entry
	// was renameable.
	cases := map[string]string{
		"openai":    "  openai:",
		"ollama":    "  ollama:",
		"anthropic": "  anthropic:",
		"mistral":   "  mistral:",
	}
	for name, want := range cases {
		yml := renderConfig(name, "http://x/v1", "m", "K", "warning", 20)
		if !strings.Contains(yml, want) {
			t.Errorf("renderConfig(%q) missing key line %q\n%s", name, want, yml)
		}
	}
}
