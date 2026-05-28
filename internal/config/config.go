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
	"strings"

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

	// v0.8: prompt-pack customization (issue #55). Lets teams ship
	// their own house rules without forking the binary.
	Prompts PromptsConfig `yaml:"prompts"`
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
	Enabled *bool  `yaml:"enabled"`
	CLIPath string `yaml:"cli_path"` // path to CLI binary (auto-detect if empty)
	// BaseURL turns an entry into a PROVIDER agent (HTTP / OpenAI-
	// compatible: Ollama, vLLM, OpenAI, Together, Groq, OpenRouter,
	// Anthropic-compat, …). When set, the runtime treats this entry
	// as a provider, not a CLI subprocess. cli_path is then ignored.
	// User-chosen entry name is the agent's identifier (free-form;
	// "qwen", "local-fast", "air-gapped"). Added in v0.14 as part of
	// the unified agent model — providers can now run side-by-side
	// with the CLI agents in the same `local-review review` fan-out.
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`           // model name passed to the agent CLI OR provider
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

// PromptsConfig customises the language prompt packs the binary ships
// with. Issue #55: teams want to tune review tone, severity bar, or
// add house rules without forking. Three knobs, all optional, all
// composable:
//
//   - PackDir: directory of override files keyed by language id. A
//     `go.md` in this directory replaces the embedded `go.md`. Files
//     not present fall through to the embedded pack of the same name.
//   - Prepend / Append: free-form text spliced before/after whatever
//     pack content was loaded. Survives an upstream pack update —
//     the prepend/append text is yours, the pack body keeps tracking
//     upstream improvements.
//
// All three apply to BOTH the single-LLM fallback path AND the
// per-LLM CLI invocations (claude/gemini/codex), so a team's house
// rules reach every reviewer.
type PromptsConfig struct {
	PackDir string `yaml:"pack_dir"` // directory of override <language>.md files
	Prepend string `yaml:"prepend"`  // text spliced BEFORE the pack body
	Append  string `yaml:"append"`   // text spliced AFTER the pack body
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
			"copilot": {
				// Enabled nil — "run if active", same as codex. copilot is
				// paid (one Premium request per run), but only runs when
				// explicitly authenticated. Defined here so
				// `merge.preferred_llm: copilot` validates without the user
				// having to hand-add an llms.copilot block. APIKeyEnv is the
				// Copilot-specific token var (NOT GH_TOKEN/GITHUB_TOKEN —
				// those are too generic to auto-enable a paid reviewer).
				CLIPath:    "copilot",
				APIKeyEnv:  "COPILOT_GITHUB_TOKEN",
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
	// Resolve any path-typed field that's natural to express
	// relative to the config file's directory before merging. The
	// most important case (codex flagged it on PR self-review):
	// `prompts.pack_dir: .local-review/prompts` in a repo's YAML
	// must point at <repo-root>/.local-review/prompts no matter
	// where the user runs `local-review` from. Pre-fix, running
	// from a subdirectory silently fell through to embedded packs.
	if err := resolveRelativePaths(&layer, filepath.Dir(path)); err != nil {
		return fmt.Errorf("resolve relative paths in %s: %w", path, err)
	}
	merge(dst, layer)
	return nil
}

// resolveRelativePaths rewrites path-typed config fields to absolute
// paths interpreted relative to baseDir (the config file's directory).
// Absolute paths and empty strings pass through unchanged. CLI-flag
// overrides bypass this routine because they ride on top of the
// already-merged config and are naturally interpreted relative to
// the user's CWD (which is what shell users expect from
// `--prompt-pack-dir ./prompts`).
//
// Returns an error when a YAML-supplied relative path escapes
// baseDir — either via `..` segments (the v0.10.0 lexical check) OR
// via symlinks that resolve outside baseDir (v0.10.5 hardening:
// the pre-fix `pathInsideDir` was lexical only, so a path that
// stayed inside baseDir on paper but pointed via symlink to /etc
// would have slipped through). Absolute paths still pass through
// (explicit user opt-in to a specific location is fine); only
// relative escapes are rejected.
//
// This is the defence the audit feature caught missing in
// v0.10.0-c's own dogfood: a malicious or accidentally-shared
// `.local-review.yml` with `pack_dir: ../../../../etc` would
// previously resolve outside the intended directory, letting any
// subsequent override read touch files in arbitrary locations. The
// symlink hardening closes the second-pass finding from PR #88's
// own dogfood (single reviewer, but real defence-in-depth gap).
func resolveRelativePaths(layer *Config, baseDir string) error {
	if p := layer.Prompts.PackDir; p != "" && !filepath.IsAbs(p) {
		resolved := filepath.Join(baseDir, p)
		if !pathInsideDir(resolved, baseDir) {
			return fmt.Errorf("prompts.pack_dir %q escapes config directory (resolves to %q outside %q); use an absolute path if you really need to point outside the config directory",
				p, resolved, baseDir)
		}
		layer.Prompts.PackDir = resolved
	}
	// StorageConfig.BasePath is intentionally LEFT relative — the
	// existing storage code resolves it relative to CWD on every
	// invocation, and tests + docs depend on that behaviour. Changing
	// it here would be a backwards-incompatible shift; deferred.
	return nil
}

// pathInsideDir returns true when filePath sits inside dir, AFTER
// resolving symlinks on both sides. Mirrors the helper in
// internal/prompts; we re-implement here rather than import to
// keep the dependency shape of internal/config minimal (nothing
// else in this package imports prompts, and adding the dep just
// for the helper is over-coupling).
//
// Symlink handling (v0.10.5):
//
//   - When EvalSymlinks succeeds on BOTH paths, the check is done
//     against the resolved real paths. This catches the case
//     where filePath lexically sits inside dir but a symlink
//     inside dir points outside (e.g. dir/link → /etc).
//
//   - When EvalSymlinks FAILS on filePath (typical: pack_dir
//     points at a directory that doesn't exist yet — the user
//     plans to create it), we fall through to the lexical check
//     on the cleaned paths. The file-open path will catch the
//     "missing target" case downstream with a clearer error than
//     this layer could produce.
//
//   - When EvalSymlinks fails on dir, something is wrong with the
//     config-dir layout itself — fall back to lexical too rather
//     than reject every load. (In practice baseDir is the config
//     file's dir, which exists by definition since we just read
//     a YAML out of it.)
//
// Fail-closed posture is preserved overall: any rel-path-with-..
// is rejected lexically before the EvalSymlinks branch even runs,
// and the EvalSymlinks branch can only ADD rejections (it never
// admits a path that lexical rejected). So a fallback-to-lexical
// on EvalSymlinks error never weakens the check.
func pathInsideDir(filePath, dir string) bool {
	cleanedDir := filepath.Clean(dir)
	cleanedPath := filepath.Clean(filePath)

	// Lexical containment first — cheap, fails fast on `..` escapes.
	rel, err := filepath.Rel(cleanedDir, cleanedPath)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	// Symlink resolution second — catches the
	// lexically-inside-but-symlinks-out case. EvalSymlinks errors
	// on missing targets (the common pack_dir-not-created-yet case),
	// so we walk UP filePath until we find an existing ancestor,
	// then check THAT against the resolved baseDir.
	//
	// First-pass v0.10.5 had a bypass here: if `<base>/evil-link
	// → /etc` and pack_dir was `evil-link/new-leaf`, the leaf
	// didn't exist on disk, EvalSymlinks failed, and the function
	// returned true. But the PARENT (`<base>/evil-link`) already
	// resolved outside the base — admitting the leaf was wrong.
	// Codex caught this on PR #90's own dogfood. The walk-up
	// approach closes the bypass: it resolves the deepest existing
	// ancestor, which always exists (worst case: baseDir itself),
	// and rejects when THAT resolves outside.
	resolvedDir, derr := filepath.EvalSymlinks(cleanedDir)
	if derr != nil {
		// baseDir doesn't resolve. Highly unusual — baseDir is
		// the config file's own directory and Load just read a
		// YAML out of it. But fail closed in a security-
		// sensitive function: if we can't verify the symlink
		// chain, we shouldn't admit the path on the strength of
		// the (weaker) lexical pass alone. A permissions race
		// between YAML-read and this resolve is the kind of
		// transient that we'd rather reject loudly (the user
		// sees the load fail and can investigate) than admit
		// silently. Codex caught the prior fail-open posture on
		// PR #90's own dogfood — fixed before merge.
		return false
	}
	resolvedAncestor := deepestExistingAncestor(cleanedPath)
	if resolvedAncestor == "" {
		// Even the root resolved to nothing — no existing
		// filesystem state to check against. Same fail-closed
		// posture as the baseDir-error branch above.
		return false
	}
	relReal, err := filepath.Rel(resolvedDir, resolvedAncestor)
	if err != nil {
		return false
	}
	if relReal == ".." || strings.HasPrefix(relReal, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// deepestExistingAncestor returns the EvalSymlinks-resolved real
// path of the deepest existing prefix of `path`. Used by
// pathInsideDir to close the v0.10.5-RC bypass where a non-
// existent leaf hid the fact that the parent already resolved
// outside the base.
//
// Algorithm: starting from `path`, walk up via filepath.Dir until
// EvalSymlinks succeeds. The walk always terminates because
// filepath.Dir eventually returns "/" (or "." for relative
// inputs), both of which exist on a working filesystem. Returns
// "" only on the truly degenerate case where even the root fails
// to resolve — the caller treats this as fail-closed (rejects
// the path), matching the security-sensitive posture of
// pathInsideDir itself. Codex caught a stale "fall back to
// lexical" claim in this comment on PR #90's final dogfood;
// updated to match the fail-closed behaviour the caller actually
// implements.
func deepestExistingAncestor(path string) string {
	cur := path
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached filesystem root or stable cycle —
			// EvalSymlinks failed at every level. Surface "".
			return ""
		}
		cur = parent
	}
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
// to Config / Provider / Review / LLMConfig / MergeConfig / StorageConfig
// / PromptsConfig, you MUST add a corresponding overlay branch here, or
// user overrides for that field will silently no-op.
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
				if llmCfg.BaseURL != "" {
					existing.BaseURL = llmCfg.BaseURL
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

	// v0.8: Prompts customization (issue #55).
	if src.Prompts.PackDir != "" {
		dst.Prompts.PackDir = src.Prompts.PackDir
	}
	if src.Prompts.Prepend != "" {
		dst.Prompts.Prepend = src.Prompts.Prepend
	}
	if src.Prompts.Append != "" {
		dst.Prompts.Append = src.Prompts.Append
	}
}

// resolveAPIKey resolves cfg.Provider.APIKey with **env vars taking
// precedence over the YAML-stored key** when both are present.
// YAML-stored keys are explicitly marked DEPRECATED in the schema
// AND warned about at load time (see warnDeprecatedAPIKeys), but
// prior to the v0.10.0 audit dogfood the resolver still preferred
// YAML over env — meaning a developer who committed a test key to
// `.local-review.yml` and later set a correct prod key in the
// environment would silently keep using the stale YAML value.
// Env-first closes that footgun. Empty env var falls back to the
// YAML key (preserves v0 compat for users who put a key in YAML
// and never set an env var).
func resolveAPIKey(cfg *Config) {
	envName := cfg.Provider.APIKeyEnv
	if envName == "" {
		envName = "LOCAL_REVIEW_API_KEY"
	}
	if envVal := os.Getenv(envName); envVal != "" {
		cfg.Provider.APIKey = envVal
	}
	// else: leave whatever was already in cfg.Provider.APIKey
	// (possibly a deprecated YAML key, warned about separately).
}

// resolveAPIKeys is the per-LLM counterpart of resolveAPIKey for
// the multi-LLM path. Same env-first precedence and same fallback
// rule: env wins when set; empty env preserves whatever YAML had.
func resolveAPIKeys(cfg *Config) {
	for name, llmCfg := range cfg.LLMs {
		if llmCfg.APIKeyEnv == "" {
			continue
		}
		if envVal := os.Getenv(llmCfg.APIKeyEnv); envVal != "" {
			llmCfg.APIKey = envVal
			cfg.LLMs[name] = llmCfg
		}
	}
}

// warnDeprecatedAPIKeys warns if API keys are set in YAML config
// files. Runs BEFORE the resolver, so the warning fires on every
// YAML-stored key regardless of whether an env var will end up
// winning. The text reflects the v0.10.0 precedence flip: env wins
// when set, so a YAML key is only "in effect" when there's no env
// var of the corresponding name. Either way, committing keys to
// YAML is the wrong shape and the warning surfaces that.
func warnDeprecatedAPIKeys(cfg *Config) {
	if cfg.Provider.APIKey != "" {
		envName := cfg.Provider.APIKeyEnv
		if envName == "" {
			envName = "LOCAL_REVIEW_API_KEY"
		}
		fmt.Fprintf(os.Stderr, "WARNING: api_key in YAML config is deprecated and insecure.\n")
		fmt.Fprintf(os.Stderr, "         Use environment variable %s instead (when set, it takes precedence over this YAML key).\n", envName)
	}

	for name, llmCfg := range cfg.LLMs {
		if llmCfg.APIKey != "" {
			fmt.Fprintf(os.Stderr, "WARNING: api_key for %s in YAML config is deprecated and insecure.\n", name)
			// If the user set api_key but not api_key_env, our
			// previous warning printed "Use environment variable
			// instead..." with an empty name — actively
			// unhelpful (Copilot caught this on PR #75). Emit a
			// concrete-next-step message instead, pointing at the
			// per-LLM config field they need to add.
			if llmCfg.APIKeyEnv == "" {
				fmt.Fprintf(os.Stderr, "         Set llms.%s.api_key_env to an env var name and export the key in your shell instead.\n", name)
			} else {
				fmt.Fprintf(os.Stderr, "         Use environment variable %s instead (when set, it takes precedence over this YAML key).\n", llmCfg.APIKeyEnv)
			}
		}
	}
}

// Validate checks the configuration for common errors.
// Returns an error if the config is invalid.
// Note: This should be called explicitly by commands that need validation (e.g., multi),
// not automatically in Load(), to avoid breaking v0-only users.
// ErrAllLLMsDisabled is returned by Validate when every configured LLM
// has an explicit `enabled: false`. The runner tolerates this
// specifically when `--only` is set: `--only` is an explicit allow-list
// that overrides config-level enable/disable for agent SELECTION, so an
// all-disabled config is fine in that case (the user opted into the
// named agents). Without the sentinel the runner couldn't tell this
// benign case apart from a genuinely misconfigured default run.
var ErrAllLLMsDisabled = errors.New("all LLMs are explicitly disabled; at least one must be enabled for multi-LLM mode")

func (c *Config) Validate() error {
	// Validate merge config FIRST. ErrAllLLMsDisabled (below) is the one
	// error the runner tolerates under --only, so it MUST be returned
	// only when nothing else is wrong — otherwise tolerating it would
	// mask an unrelated misconfig (e.g. a typo'd merge.preferred_llm).
	// Checking everything else before the all-disabled short-circuit
	// keeps the runner's "tolerate ErrAllLLMsDisabled" narrow and exact.
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

	// v0.1: at least one LLM must be enabled. Treat nil as enabled
	// (default); only count explicit disables. This check is LAST so
	// the merge validation above is never short-circuited by it.
	hasEnabled := false
	hasExplicitlyDisabled := 0
	for _, llm := range c.LLMs {
		if llm.Enabled == nil || *llm.Enabled {
			hasEnabled = true
		} else {
			hasExplicitlyDisabled++
		}
	}
	// Only error if the user explicitly disabled all LLMs (not if the
	// LLMs map is empty).
	if len(c.LLMs) > 0 && !hasEnabled && hasExplicitlyDisabled == len(c.LLMs) {
		return ErrAllLLMsDisabled
	}

	return nil
}
