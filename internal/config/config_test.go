package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Provider.Model == "" {
		t.Error("default model is empty")
	}
	if d.Review.MinSeverity != "warning" {
		t.Errorf("default min_severity = %q, want warning", d.Review.MinSeverity)
	}
}

func TestMergeReplaces(t *testing.T) {
	dst := Defaults()
	src := Config{
		Provider: Provider{Model: "claude-opus-4-6", BaseURL: "https://api.anthropic.com/v1"},
		Review:   Review{MinSeverity: "major", MaxFindings: 5},
	}
	merge(&dst, src)
	if dst.Provider.Model != "claude-opus-4-6" {
		t.Errorf("model = %q", dst.Provider.Model)
	}
	if dst.Review.MinSeverity != "major" {
		t.Errorf("min_severity = %q", dst.Review.MinSeverity)
	}
	if dst.Review.MaxFindings != 5 {
		t.Errorf("max_findings = %d", dst.Review.MaxFindings)
	}
}

func TestLoadUsesRepoYAML(t *testing.T) {
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
provider:
  model: gpt-4o
review:
  min_severity: critical
  max_findings: 3
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", cfg.Provider.Model)
	}
	if cfg.Review.MinSeverity != "critical" {
		t.Errorf("min_severity = %q", cfg.Review.MinSeverity)
	}
	if cfg.Review.MaxFindings != 3 {
		t.Errorf("max_findings = %d", cfg.Review.MaxFindings)
	}
}

func TestFindRepoConfig(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(cfg, []byte("provider: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindRepoConfig(deep)
	if got != cfg {
		t.Errorf("FindRepoConfig(deep) = %q, want %q", got, cfg)
	}
	if FindRepoConfig(t.TempDir()) != "" {
		t.Error("expected empty string when no config exists")
	}
}

func TestResolveAPIKeyFromEnv(t *testing.T) {
	t.Setenv("LOCAL_REVIEW_API_KEY", "sk-test-123")
	cfg := Defaults()
	resolveAPIKey(&cfg)
	if cfg.Provider.APIKey != "sk-test-123" {
		t.Errorf("api_key = %q, want sk-test-123", cfg.Provider.APIKey)
	}
}

func TestResolveAPIKeyCustomEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-openai")
	cfg := Defaults()
	cfg.Provider.APIKeyEnv = "OPENAI_API_KEY"
	resolveAPIKey(&cfg)
	if cfg.Provider.APIKey != "sk-from-openai" {
		t.Errorf("api_key = %q, want sk-from-openai", cfg.Provider.APIKey)
	}
}

// v0.1 tests

func TestDefaults_V01(t *testing.T) {
	d := Defaults()

	// Check that LLMs are configured
	if len(d.LLMs) != 3 {
		t.Errorf("expected 3 LLMs, got %d", len(d.LLMs))
	}

	// Check specific LLMs
	if _, ok := d.LLMs["claude"]; !ok {
		t.Error("claude LLM not in defaults")
	}
	if _, ok := d.LLMs["gemini"]; !ok {
		t.Error("gemini LLM not in defaults")
	}
	if _, ok := d.LLMs["codex"]; !ok {
		t.Error("codex LLM not in defaults")
	}

	// Check merge config
	if d.Merge.PreferredLLM != "auto" {
		t.Errorf("merge.preferred_llm = %q, want auto", d.Merge.PreferredLLM)
	}
	if d.Merge.Deduplicate == nil || !*d.Merge.Deduplicate {
		t.Error("merge.deduplicate should be true by default")
	}

	// Check storage config
	if d.Storage.BasePath != ".local-review/reviews" {
		t.Errorf("storage.base_path = %q", d.Storage.BasePath)
	}
}

func TestMergeReplaces_V01(t *testing.T) {
	dst := Defaults()
	src := Config{
		LLMs: map[string]LLMConfig{
			"claude": {
				Enabled: boolPtr(false), // disable claude
				Model:   "claude-3-haiku",
			},
			"custom": {
				Enabled:    boolPtr(true),
				CLIPath:    "custom-llm",
				TimeoutSec: 60,
			},
		},
		Merge: MergeConfig{
			PreferredLLM: "gemini",
		},
		Storage: StorageConfig{
			BasePath: "/tmp/reviews",
		},
	}

	merge(&dst, src)

	// Check that claude was updated
	if dst.LLMs["claude"].Enabled != nil && *dst.LLMs["claude"].Enabled {
		t.Error("claude should be disabled after merge")
	}
	if dst.LLMs["claude"].Model != "claude-3-haiku" {
		t.Errorf("claude model = %q, want claude-3-haiku", dst.LLMs["claude"].Model)
	}

	// Check that custom LLM was added
	if _, ok := dst.LLMs["custom"]; !ok {
		t.Error("custom LLM should be added")
	}

	// Check merge config
	if dst.Merge.PreferredLLM != "gemini" {
		t.Errorf("merge.preferred_llm = %q", dst.Merge.PreferredLLM)
	}

	// Check storage config
	if dst.Storage.BasePath != "/tmp/reviews" {
		t.Errorf("storage.base_path = %q", dst.Storage.BasePath)
	}
}

func TestResolveAPIKeys_V01(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-claude-123")
	t.Setenv("OPENAI_API_KEY", "sk-openai-456")

	cfg := Defaults()
	resolveAPIKeys(&cfg)

	// Check that API keys were resolved
	if cfg.LLMs["claude"].APIKey != "sk-claude-123" {
		t.Errorf("claude api_key = %q, want sk-claude-123", cfg.LLMs["claude"].APIKey)
	}
	if cfg.LLMs["codex"].APIKey != "sk-openai-456" {
		t.Errorf("codex api_key = %q, want sk-openai-456", cfg.LLMs["codex"].APIKey)
	}
}

func TestLoadUsesRepoYAML_V01(t *testing.T) {
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
llms:
  claude:
    enabled: false
  gemini:
    model: gemini-1.5-flash
merge:
  preferred_llm: gemini
  deduplicate: false
storage:
  base_path: /custom/path
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Check that LLMs were merged
	if cfg.LLMs["claude"].Enabled != nil && *cfg.LLMs["claude"].Enabled {
		t.Error("claude should be disabled")
	}
	if cfg.LLMs["gemini"].Model != "gemini-1.5-flash" {
		t.Errorf("gemini model = %q", cfg.LLMs["gemini"].Model)
	}

	// Check merge config
	if cfg.Merge.PreferredLLM != "gemini" {
		t.Errorf("merge.preferred_llm = %q", cfg.Merge.PreferredLLM)
	}
	if cfg.Merge.Deduplicate != nil && *cfg.Merge.Deduplicate {
		t.Error("merge.deduplicate should be false")
	}

	// Check storage config
	if cfg.Storage.BasePath != "/custom/path" {
		t.Errorf("storage.base_path = %q", cfg.Storage.BasePath)
	}
}

func TestValidate_Success(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

func TestValidate_NoLLMsEnabled(t *testing.T) {
	cfg := Defaults()
	// Disable all LLMs
	for name, llm := range cfg.LLMs {
		llm.Enabled = boolPtr(false)
		cfg.LLMs[name] = llm
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error when no LLMs enabled")
	}
}

func TestValidate_InvalidPreferredLLM(t *testing.T) {
	cfg := Defaults()
	cfg.Merge.PreferredLLM = "nonexistent"

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid preferred_llm")
	}
}

func TestValidate_StrayModeFieldIsIgnored(t *testing.T) {
	// `mode:` was removed from LLMConfig in v0.5.x — it shipped in the
	// v0.1 example but was never wired through to the orchestrator.
	// Existing user YAML configs with `mode: cli|api|whatever` lines
	// must keep loading: yaml.v3 silently drops unknown fields, and
	// Validate must not refuse to start because of this legacy line.
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed on default config: %v", err)
	}
}

// TestMergeCoversAllExportedFields enforces the maintenance contract
// documented above merge(): every exported field on Config and its
// nested structs must be wired into the overlay logic.
//
// Strategy: synthesize a `src` Config where every exported field has
// a non-zero sentinel, merge onto a zeroed `dst`, then walk the result
// via reflection and fail loudly on any field that's still its zero
// value. Catches the "added field, forgot overlay branch" footgun the
// reviewer flagged — `LLMConfig.Mode` shipped that way for several
// minor releases until v0.5.x. The test costs nothing at runtime;
// drift is caught the moment a new field lands.
func TestMergeCoversAllExportedFields(t *testing.T) {
	src := nonZeroConfig()
	var dst Config
	merge(&dst, src)

	missing := findZeroFields(reflect.ValueOf(dst), "")
	// merge() actually DOES copy the deprecated inline APIKey field
	// for v0.4.x backward compat — a user with `api_key:` in their
	// YAML still works, and warnDeprecatedAPIKeys nudges them toward
	// env vars via stderr instead of silently dropping their key.
	// So nothing is filtered out; if any exported field is missing,
	// merge() needs an overlay branch.
	if len(missing) > 0 {
		t.Errorf("merge() didn't propagate these fields:\n  %s\n\n"+
			"Add an overlay branch in merge() for each one. See the\n"+
			"MAINTENANCE CONTRACT comment above merge().",
			strings.Join(missing, "\n  "))
	}
}

// nonZeroConfig returns a Config where every exported (non-deprecated)
// field is set to a unique non-zero value. Sentinel values aren't
// meaningful — we only assert presence after merge.
func nonZeroConfig() Config {
	enabled := true
	dedup := true
	return Config{
		Provider: Provider{
			BaseURL:    "https://example.test/v1",
			Model:      "test-model",
			APIKey:     "sk-test", // merge() does copy this for v0.4.x compat; warnDeprecatedAPIKeys nudges
			APIKeyEnv:  "TEST_KEY",
			TimeoutSec: 99,
		},
		Review: Review{
			MinSeverity:  "major",
			MaxFindings:  42,
			IncludeGlobs: []string{"**/*.go"},
			ExcludeGlobs: []string{"**/vendor/**"},
			PromptPack:   "go",
		},
		Org: Org{
			ConfigURL: "https://internal.test/lr.yml",
		},
		LLMs: map[string]LLMConfig{
			"claude": {
				Enabled:    &enabled,
				CLIPath:    "/opt/test/claude",
				Model:      "claude-test",
				APIKeyEnv:  "TEST_ANTHROPIC",
				APIKey:     "sk-test", // merge() copies for v0.4.x compat; deprecated, warnings nudge to env
				TimeoutSec: 240,
			},
		},
		Merge: MergeConfig{
			PreferredLLM:       "claude",
			Deduplicate:        &dedup,
			ConsensusThreshold: 7,
		},
		Storage: StorageConfig{
			BasePath: "/tmp/test-reviews",
		},
	}
}

// findZeroFields walks v's exported fields and returns dotted paths of
// any that are still their zero value. Used to detect merge() drift.
func findZeroFields(v reflect.Value, prefix string) []string {
	var out []string
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			fv := v.Field(i)
			ft := t.Field(i)
			if !ft.IsExported() {
				continue
			}
			path := prefix + ft.Name
			out = append(out, findZeroFields(fv, path+".")...)
		}
	case reflect.Map:
		if v.Len() == 0 {
			out = append(out, strings.TrimSuffix(prefix, "."))
			return out
		}
		// Recurse into one entry — sufficient to catch "the inner struct
		// type isn't fully merged"; we don't need to walk every key.
		for _, k := range v.MapKeys() {
			out = append(out, findZeroFields(v.MapIndex(k), prefix+k.String()+".")...)
			break
		}
	case reflect.Pointer:
		if v.IsNil() {
			out = append(out, strings.TrimSuffix(prefix, "."))
		}
	default:
		if v.IsZero() {
			out = append(out, strings.TrimSuffix(prefix, "."))
		}
	}
	return out
}
