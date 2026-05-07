// Package config loads the cascading YAML config that drives local-review.
//
// Cascade (lowest precedence first):
//
//  1. Built-in defaults (compiled in)
//  2. Org config (optional URL, fetched + cached)  -- v1: stub, see TODO
//  3. ~/.local-review.yml                                 (per-user)
//  4. .local-review.yml (project root)                    (per-repo)
//  5. CLI flags                                     (per-invocation)
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
	Provider Provider `yaml:"provider"` // v0: single-LLM API mode
	Review   Review   `yaml:"review"`
	Org      Org      `yaml:"org"`

	// v0.1: multi-LLM support
	LLMs    map[string]LLMConfig `yaml:"llms"`
	Merge   MergeConfig          `yaml:"merge"`
	Storage StorageConfig        `yaml:"storage"`
}

// Provider holds LLM endpoint settings. Defaults to OpenAI; any
// OpenAI-compatible endpoint works (Anthropic via /v1/chat/completions,
// Together, Groq, OpenRouter, Ollama, vLLM, etc.).
type Provider struct {
	BaseURL    string `yaml:"base_url"`        // e.g. https://api.openai.com/v1
	Model      string `yaml:"model"`           // e.g. gpt-4o, claude-3-5-sonnet
	APIKey     string `yaml:"api_key"`         // DEPRECATED: use environment variable instead
	APIKeyEnv  string `yaml:"api_key_env"`     // env var name to read; defaults to LOCAL_REVIEW_API_KEY
	TimeoutSec int    `yaml:"timeout_seconds"` // per-call timeout, default 60
}

// Review holds tuning knobs for what gets surfaced.
type Review struct {
	MinSeverity  string   `yaml:"min_severity"` // "nit"|"info"|"warning"|"major"|"critical"
	MaxFindings  int      `yaml:"max_findings"` // hard cap to avoid noise
	IncludeGlobs []string `yaml:"include"`      // file globs to consider
	ExcludeGlobs []string `yaml:"exclude"`      // file globs to drop
	PromptPack   string   `yaml:"prompt_pack"`  // override auto-detection
}

// Org is reserved for org-wide config delivery (v1: stub).
type Org struct {
	ConfigURL string `yaml:"config_url"`
}

// LLMConfig holds configuration for a single LLM (v0.1+).
//
// Note: a `mode: cli|api` field shipped in v0.1's example config but
// was never wired through to the orchestrator (multi-LLM always
// invokes via CLI). It was removed in v0.5.x. Existing YAML configs
// with a `mode:` line still load — yaml.v3 silently ignores unknown
// fields. The "API fallback when CLI auth fails" idea is parked in
// do-not-merge/v06-fully-local-ollama-preset.md.
type LLMConfig struct {
	Enabled    *bool  `yaml:"enabled"`
	CLIPath    string `yaml:"cli_path"`        // path to CLI binary (auto-detect if empty)
	Model      string `yaml:"model"`           // model name passed to the agent CLI
	APIKeyEnv  string `yaml:"api_key_env"`     // env var name for API key
	APIKey     string `yaml:"api_key"`         // DEPRECATED: use environment variable instead
	TimeoutSec int    `yaml:"timeout_seconds"` // per-call timeout
}

// MergeConfig controls how multi-LLM reviews are merged (v0.1+).
type MergeConfig struct {
	PreferredLLM       string `yaml:"preferred_llm"`       // "auto" or specific LLM name
	Deduplicate        *bool  `yaml:"deduplicate"`         // remove duplicate findings
	ConsensusThreshold int    `yaml:"consensus_threshold"` // N LLMs agreeing = "Confirmed by N"
}

// StorageConfig controls where reviews are saved (v0.1+).
type StorageConfig struct {
	BasePath string `yaml:"base_path"` // base directory for reviews
}

// boolPtr returns a pointer to a bool value (helper for defaults).
func boolPtr(b bool) *bool {
	return &b
}

// Defaults returns the built-in starting point.
func Defaults() Config {
	return Config{
		// v0: single-LLM API mode defaults
		Provider: Provider{
			BaseURL:   "https://api.openai.com/v1",
			Model:     "gpt-4o-mini",
			APIKeyEnv: "LOCAL_REVIEW_API_KEY",
			// 10 minutes. The v0 single-LLM API path is usually fast,
			// but a long thinking-model response on a big diff can run
			// 3-5 min; user feedback after v0.6.3 was that the prior
			// 60s default surfaced as confusing timeouts on real branch
			// reviews. Per-config override still works for users who
			// want shorter timeouts.
			TimeoutSec: 600,
		},
		Review: Review{
			MinSeverity:  "warning",
			MaxFindings:  20,
			ExcludeGlobs: []string{"**/*.lock", "**/*.snap", "**/dist/**", "**/build/**"},
		},

		// v0.1: multi-LLM defaults.
		//
		// Model is intentionally empty for every agent. We rely on the
		// vendor CLI's own current default rather than hardcoding model
		// IDs in our config — those go stale within months (the v0.1
		// defaults pinned claude-3-5-sonnet-20241022, gemini-1.5-pro,
		// and gpt-4, all 12-24 months out of date by v0.6.x). Each
		// invoker (internal/cli/invoker.go) only passes --model when
		// non-empty, so an empty default leaves the CLI on whatever it
		// currently considers stable. Users who want to pin a specific
		// model should set `model:` explicitly in .local-review.yml.
		// Per-LLM timeout default is 10 minutes. The pre-v0.6.4 default
		// of 120s surfaced as user-reported "timeout" failures on the
		// most-used review path (`local-review review` against a
		// branch's full diff): gemini and codex were finishing in 80–
		// 100s but claude (sonnet on a thinking model) regularly took
		// 2–5 min on the same diff and timed out. 600s gives enough
		// headroom for a worst-case agent on a worst-case diff while
		// still failing fast on a genuinely hung subprocess. Users
		// can lower per-agent via `llms.<agent>.timeout_seconds:`.
		LLMs: map[string]LLMConfig{
			"claude": {
				Enabled:    boolPtr(true),
				CLIPath:    "claude",
				APIKeyEnv:  "ANTHROPIC_API_KEY",
				TimeoutSec: 600,
			},
			"gemini": {
				Enabled:    boolPtr(true),
				CLIPath:    "gemini",
				APIKeyEnv:  "GEMINI_API_KEY",
				TimeoutSec: 600,
			},
			"codex": {
				// Enabled is intentionally nil — defaults to "run if active".
				// codex is paid (ChatGPT Plus or pay-per-token via OPENAI_API_KEY),
				// but we only invoke it when the user has explicitly authenticated,
				// so running by default doesn't surprise anyone with a bill.
				CLIPath:    "codex",
				APIKeyEnv:  "OPENAI_API_KEY",
				TimeoutSec: 600,
			},
		},
		Merge: MergeConfig{
			PreferredLLM:       "auto",
			Deduplicate:        boolPtr(true),
			ConsensusThreshold: 3,
		},
		Storage: StorageConfig{
			BasePath: ".local-review/reviews",
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

	// Warn about deprecated api_key in YAML (security risk)
	warnDeprecatedAPIKeys(&cfg)

	// Env-driven API keys — resolve for v0 and v0.1
	resolveAPIKey(&cfg)
	resolveAPIKeys(&cfg)

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
//
// ⚠ MAINTENANCE CONTRACT ⚠
//
// This function manually walks every Config field. A reflection-based
// merger (mergo, etc.) would be terser but the project deliberately
// avoids vendor SDKs / heavy reflection to keep the binary small and
// the cascade behavior auditable. The cost is: when you add a field
// to Config / Provider / Review / LLMConfig / MergeConfig / StorageConfig,
// you MUST add a corresponding overlay branch here, or user overrides
// for that field will silently no-op.
//
// History: this contract was violated in v0.1 — `LLMConfig.Mode` was
// added to the schema but never wired through, leaving the documented
// `mode: api` config inert until v0.5.x.
//
// TestMergeCoversAllExportedFields (config_test.go) uses reflection to
// fail at test time when a new exported field is added without an
// overlay branch here. Don't bypass the test — extend merge() instead.
func merge(dst *Config, src Config) {
	// v0: Provider settings
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

	// Review settings
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

	// Org settings
	if src.Org.ConfigURL != "" {
		dst.Org.ConfigURL = src.Org.ConfigURL
	}

	// v0.1: LLMs settings (per-LLM merge)
	if len(src.LLMs) > 0 {
		if dst.LLMs == nil {
			dst.LLMs = make(map[string]LLMConfig)
		}
		for name, llmCfg := range src.LLMs {
			// If LLM exists in dst, merge fields
			if existing, ok := dst.LLMs[name]; ok {
				if llmCfg.CLIPath != "" {
					existing.CLIPath = llmCfg.CLIPath
				}
				if llmCfg.Model != "" {
					existing.Model = llmCfg.Model
				}
				if llmCfg.APIKeyEnv != "" {
					existing.APIKeyEnv = llmCfg.APIKeyEnv
				}
				if llmCfg.APIKey != "" {
					existing.APIKey = llmCfg.APIKey
				}
				if llmCfg.TimeoutSec != 0 {
					existing.TimeoutSec = llmCfg.TimeoutSec
				}
				// Enabled is a *bool, only override if explicitly set in src
				if llmCfg.Enabled != nil {
					existing.Enabled = llmCfg.Enabled
				}

				dst.LLMs[name] = existing
			} else {
				// New LLM, add it
				dst.LLMs[name] = llmCfg
			}
		}
	}

	// v0.1: Merge settings
	if src.Merge.PreferredLLM != "" {
		dst.Merge.PreferredLLM = src.Merge.PreferredLLM
	}
	// Deduplicate is a *bool, only override if explicitly set in src
	if src.Merge.Deduplicate != nil {
		dst.Merge.Deduplicate = src.Merge.Deduplicate
	}
	if src.Merge.ConsensusThreshold != 0 {
		dst.Merge.ConsensusThreshold = src.Merge.ConsensusThreshold
	}

	// v0.1: Storage settings
	if src.Storage.BasePath != "" {
		dst.Storage.BasePath = src.Storage.BasePath
	}
}

// resolveAPIKey fills cfg.Provider.APIKey from the named env var when
// it isn't already set in YAML (v0 compatibility).
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

// resolveAPIKeys fills API keys for all LLMs from their configured env vars (v0.1).
func resolveAPIKeys(cfg *Config) {
	for name, llmCfg := range cfg.LLMs {
		// Skip if API key already set
		if llmCfg.APIKey != "" {
			continue
		}

		// Skip if no env var configured
		if llmCfg.APIKeyEnv == "" {
			continue
		}

		// Resolve from environment
		llmCfg.APIKey = os.Getenv(llmCfg.APIKeyEnv)
		cfg.LLMs[name] = llmCfg
	}
}

// warnDeprecatedAPIKeys warns if API keys are set in YAML config files (security risk).
func warnDeprecatedAPIKeys(cfg *Config) {
	// Check Provider.APIKey
	if cfg.Provider.APIKey != "" {
		fmt.Fprintf(os.Stderr, "WARNING: api_key in YAML config is deprecated and insecure.\n")
		fmt.Fprintf(os.Stderr, "         Use environment variable %s instead.\n", cfg.Provider.APIKeyEnv)
	}

	// Check LLM APIKeys
	for name, llmCfg := range cfg.LLMs {
		if llmCfg.APIKey != "" {
			fmt.Fprintf(os.Stderr, "WARNING: api_key for %s in YAML config is deprecated and insecure.\n", name)
			fmt.Fprintf(os.Stderr, "         Use environment variable %s instead.\n", llmCfg.APIKeyEnv)
		}
	}
}

// Validate checks the configuration for common errors.
// Returns an error if the config is invalid.
// Note: This should be called explicitly by commands that need validation (e.g., multi),
// not automatically in Load(), to avoid breaking v0-only users.
func (c *Config) Validate() error {
	// v0.1: Check that at least one LLM is enabled
	// Treat nil as enabled (default), only count explicit disables
	hasEnabled := false
	hasExplicitlyDisabled := 0

	for _, llm := range c.LLMs {
		// nil means enabled by default
		if llm.Enabled == nil || *llm.Enabled {
			hasEnabled = true
		} else {
			// Explicitly disabled
			hasExplicitlyDisabled++
		}
	}

	// Only error if user explicitly disabled all LLMs (not if LLMs map is empty)
	if len(c.LLMs) > 0 && !hasEnabled && hasExplicitlyDisabled == len(c.LLMs) {
		return fmt.Errorf("all LLMs are explicitly disabled; at least one must be enabled for multi-LLM mode")
	}

	// Validate merge config
	if c.Merge.PreferredLLM != "" && c.Merge.PreferredLLM != "auto" {
		// Check that preferred LLM exists and is enabled
		llm, ok := c.LLMs[c.Merge.PreferredLLM]
		if !ok {
			return fmt.Errorf("merge.preferred_llm '%s' not found in llms configuration", c.Merge.PreferredLLM)
		}
		if llm.Enabled != nil && !*llm.Enabled {
			return fmt.Errorf("merge.preferred_llm '%s' is disabled (must be enabled to use for merging)", c.Merge.PreferredLLM)
		}
	}

	return nil
}
