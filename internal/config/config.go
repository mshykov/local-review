// Package config loads the cascading YAML config that drives local-review.
//
// Cascade (lowest precedence first):
//
//	1. Built-in defaults (compiled in)
//	2. Org config (optional URL, fetched + cached)  -- v1: stub, see TODO
//	3. ~/.local-review.yml                                 (per-user)
//	4. .local-review.yml (project root)                    (per-repo)
//	5. CLI flags                                     (per-invocation)
//
// Each layer is a partial YAML; later layers shallow-merge over earlier ones.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the resolved (post-cascade) configuration.
type Config struct {
	Provider Provider `yaml:"provider"`
	Review   Review   `yaml:"review"`
	Org      Org      `yaml:"org"`
}

// Provider holds LLM endpoint settings. Defaults to OpenAI; any
// OpenAI-compatible endpoint works (Anthropic via /v1/chat/completions,
// Together, Groq, OpenRouter, Ollama, vLLM, etc.).
type Provider struct {
	BaseURL string `yaml:"base_url"`         // e.g. https://api.openai.com/v1
	Model   string `yaml:"model"`            // e.g. gpt-4o, claude-3-5-sonnet
	APIKey  string `yaml:"api_key"`          // prefer env: LOCAL_REVIEW_API_KEY
	APIKeyEnv string `yaml:"api_key_env"`    // env var name to read; defaults to LOCAL_REVIEW_API_KEY
	TimeoutSec int  `yaml:"timeout_seconds"` // per-call timeout, default 60
}

// Review holds tuning knobs for what gets surfaced.
type Review struct {
	MinSeverity   string   `yaml:"min_severity"`    // "nit"|"info"|"warning"|"major"|"critical"
	MaxFindings   int      `yaml:"max_findings"`    // hard cap to avoid noise
	IncludeGlobs  []string `yaml:"include"`         // file globs to consider
	ExcludeGlobs  []string `yaml:"exclude"`         // file globs to drop
	PromptPack    string   `yaml:"prompt_pack"`     // override auto-detection
}

// Org is reserved for org-wide config delivery (v1: stub).
type Org struct {
	ConfigURL string `yaml:"config_url"`
}

// Defaults returns the built-in starting point.
func Defaults() Config {
	return Config{
		Provider: Provider{
			BaseURL:    "https://api.openai.com/v1",
			Model:      "gpt-4o-mini",
			APIKeyEnv:  "LOCAL_REVIEW_API_KEY",
			TimeoutSec: 60,
		},
		Review: Review{
			MinSeverity:  "warning",
			MaxFindings:  20,
			ExcludeGlobs: []string{"**/*.lock", "**/*.snap", "**/dist/**", "**/build/**"},
		},
	}
}

// Load resolves the cascade.
//
// repoConfigPath is the path to the project-level .local-review.yml (typically
// found by walking up from cwd). Either path may be empty / missing.
func Load(repoConfigPath string) (Config, error) {
	cfg := Defaults()

	// User config (~/.local-review.yml)
	if home, err := os.UserHomeDir(); err == nil {
		if err := mergeFrom(&cfg, filepath.Join(home, ".local-review.yml")); err != nil {
			return cfg, fmt.Errorf("load user config: %w", err)
		}
	}

	// Repo config
	if repoConfigPath != "" {
		if err := mergeFrom(&cfg, repoConfigPath); err != nil {
			return cfg, fmt.Errorf("load repo config (%s): %w", repoConfigPath, err)
		}
	}

	// Env-driven API key — checked here so we fail fast if missing
	resolveAPIKey(&cfg)

	return cfg, nil
}

// FindRepoConfig walks up from start looking for a .local-review.yml. Returns
// "" when none is found (not an error).
func FindRepoConfig(start string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, ".local-review.yml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// mergeFrom reads YAML from path (if it exists) and shallow-merges
// non-zero fields into dst. Missing files are not an error.
func mergeFrom(dst *Config, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var layer Config
	if err := yaml.Unmarshal(b, &layer); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	merge(dst, layer)
	return nil
}

// merge does a shallow overlay: any non-zero field in src replaces dst.
// Slices are replaced wholesale (not appended) so users can override
// defaults like ExcludeGlobs cleanly.
func merge(dst *Config, src Config) {
	if src.Provider.BaseURL != "" {
		dst.Provider.BaseURL = src.Provider.BaseURL
	}
	if src.Provider.Model != "" {
		dst.Provider.Model = src.Provider.Model
	}
	if src.Provider.APIKey != "" {
		dst.Provider.APIKey = src.Provider.APIKey
	}
	if src.Provider.APIKeyEnv != "" {
		dst.Provider.APIKeyEnv = src.Provider.APIKeyEnv
	}
	if src.Provider.TimeoutSec != 0 {
		dst.Provider.TimeoutSec = src.Provider.TimeoutSec
	}
	if src.Review.MinSeverity != "" {
		dst.Review.MinSeverity = src.Review.MinSeverity
	}
	if src.Review.MaxFindings != 0 {
		dst.Review.MaxFindings = src.Review.MaxFindings
	}
	if len(src.Review.IncludeGlobs) > 0 {
		dst.Review.IncludeGlobs = src.Review.IncludeGlobs
	}
	if len(src.Review.ExcludeGlobs) > 0 {
		dst.Review.ExcludeGlobs = src.Review.ExcludeGlobs
	}
	if src.Review.PromptPack != "" {
		dst.Review.PromptPack = src.Review.PromptPack
	}
	if src.Org.ConfigURL != "" {
		dst.Org.ConfigURL = src.Org.ConfigURL
	}
}

// resolveAPIKey fills cfg.Provider.APIKey from the named env var when
// it isn't already set in YAML.
func resolveAPIKey(cfg *Config) {
	if cfg.Provider.APIKey != "" {
		return
	}
	envName := cfg.Provider.APIKeyEnv
	if envName == "" {
		envName = "LOCAL_REVIEW_API_KEY"
	}
	cfg.Provider.APIKey = os.Getenv(envName)
}
