package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/prompts"
)

// claudeSessionFreshness caps how stale a session file can be before
// we stop counting it as "logged in". Claude Code stores tokens in the
// OS keychain (not a file we can read), so we use session activity as
// a proxy. A 30-day window allows infrequent users while still
// reflecting an explicit logout that wipes recent activity.
const claudeSessionFreshness = 30 * 24 * time.Hour

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check LLM CLI installations and authentication status",
		Long: `Doctor checks each LLM CLI's installation, version, and authentication state.

It detects:
  - Claude CLI (claude) — auth via 'claude login' or ANTHROPIC_API_KEY
  - Gemini CLI (gemini) — auth via GEMINI_API_KEY (preferred) or 'gemini /auth' (Google OAuth). DEPRECATED: stops serving 2026-06-18; migrate to Antigravity.
  - OpenAI Codex CLI (codex) — auth via 'codex login' (ChatGPT Plus) or OPENAI_API_KEY
  - Antigravity CLI (agy) — Google's Gemini-CLI successor; auth via Google OAuth ('agy' to log in)

For each CLI, doctor prints one of:
  ✓ ready                — installed, version detected, authenticated
  ◐ experimental         — detected but excluded from the review fan-out (e.g. agy)
  ⚠ install broken       — binary in PATH but version probe failed
  ⚠ not authenticated    — installed and working, but no credentials/key found
  ✗ not installed        — binary not in PATH

Exit code is always 0; this is a diagnostic command, not a gate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.OutOrStdout())
		},
	}
}

// runDoctor prints a diagnostic table of each LLM CLI's state. Output
// is structured so a user reading it knows exactly what's needed for
// each CLI: install it, log in, set an env var, or nothing.
//
// All writes go through an errWriter so the first I/O error (broken
// pipe, disk full, etc.) is preserved and returned at the end —
// otherwise `local-review doctor > /dev/full` would silently exit 0.
func runDoctor(out io.Writer) error {
	w := &errWriter{w: out}

	fmt.Fprintln(w, "Checking LLM installations and authentication...")
	fmt.Fprintln(w)

	// Mirror the runner: honor cfg.LLMs[*].CLIPath overrides so doctor
	// and runtime see the same binaries. Without this a user with a
	// custom cli_path would see ✗ in doctor but a successful run, or
	// vice versa.
	overrides := map[string]string{}
	customEnvVars := map[string]string{}
	models := map[string]string{}
	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		// Surface the failure inline. doctor's whole job is "tell me
		// what state the user's setup is in"; silently falling back to
		// defaults here would print cli_path / api_key_env diagnostics
		// based on built-ins exactly when the user is debugging why
		// their config isn't taking effect.
		fmt.Fprintf(w, "WARNING: failed to load config: %v\n", cfgErr)
		fmt.Fprintln(w, "         Falling back to compiled-in defaults; cli_path / api_key_env shown below may not reflect your config.")
		fmt.Fprintln(w)
	} else {
		for name, c := range cfg.LLMs {
			if c.CLIPath != "" {
				overrides[name] = c.CLIPath
			}
			// Honor cfg.LLMs[*].APIKeyEnv so a user with a key under,
			// say, MY_GEMINI_KEY sees ✓ ready instead of "not authed".
			// Empty falls through to the canonical default per LLM.
			if c.APIKeyEnv != "" {
				customEnvVars[name] = c.APIKeyEnv
			}
			// Surface the configured model so doctor exposes "which
			// weights will run" — pre-fix the user only learned the
			// model name when `review` printed the roster, too late
			// to catch a misconfigured model before triggering an
			// expensive call.
			if c.Model != "" {
				models[name] = c.Model
			}
		}
	}
	llms := cli.DetectAllWithOverrides(overrides)
	// Provider agents (entries with base_url in cfg.LLMs) — detected via
	// HTTP /v1/models probe and rendered alongside CLI agents in the same
	// readiness summary. Empty when no provider entries are configured.
	// Specs sorted by name so doctor's provider rows appear in a stable
	// order across runs (Go map iteration is randomised; without the sort
	// the output flickered between invocations).
	var providerSpecs []cli.ProviderSpec
	if cfgErr == nil {
		names := make([]string, 0, len(cfg.LLMs))
		for name, c := range cfg.LLMs {
			if c.BaseURL == "" {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := cfg.LLMs[name]
			providerSpecs = append(providerSpecs, cli.ProviderSpec{
				Name:       name,
				BaseURL:    c.BaseURL,
				Model:      c.Model,
				APIKey:     c.APIKey,
				TimeoutSec: c.TimeoutSec,
			})
		}
	}
	llms = append(llms, cli.DetectProviders(context.Background(), providerSpecs)...)

	// Capture `now` once so every clock-driven decision in this
	// run (sunset gate on the numerator AND denominator, sunset
	// banner rendering) sees the same instant. Pre-fix doctor
	// called time.Now() three times across the loop and the
	// banner; near the cutoff boundary that could produce a row
	// that says "sunset" while the denominator still counted the
	// agent as available, or vice versa. (v0.15 pre-release QA
	// catch from codex.)
	now := time.Now().UTC()
	readyCount := 0
	reviewCapable := 0
	for _, llm := range llms {
		var force bool
		if c, ok := cfg.LLMs[llm.Name]; ok && c.ForceAfterSunset != nil {
			force = *c.ForceAfterSunset
		}
		// Sunset gate applies to CLI agents only — a user-named
		// provider entry (`llms.gemini: { base_url: ... }`) is
		// NOT a Google CLI subprocess and must not be auto-dropped.
		sunsetGated := llm.BaseURL == "" && cli.IsAgentSunset(llm.Name, now) && !force

		if cli.IsReviewCapable(llm.Name) && !sunsetGated {
			reviewCapable++
		}
		status, auth := classify(llm, customEnvVars[llm.Name])
		// Mirror the runtime fan-out: a sunset CLI without
		// force_after_sunset is NOT going to participate, so it
		// must NOT count toward "N/M LLMs ready for multi-review".
		// Numerator and denominator both apply the same gate now.
		if status == statusReady && !sunsetGated {
			readyCount++
		}
		printLLMRow(w, llm, status, auth, models[llm.Name], force, now)
	}

	fmt.Fprintln(w)
	// Denominator is the review-capable count, not len(llms): an
	// experimental CLI (e.g. antigravity) is detected but can't join
	// the fan-out, so counting it would make "3/4 ready" read like a
	// fixable gap when nothing is wrong.
	fmt.Fprintf(w, "%d/%d LLMs ready for multi-review.\n", readyCount, reviewCapable)

	// Issue #55: warn when prompts.pack_dir is configured but the
	// directory is missing/empty. A misconfigured override is silent
	// at runtime (the resolver falls through to embedded packs), so
	// without doctor surfacing it the user would only notice their
	// house rules aren't applying after running a review and seeing
	// the wrong tone.
	if cfgErr == nil {
		checkPromptOverride(w, cfg)
	}

	return w.err
}

// checkPromptOverride writes a warning line when cfg.Prompts.PackDir
// is set but doesn't resolve to a populated directory of <lang>.md
// override files. Silent fall-through is the documented Resolve
// behaviour, but at the doctor level we surface it explicitly so a
// typo'd path or an unmounted directory becomes visible.
func checkPromptOverride(w io.Writer, cfg config.Config) {
	dir := cfg.Prompts.PackDir
	if dir == "" {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		fmt.Fprintf(w, "\n⚠ Prompt pack_dir %q does not exist or is unreadable (%v)\n", dir, err)
		fmt.Fprintln(w, "  Reviews will fall through to the embedded packs. Fix the path or remove `prompts.pack_dir` from your config.")
		return
	}
	if !info.IsDir() {
		fmt.Fprintf(w, "\n⚠ Prompt pack_dir %q is a file, not a directory.\n", dir)
		fmt.Fprintln(w, "  prompts.pack_dir must point at a directory of <language>.md override files (e.g. go.md, default.md).")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(w, "\n⚠ Prompt pack_dir %q is unreadable: %v\n", dir, err)
		return
	}
	// Two things to check, both caught by self-review iterations:
	//
	// 1. Counting any *.md file silenced the diagnostic when the
	//    user dropped a README.md into the prompts directory but
	//    no actual override files. Fix: match against the known
	//    language-id set.
	//
	// 2. A known-language override file that EXISTS but isn't
	//    READABLE (perms drift, broken symlink, NFS hiccup) would
	//    pass the count check but get silently skipped at review
	//    time by the resolver's fall-through-on-error contract.
	//    Fix: actively probe readability here, where surfacing the
	//    problem doesn't disrupt a real review run. The resolver
	//    stays resilient; doctor stays loud.
	knownLangs := promptLanguageSet()
	overrideCount := 0
	var unreadable []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		lang := strings.TrimSuffix(name, ".md")
		if _, ok := knownLangs[lang]; !ok {
			continue
		}
		overrideCount++
		path := filepath.Join(dir, name)
		// Use os.ReadFile (matching the resolver's Resolve path
		// in internal/prompts/prompts.go) so this probe agrees
		// with what review-time actually does. The pre-fix
		// `os.Open + Close` shape passed on edge cases the
		// resolver fails on — most notably a symlink whose
		// target is a directory, where Open(O_RDONLY) succeeds
		// but ReadFile errors out — letting doctor report ✓ on
		// an override the runtime would silently fall through.
		// Empty / whitespace-only files also fall through at
		// review time (resolver's TrimSpace check defends
		// against an accidentally-truncated pack neutering the
		// system prompt), so we mirror that here.
		b, err := os.ReadFile(path)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", name, err))
			continue
		}
		if strings.TrimSpace(string(b)) == "" {
			unreadable = append(unreadable, fmt.Sprintf("%s (empty)", name))
		}
	}
	if overrideCount == 0 {
		fmt.Fprintf(w, "\n⚠ Prompt pack_dir %q has no <language>.md files matching a shipped pack.\n", dir)
		fmt.Fprintln(w, "  Drop a file like `go.md` or `default.md` into the directory to override the embedded pack.")
		return
	}
	if len(unreadable) > 0 {
		fmt.Fprintf(w, "\n⚠ Prompt override file(s) in %q present but unreadable:\n", dir)
		for _, u := range unreadable {
			fmt.Fprintf(w, "    %s\n", u)
		}
		fmt.Fprintln(w, "  Reviews will silently fall through to embedded packs for those languages. Fix permissions or remove the file.")
	}
}

// promptLanguageSet returns the set of language ids that have a
// shipped pack, used to validate override filenames in pack_dir.
// Pulled from prompts.Available so adding a new language pack
// automatically extends the doctor check without a separate edit.
// Returns an empty set on error (the embedded FS would have to be
// broken; the resolver itself catches that case loudly).
func promptLanguageSet() map[string]struct{} {
	ids, err := prompts.Available()
	if err != nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

// errWriter is an io.Writer that captures the first error from the
// underlying writer and short-circuits subsequent writes. Lets the
// caller string together many fmt.Fprintln calls and then check one
// error at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) Write(p []byte) (int, error) {
	if ew.err != nil {
		return 0, ew.err
	}
	n, err := ew.w.Write(p)
	if err != nil {
		ew.err = err
	}
	return n, err
}

// llmStatus is the consolidated state of an LLM CLI as we want to
// surface it to the user. It collapses the install + version + auth
// signals into one of four buckets so the doctor output is unambiguous.
type llmStatus int

const (
	statusReady               llmStatus = iota // installed, version-detected, authenticated
	statusBrokenInstall                        // binary found, version probe failed
	statusNotAuthed                            // installed + version-detected, but no credentials
	statusNotInstalled                         // binary not in PATH
	statusExperimental                         // installed but excluded from the review fan-out (cli.IsReviewCapable == false)
	statusProviderUnreachable                  // provider entry (BaseURL set) but /v1/models probe failed
)

// classify returns both the bucket and the underlying authStatus so
// the caller can render either without re-running auth checks.
//
// customEnvVar — when non-empty — replaces the canonical env var name
// in the auth check (e.g., a config with `api_key_env: MY_GEMINI_KEY`
// makes us look at $MY_GEMINI_KEY instead of $GEMINI_API_KEY). Empty
// keeps the canonical default.
func classify(llm cli.LLM, customEnvVar string) (llmStatus, authStatus) {
	// Provider agents (HTTP /v1 endpoints) — discriminate by BaseURL.
	// Available here means "the /v1/models probe succeeded" (set by
	// cli.DetectProviders). No subprocess, no version, no env-var auth
	// check: if the endpoint accepted us, we're ready. If it didn't,
	// surface a provider-specific status so printLLMRow renders an
	// endpoint-shaped error row instead of "binary not in PATH."
	if llm.BaseURL != "" {
		if llm.Available {
			return statusReady, authStatus{detail: llm.BaseURL}
		}
		return statusProviderUnreachable, authStatus{}
	}
	if !llm.Available {
		if llm.Path != "" {
			return statusBrokenInstall, authStatus{}
		}
		return statusNotInstalled, authStatus{}
	}
	// Detected but review-incapable (e.g. antigravity): surface it as
	// experimental without running an auth check — it never joins the
	// fan-out, so its auth state is moot, and agy has no API-key env
	// var to probe anyway.
	if !cli.IsReviewCapable(llm.Name) {
		return statusExperimental, authStatus{}
	}
	auth := checkAuth(llm.Name, customEnvVar)
	if !auth.authenticated {
		return statusNotAuthed, auth
	}
	return statusReady, auth
}

// printLLMRow emits one CLI's full diagnostic block. configuredModel
// is the cfg.LLMs[name].Model value (or empty); for ready rows we
// always print a model line — either the pinned value or a "vendor's
// default" notice with a pin instruction — so users can tell "I
// didn't pin one" apart from "config didn't load" at a glance, AND
// know how to take control. For not-authed rows we still elide when
// no model is pinned, since the row's primary signal is the auth fix
// and the model line would be noise.
func printLLMRow(out io.Writer, llm cli.LLM, status llmStatus, auth authStatus, configuredModel string, forceAfterSunset bool, now time.Time) {
	displayName := getDisplayName(llm.Name)

	// Provider agents render an HTTP-endpoint-shaped row (no Path,
	// no Version — those are CLI-specific). Branched here rather than
	// in the switch below to keep CLI cases untouched.
	if llm.BaseURL != "" {
		switch status {
		case statusReady:
			fmt.Fprintf(out, "✓ %-15s provider     ready\n", displayName)
			fmt.Fprintf(out, "    endpoint:      %s\n", llm.BaseURL)
			if llm.Model != "" {
				fmt.Fprintf(out, "    model:         %s\n", llm.Model)
			} else {
				fmt.Fprintf(out, "    model:         (none pinned — set `llms.%s.model:` to the model id loaded on the endpoint)\n", llm.Name)
			}
		case statusProviderUnreachable:
			fmt.Fprintf(out, "✗ %-15s provider     unreachable\n", displayName)
			fmt.Fprintf(out, "    endpoint:  %s\n", llm.BaseURL)
			fmt.Fprintln(out, "    note:      the /v1/models probe failed; check the endpoint is up, the host/port is reachable, and the api_key (if any) is correct")
		}
		fmt.Fprintln(out)
		return
	}

	switch status {
	case statusReady:
		fmt.Fprintf(out, "✓ %-15s v%-10s ready\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed:     %s\n", llm.Path)
		fmt.Fprintf(out, "    authenticated: %s\n", auth.detail)
		if configuredModel != "" {
			fmt.Fprintf(out, "    model:         %s\n", configuredModel)
		} else {
			// No pinned model — invoker doesn't pass --model and the
			// vendor CLI picks its own default. Surface this with a
			// pin-instruction so users debugging "why did claude run
			// model X" know how to take control. Pre-fix said "(CLI
			// default)" which was reported as a non-answer.
			fmt.Fprintf(out, "    model:         vendor's default — pin via `llms.%s.model:` to override\n", llm.Name)
		}

	case statusNotAuthed:
		fmt.Fprintf(out, "⚠ %-15s v%-10s not authenticated\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed: %s\n", llm.Path)
		fmt.Fprintf(out, "    fix:       %s\n", auth.hint)
		if configuredModel != "" {
			fmt.Fprintf(out, "    model:     %s\n", configuredModel)
		}

	case statusBrokenInstall:
		fmt.Fprintf(out, "⚠ %-15s install broken\n", displayName)
		fmt.Fprintf(out, "    found at:  %s\n", llm.Path)
		fmt.Fprintln(out, "    note:      version probe failed; reinstall the CLI")
		printInstallInstructions(out, llm.Name)

	case statusExperimental:
		// Detected and usable as an interactive agent, but excluded
		// from the review fan-out: agy's `--print` mode runs an
		// autonomous agent loop (explores the repo, rebuilds its own
		// diff, emits step-narration) rather than returning a clean
		// review, so wiring it into the parallel fan-out ships an
		// empty/garbled report. Surfaced so users know it's recognised
		// and why it isn't reviewing — not a misconfiguration to fix.
		fmt.Fprintf(out, "◐ %-15s v%-10s detected (review integration: experimental)\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed:     %s\n", llm.Path)
		fmt.Fprintln(out, "    note:          agy is an autonomous agent; its --print mode does not yet")
		fmt.Fprintln(out, "                   emit a clean review, so it is excluded from the review")
		fmt.Fprintln(out, "                   fan-out. Tracked for a future release.")

	case statusNotInstalled:
		fmt.Fprintf(out, "✗ %-15s not installed\n", displayName)
		printInstallInstructions(out, llm.Name)
	}

	// Gemini sunset notice. Google's Gemini CLI stops serving
	// Pro/Ultra/free-tier requests on 2026-06-18 (`cli.GeminiSunsetDate`);
	// Antigravity (`agy`) is the replacement.
	//
	// v0.15 upgraded the static banner to a clock-aware variant:
	//   pre-sunset             → countdown ("N days until sunset, ...")
	//   post-sunset, !force    → "sunset (auto-disabled; ...)"
	//   post-sunset, force     → "sunset (overridden — running anyway, ...)"
	// Surfaced on every gemini row regardless of detection state so
	// the user sees the migration path even when claude/codex are
	// the active fan-out. Remove this block once gemini support is
	// dropped entirely in a post-cutoff release.
	if llm.Name == "gemini" {
		geminiSunsetBanner(out, now, forceAfterSunset)
	}

	fmt.Fprintln(out)
}

// geminiSunsetBanner renders the appropriate sunset notice for the
// gemini doctor row. Pure function: no time.Now() side effect (caller
// passes `now`), no I/O beyond the writer. Three modes:
//
//   - Pre-sunset → countdown ("19 days until Gemini CLI sunset on
//     2026-06-18; Antigravity (agy) is the announced replacement").
//   - Post-sunset, force=false → "Gemini CLI sunset 2026-06-18:
//     auto-disabled in the review fan-out. Migrate to Antigravity
//     (agy), or set llms.gemini.force_after_sunset: true to override".
//   - Post-sunset, force=true → "Gemini CLI sunset 2026-06-18:
//     force_after_sunset is set — running anyway. Expect 401 / model-
//     unavailable failures if Google has removed your tier".
func geminiSunsetBanner(out io.Writer, now time.Time, force bool) {
	sunset := cli.AgentSunsetDate("gemini")
	dateStr := sunset.Format("2006-01-02")
	if !cli.IsAgentSunset("gemini", now) {
		days := cli.DaysUntilAgentSunset("gemini", now)
		fmt.Fprintf(out, "    ⚠ deprecated:  %d days until Gemini CLI sunset on %s. Migrate to Antigravity:\n", days, dateStr)
		fmt.Fprintln(out, "                   curl -fsSL https://antigravity.google/cli/install.sh | bash  (then `agy` to log in)")
		return
	}
	if force {
		fmt.Fprintf(out, "    ⚠ sunset:      Gemini CLI sunset %s — force_after_sunset is set, running anyway.\n", dateStr)
		fmt.Fprintln(out, "                   Expect 401 / model-unavailable failures if Google has removed your tier.")
		return
	}
	fmt.Fprintf(out, "    ✗ sunset:      Gemini CLI sunset %s — auto-disabled in the review fan-out.\n", dateStr)
	fmt.Fprintln(out, "                   Migrate to Antigravity (`agy`), or set llms.gemini.force_after_sunset: true to override.")
}

func getDisplayName(name string) string {
	switch name {
	case "claude":
		return "Claude CLI"
	case "gemini":
		return "Gemini CLI"
	case "codex":
		return "Codex CLI"
	case "antigravity":
		return "Antigravity CLI"
	case "copilot":
		return "Copilot CLI"
	default:
		return name
	}
}

func printInstallInstructions(out io.Writer, name string) {
	switch name {
	case "claude":
		fmt.Fprintln(out, "    install:   npm install -g @anthropic-ai/claude-code")
		fmt.Fprintln(out, "    then:      claude login   (free tier — uses your claude.ai account)")
	case "gemini":
		fmt.Fprintln(out, "    install:   npm install -g @google/gemini-cli  (requires Node.js 20+)")
		fmt.Fprintln(out, "    then:      export GEMINI_API_KEY=...   (free at https://aistudio.google.com/apikey)")
		fmt.Fprintln(out, "    or:        gemini /auth   (Google OAuth, free tier)")
	case "codex":
		fmt.Fprintln(out, "    install:   npm install -g @openai/codex")
		fmt.Fprintln(out, "    then:      codex login   (ChatGPT Plus subscription, $20/mo)")
		fmt.Fprintln(out, "    or:        export OPENAI_API_KEY=...   (pay-per-token; usually cheaper for occasional use)")
	case "antigravity":
		fmt.Fprintln(out, "    install:   curl -fsSL https://antigravity.google/cli/install.sh | bash")
		fmt.Fprintln(out, "    then:      agy   (Google OAuth login — successor to the Gemini CLI, which stops serving 2026-06-18)")
	case "copilot":
		fmt.Fprintln(out, "    install:   npm install -g @github/copilot")
		fmt.Fprintln(out, "    then:      copilot login   (requires a GitHub Copilot subscription)")
		fmt.Fprintln(out, "    or:        export COPILOT_GITHUB_TOKEN=...   (headless / CI — a bare GH_TOKEN won't auto-enable this paid reviewer)")
	}
}

// authStatus describes how an LLM CLI is authenticated, or what the
// user needs to do if it isn't.
type authStatus struct {
	authenticated bool
	detail        string // user-facing description, e.g. "logged in (claude login)"
	hint          string // shown when not authenticated, e.g. "run: claude login"
}

// checkAuth returns the authentication state for a given LLM.
//
// Uniform precedence rule across all three providers: **env-var auth
// is checked first**, then file-based auth. The reasoning:
//   - An exported env var represents the user's *current shell intent*
//     ("use this key right now"), which should win over a persisted
//     OAuth login they may have forgotten about.
//   - Most CLIs themselves apply the same rule when picking credentials.
//   - Without this, doctor would lie when a user with a stale OAuth
//     login exports a fresh API key for testing.
func checkAuth(name, customEnvVar string) authStatus {
	switch name {
	case "claude":
		return checkClaudeAuth(customEnvVar)
	case "gemini":
		return checkGeminiAuth(customEnvVar)
	case "codex":
		return checkCodexAuth(customEnvVar)
	case "copilot":
		return checkCopilotAuth(customEnvVar)
	default:
		// antigravity has no auth case: classify() short-circuits it to
		// statusExperimental before reaching checkAuth (it never joins
		// the fan-out, and agy authenticates via Google OAuth with no
		// API-key env var to probe).
		return authStatus{}
	}
}

// resolveEnvVar returns customEnvVar when non-empty, otherwise the
// canonical default for the named LLM. Centralised so the three
// per-LLM check functions don't each repeat the fallback logic.
func resolveEnvVar(name, customEnvVar string) string {
	if customEnvVar != "" {
		return customEnvVar
	}
	return cli.CanonicalAPIKeyEnv[name]
}

// authHomeDir returns the directory where each CLI stores its auth.
// Production: $HOME. Tests: set LOCAL_REVIEW_AUTH_HOME to a t.TempDir()
// so the auth checks read fixture files instead of the developer's
// real auth state.
func authHomeDir() string {
	if h := os.Getenv("LOCAL_REVIEW_AUTH_HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func checkClaudeAuth(customEnvVar string) authStatus {
	envVar := resolveEnvVar("claude", customEnvVar)
	if os.Getenv(envVar) != "" {
		return authStatus{
			authenticated: true,
			detail:        envVar + " env var set",
		}
	}
	// Claude Code stores tokens in macOS Keychain (or equivalent on
	// other OSes), so there's no credentials file we can read directly.
	// Heuristic: any file under ~/.claude/sessions/ modified within
	// claudeSessionFreshness implies a recent successful authentication.
	// Old-only files (after explicit logout, after a token rotation,
	// etc.) don't count.
	if home := authHomeDir(); home != "" {
		if hasRecentClaudeSession(filepath.Join(home, ".claude", "sessions")) {
			return authStatus{
				authenticated: true,
				detail:        "logged in via 'claude login'",
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          fmt.Sprintf("run 'claude login' (free, uses your claude.ai account) — or export %s=...", envVar),
	}
}

// hasRecentClaudeSession returns true when sessionsDir contains any
// regular file modified within claudeSessionFreshness. Used as a proxy
// for "the user has a working Claude login," since the actual token is
// stored in the OS keychain and isn't readable from a file.
func hasRecentClaudeSession(sessionsDir string) bool {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return false
	}
	cutoff := time.Now().Add(-claudeSessionFreshness)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

func checkGeminiAuth(customEnvVar string) authStatus {
	envVar := resolveEnvVar("gemini", customEnvVar)
	if os.Getenv(envVar) != "" {
		return authStatus{
			authenticated: true,
			detail:        envVar + " env var set",
		}
	}
	// Gemini CLI stores OAuth state in ~/.gemini/google_accounts.json
	// with an "active" field that's null until the user logs in.
	if home := authHomeDir(); home != "" {
		b, err := os.ReadFile(filepath.Join(home, ".gemini", "google_accounts.json"))
		if err == nil {
			var ga struct {
				Active any `json:"active"`
			}
			if err := json.Unmarshal(b, &ga); err == nil && ga.Active != nil {
				return authStatus{
					authenticated: true,
					detail:        "logged in via Google OAuth",
				}
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          fmt.Sprintf("export %s=... (free at https://aistudio.google.com/apikey) — or run 'gemini /auth' for Google OAuth", envVar),
	}
}

func checkCodexAuth(customEnvVar string) authStatus {
	envVar := resolveEnvVar("codex", customEnvVar)
	if os.Getenv(envVar) != "" {
		return authStatus{
			authenticated: true,
			detail:        envVar + " env var set",
		}
	}
	// Codex stores an explicit auth_mode field in ~/.codex/auth.json.
	// Newer versions write "chatgpt" or "api_key"; older versions or
	// hand-edited files may have an empty auth_mode but a non-null
	// OPENAI_API_KEY field — also treat that as authenticated.
	if home := authHomeDir(); home != "" {
		b, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
		if err == nil {
			var a struct {
				AuthMode     string  `json:"auth_mode"`
				OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
			}
			if err := json.Unmarshal(b, &a); err == nil {
				switch a.AuthMode {
				case "chatgpt":
					return authStatus{
						authenticated: true,
						detail:        "logged in via 'codex login' (ChatGPT subscription)",
					}
				case "api_key":
					// auth_mode: "api_key" only counts when the stored
					// key is actually non-empty. A partial / corrupted
					// auth.json with the mode set but the key cleared
					// must not produce a false "authenticated" result.
					if a.OpenAIAPIKey != nil && *a.OpenAIAPIKey != "" {
						return authStatus{
							authenticated: true,
							detail:        "API key configured via 'codex login --api-key'",
						}
					}
				default:
					// Older / hand-edited auth.json files may lack
					// auth_mode but have a stored key. Honor that.
					if a.OpenAIAPIKey != nil && *a.OpenAIAPIKey != "" {
						return authStatus{
							authenticated: true,
							detail:        "API key stored in ~/.codex/auth.json",
						}
					}
				}
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          fmt.Sprintf("run 'codex login' (ChatGPT Plus, $20/mo) — or export %s=... (pay-per-token, usually cheaper)", envVar),
	}
}

// copilotConfigDir returns Copilot CLI's config/login home — $COPILOT_HOME
// when set, else ~/.copilot (honoring LOCAL_REVIEW_AUTH_HOME in tests via
// authHomeDir). Empty when no home can be resolved.
func copilotConfigDir() string {
	if d := strings.TrimSpace(os.Getenv("COPILOT_HOME")); d != "" {
		return d
	}
	home := authHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".copilot")
}

// checkCopilotAuth reports the Copilot CLI's auth state. Copilot has no
// readable token file we can validate directly (login credentials live
// in its own store), so — like the Claude keychain case — doctor uses a
// proxy and the pre-flight probe is the real gate at review time.
//
// Precedence:
//  1. The Copilot-specific token env var — COPILOT_GITHUB_TOKEN by
//     default, or a user-configured api_key_env. Generic GH_TOKEN /
//     GITHUB_TOKEN are deliberately NOT honored (see below).
//  2. A populated `copilot login` session under ~/.copilot ($COPILOT_HOME):
//     reported authenticated, with the detail noting probe-time verification.
//  3. Otherwise not authenticated, with a login/token hint that names
//     the resolved env var.
func checkCopilotAuth(customEnvVar string) authStatus {
	// ONLY the Copilot-specific token (COPILOT_GITHUB_TOKEN by default,
	// or a user-configured api_key_env) auto-enables Copilot. We
	// deliberately do NOT honor the generic GH_TOKEN / GITHUB_TOKEN
	// here: those are routinely set for `gh` and CI for unrelated
	// reasons, and treating them as Copilot auth would silently pull a
	// PAID reviewer (one Premium request per run) into the fan-out —
	// a surprise-cost footgun flagged by the multi-LLM self-review.
	// The copilot CLI itself still reads GH_TOKEN/GITHUB_TOKEN at run
	// time; we just won't auto-activate on them. To opt in, set
	// COPILOT_GITHUB_TOKEN or run `copilot login`.
	envVar := resolveEnvVar("copilot", customEnvVar)
	if envVar == "" {
		envVar = "COPILOT_GITHUB_TOKEN" // defensive: resolveEnvVar always returns this for copilot
	}
	if strings.TrimSpace(os.Getenv(envVar)) != "" {
		return authStatus{
			authenticated: true,
			detail:        envVar + " env var set",
		}
	}
	// Stored `copilot login` session. A NON-EMPTY config dir is the
	// proxy (we don't read the credentials themselves); the pre-flight
	// probe confirms it's actually live. We require non-empty rather
	// than mere existence so a bare/stale `~/.copilot` (created but
	// never logged in) doesn't read as a false "authenticated."
	if dir := copilotConfigDir(); dir != "" {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return authStatus{
				authenticated: true,
				detail:        "login detected in " + dir + " (verified at review time by the pre-flight probe)",
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          fmt.Sprintf("run 'copilot login' (GitHub Copilot subscription) — or export %s=... (a bare GH_TOKEN won't auto-enable a paid reviewer)", envVar),
	}
}
