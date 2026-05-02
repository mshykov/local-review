package config

import (
	"os"
	"path/filepath"
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
