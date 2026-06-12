// Package config loads the cascading YAML config that drives local-review.
//
// Cascade (lowest precedence first):
//
//  1. Built-in defaults (compiled in)
//  2. Org config (optional URL, fetched + cached)  -- not yet wired; the
//     Org.ConfigURL field is parsed but unused. Planned for v0.18.0 (see ROADMAP).
//  3. ~/.local-review.yml                                 (per-user)
//  4. .local-review.yml (project root)                    (per-repo)
//  5. CLI flags                                     (per-invocation)
//
// Each layer is a partial YAML; later layers shallow-merge over earlier ones.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mshykov/local-review/internal/pathsafe"
	"gopkg.in/yaml.v3"
)

// Config is the resolved (post-cascade) configuration.
//
// The v0.13-and-earlier top-level `provider:` block was removed in v0.15
// (deprecated in v0.14). Loading a YAML file that still contains a
// `provider:` key surfaces a migration error from mergeFrom — see
// detectRemovedProviderBlock below — rather than silently dropping the
// fields. Provider endpoints now live under `llms.<name>:` with the
// same field shape (`base_url`, `model`, `api_key_env`, `timeout_seconds`).
type Config struct {
	Review Review `yaml:"review"`
	Org    Org    `yaml:"org"`

	// v0.1: multi-LLM support
	LLMs    map[string]LLMConfig `yaml:"llms"`
	Merge   MergeConfig          `yaml:"merge"`
	Storage StorageConfig        `yaml:"storage"`

	// v0.8: prompt-pack customization (issue #55). Lets teams ship
	// their own house rules without forking the binary.
	Prompts PromptsConfig `yaml:"prompts"`
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

	// ForceAfterSunset overrides the auto-disable behaviour applied
	// to manufacturer-sunset CLIs (today: gemini, sunset 2026-06-18).
	// Default behaviour is to drop a sunset CLI from the fan-out as
	// soon as the cutoff passes — keeping it active without an
	// override risks confusing 401s / "model unavailable" errors
	// against an unreachable endpoint. A user who wants to retry
	// past the cutoff (in case Google extends, or in case their
	// network sees a different rollout) can set
	// `llms.gemini.force_after_sunset: true` to opt back in.
	//
	// Pointer (*bool) so "field absent in YAML" is distinguishable
	// from "field explicitly false". Today only meaningful on the
	// gemini entry; ignored everywhere else.
	ForceAfterSunset *bool `yaml:"force_after_sunset"`
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
		Review: Review{
			// No severity floor / findings cap by default: audit applies
			// these only when the user explicitly sets --min-severity /
			// --max-findings (or the matching review.* config keys). A
			// non-empty default would silently filter audit output, which
			// nothing did pre-v0.16. (No production code read these before
			// v0.16, so the prior "warning"/20 defaults were inert.)
			MinSeverity:  "",
			MaxFindings:  0,
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

	// User config (~/.local-review.yml) — trusted: it lives in the
	// invoking user's home, not in a repo someone else can write to.
	var homeConfigPath string
	if home, err := os.UserHomeDir(); err == nil {
		homeConfigPath = filepath.Join(home, ".local-review.yml")
		if err := mergeFrom(&cfg, homeConfigPath, true); err != nil {
			return cfg, fmt.Errorf("load user config: %w", err)
		}
	}

	// Repo config (.local-review.yml at/above the project root) — UNtrusted
	// by default. It is attacker-controllable whenever you review code you
	// didn't write (a CI runner checking out a hostile commit, a freshly
	// cloned repo). The security-sensitive LLM fields it could carry —
	// cli_path (→ arbitrary binary exec), base_url (→ a new outbound
	// endpoint your diff/source is POSTed to), and api_key (a secret in a
	// repo) — are therefore stripped from this layer with a warning, the
	// same defence resolveRelativePaths already gives prompts.pack_dir.
	// A team that genuinely wants to check in a trusted config (e.g. a LAN
	// Ollama base_url) opts back in with LOCAL_REVIEW_TRUST_REPO_CONFIG=1.
	//
	// BUT: when the project lives under $HOME and has no project-local
	// config, FindRepoConfig walks up and returns the SAME file as the home
	// config loaded above. Re-processing the user's own ~/.local-review.yml
	// as the untrusted repo layer would spuriously strip its base_url /
	// api_key_env and print an alarming "untrusted config" warning about the
	// user's own trusted file (a v0.16.0 regression). A file that IS the
	// home config is trusted — skip the redundant untrusted pass.
	if repoConfigPath != "" && !sameFile(repoConfigPath, homeConfigPath) {
		trusted := os.Getenv(envTrustRepoConfig) == "1"
		if err := mergeFrom(&cfg, repoConfigPath, trusted); err != nil {
			return cfg, fmt.Errorf("load repo config (%s): %w", repoConfigPath, err)
		}
	}

	// Warn about deprecated api_key in YAML (security risk)
	warnDeprecatedAPIKeys(&cfg)

	// Env-driven API keys — resolve per-LLM API keys (the v0
	// single-LLM Provider variant of this was removed in v0.15).
	resolveAPIKeys(&cfg)

	return cfg, nil
}

// detectRemovedProviderBlock returns true when the raw YAML bytes
// contain a top-level `provider` key (regardless of value, including
// null / empty / merged-in-via-anchor). The `provider:` block was
// hard-removed in v0.15 — we look for it pre-decode so a stale v0.14
// config gets a migration error from mergeFrom instead of being
// silently dropped (yaml.v3 ignores unknown fields).
//
// Implementation note: we decode into `map[string]interface{}` so
// yaml.v3's YAML-1.1 merge-key resolution (`<<: *anchor`) runs as
// part of the parse. A pure node walk over the unresolved document
// tree would miss a provider: key that was merged in via an anchor
// — and "silently miss the legacy block" defeats the hard-rejection
// contract, exactly the bypass the v0.15 self-review caught. The
// map also handles the null-value edge case (`provider:` with no
// value) — yaml.v3 sets the key with a nil value, which the lookup
// sees regardless.
//
// A parse error here is non-fatal: we return (false, err) so the
// caller can fall through to the real decode, which will surface a
// proper "parse %s" error. Either way, the user sees something
// actionable.
func detectRemovedProviderBlock(b []byte) (bool, error) {
	var m map[string]interface{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return false, err
	}
	_, present := m["provider"]
	return present, nil
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

// envTrustRepoConfig, when set to "1", opts a repo-level
// .local-review.yml back into the security-sensitive LLM fields
// (cli_path / base_url / api_key) that are otherwise stripped from the
// untrusted repo layer. See Load and sanitizeUntrustedLayer.
const envTrustRepoConfig = "LOCAL_REVIEW_TRUST_REPO_CONFIG"

// sameFile reports whether two paths refer to the same file on disk,
// robust to relative-vs-absolute and symlink differences (os.SameFile
// compares device+inode). Returns false if either path is empty or
// doesn't exist — callers treat "not the same" as the safe default
// (the repo layer is then processed as untrusted).
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// mergeFrom reads YAML from path (if it exists) and shallow-merges
// non-zero fields into dst. Missing files are not an error. When
// trusted is false, security-sensitive LLM fields are stripped from the
// layer (with a warning) before merging — see sanitizeUntrustedLayer.
func mergeFrom(dst *Config, path string, trusted bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// v0.15 hard removal: detect the dropped top-level `provider:`
	// block and refuse with a migration message before the main
	// decode runs. yaml.v3 silently ignores unknown fields, so
	// without this check a stale v0.14 config would parse cleanly
	// and the user would see "no LLMs configured" downstream
	// without ever learning their provider: stanza was dropped.
	if removed, lerr := detectRemovedProviderBlock(b); lerr == nil && removed {
		return fmt.Errorf("%s: the top-level `provider:` block was removed in v0.15 (deprecated in v0.14).\n"+
			"         Migrate the same fields under `llms.<name>:` — pick any name (e.g. `ollama`, `qwen`, `cloud`):\n\n"+
			"           llms:\n"+
			"             ollama:\n"+
			"               base_url: <your previous provider.base_url>\n"+
			"               model: <your previous provider.model>\n"+
			"               api_key_env: <your previous provider.api_key_env>\n"+
			"               timeout_seconds: <your previous provider.timeout_seconds>  # optional\n\n"+
			"         See examples/.local-review.yml in the repo for a full annotated example, or `local-review init` for an interactive wizard.", path)
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
	if !trusted {
		sanitizeUntrustedLayer(&layer, path)
	}
	merge(dst, layer)
	return nil
}

// sanitizeUntrustedLayer zeroes the LLM fields that must not be honored
// from an untrusted (repo-level) config layer, warning about each one
// dropped. Stripping happens on the parsed layer BEFORE merge, so the
// trusted lower layers (embedded defaults + user-home config) are left
// intact — an untrusted repo can refine model / timeout / enabled, but
// cannot redirect execution, redirect the network, or inject a secret.
//
//   - cli_path → the runner feeds it to exec.LookPath + exec.CommandContext,
//     so a checked-in value runs an arbitrary binary on `review`/`audit`/
//     `doctor`. There is no legitimate per-repo use: where your claude /
//     gemini / codex binary lives is a per-machine concern.
//   - base_url → registers an OpenAI-compatible provider agent that the
//     diff (or, under audit, the whole tracked source tree) is POSTed to.
//     A checked-in value is a silent data-exfiltration channel.
//   - api_key → a credential committed into a repo; never trust one from
//     there (warnDeprecatedAPIKeys separately flags YAML keys at all).
//
// Opt back in for a genuinely trusted repo with
// LOCAL_REVIEW_TRUST_REPO_CONFIG=1.
func sanitizeUntrustedLayer(layer *Config, path string) {
	for name, llmCfg := range layer.LLMs {
		var dropped []string
		if llmCfg.CLIPath != "" {
			dropped = append(dropped, fmt.Sprintf("cli_path=%q", llmCfg.CLIPath))
			llmCfg.CLIPath = ""
		}
		if llmCfg.BaseURL != "" {
			dropped = append(dropped, fmt.Sprintf("base_url=%q", SanitizeBaseURLForDisplay(llmCfg.BaseURL)))
			llmCfg.BaseURL = ""
		}
		if llmCfg.APIKey != "" {
			dropped = append(dropped, "api_key")
			llmCfg.APIKey = ""
		}
		// api_key_env redirects WHICH env var is read as the credential
		// and injected into the agent's process. An untrusted repo setting
		// it (e.g. api_key_env: SOME_OTHER_SECRET on an existing agent)
		// could exfiltrate an arbitrary env var as that agent's auth token.
		// Same credential-sourcing class as api_key — strip it too.
		if llmCfg.APIKeyEnv != "" {
			dropped = append(dropped, fmt.Sprintf("api_key_env=%q", llmCfg.APIKeyEnv))
			llmCfg.APIKeyEnv = ""
		}
		if len(dropped) == 0 {
			continue
		}
		layer.LLMs[name] = llmCfg
		fmt.Fprintf(os.Stderr, "WARNING: ignoring security-sensitive field(s) for llms.%s from repo config %s: %s\n",
			name, path, strings.Join(dropped, ", "))
		fmt.Fprintf(os.Stderr, "         The repo-level .local-review.yml is untrusted by default — it could run an arbitrary\n")
		fmt.Fprintf(os.Stderr, "         binary or redirect your code to another server. Move the field to ~/.local-review.yml,\n")
		fmt.Fprintf(os.Stderr, "         or set %s=1 if you trust this repository.\n", envTrustRepoConfig)
	}
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
		if !pathsafe.InsideDir(resolved, baseDir) {
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
// to Config / Review / LLMConfig / MergeConfig / StorageConfig
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
				// ForceAfterSunset (v0.15) — *bool, same merge shape
				// as Enabled. Distinguishes "field absent" (nil, use
				// dst value) from "set to false" (non-nil, override).
				if llmCfg.ForceAfterSunset != nil {
					existing.ForceAfterSunset = llmCfg.ForceAfterSunset
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

// resolveAPIKeys resolves per-LLM API keys with **env vars taking
// precedence over the YAML-stored key** when both are present.
// YAML-stored keys are explicitly marked DEPRECATED in the schema
// AND warned about at load time (see warnDeprecatedAPIKeys), but
// prior to the v0.10.0 audit dogfood the resolver still preferred
// YAML over env — meaning a developer who committed a test key to
// `.local-review.yml` and later set a correct prod key in the
// environment would silently keep using the stale YAML value.
// Env-first closes that footgun. Empty env var preserves whatever
// YAML had (v0 compat for users who put a key in YAML and never
// set an env var).
//
// The v0 single-LLM `cfg.Provider.APIKey` variant of this resolver
// was removed in v0.15 along with the `provider:` block itself.
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
//
// The v0 `cfg.Provider.APIKey` branch was removed in v0.15 along
// with the `provider:` block itself.
func warnDeprecatedAPIKeys(cfg *Config) {
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

// SanitizeBaseURLForDisplay strips potentially-sensitive parts of a
// configured base_url before echoing it back into any user-facing
// surface (`local-review config` dump, CI logs, terminal history).
// Basic-auth userinfo (`https://user:pass@host`) and the query /
// fragment (`?api_key=…`) get dropped; scheme + host + path survive
// because that's the part the user actually needs. A URL that fails
// to parse is replaced with a literal placeholder rather than
// leaked verbatim — fail-closed (CLAUDE.md rule 4) and beats
// printing garbage into a YAML stanza we're showing the user.
//
// Introduced in v0.14 for the (since-removed) deprecation warning;
// the `local-review config` printer still uses it for every
// `llms.<name>.base_url` value.
func SanitizeBaseURLForDisplay(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<your-provider-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
