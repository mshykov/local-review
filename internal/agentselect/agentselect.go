// Package agentselect decides which detected LLM agents run for a given
// invocation: it applies the --only allow-list, readiness, config
// enable/disable, manufacturer-sunset auto-disable, and per-agent config
// threading. It is pure (no detection I/O) so the selection rules are
// unit-testable against synthetic input; the command layer (pickAgents)
// owns the real CLI/provider detection and feeds the results in here.
package agentselect

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
)

// DropCLITwins removes CLI-detected agents whose name also carries a
// base_url in config. Such a name is a PROVIDER agent (the kind
// discriminator is base_url != ""), and the caller appends its provider
// twin separately. Without this filter, `llms.claude.base_url: ...` with
// the claude CLI installed yields BOTH a CLI "claude" (from the hardcoded
// supported list) AND a provider "claude" — two same-named agents that
// both run, double-review the same diff, and collide in the name-keyed
// ready/merge maps. Dropping the CLI twin makes the provider entry win,
// matching the documented "sets BaseURL → routes to the provider path"
// invariant. This is deliberate even if the provider endpoint is later
// found unreachable: a user who set base_url on a CLI name asked for THAT
// endpoint, and silently falling back to the local CLI would route around
// their explicit choice (e.g. a policy-enforcing proxy). Only CLI entries
// (BaseURL == "") are dropped, so the helper
// is safe even if called on a slice that already contains provider twins.
// Pure (no detection I/O) so it is unit-testable.
func DropCLITwins(detected []cli.LLM, cfg config.Config) []cli.LLM {
	providerNames := make(map[string]bool, len(cfg.LLMs))
	for name, c := range cfg.LLMs {
		if strings.TrimSpace(c.BaseURL) != "" {
			providerNames[name] = true
		}
	}
	if len(providerNames) == 0 {
		return detected
	}
	kept := make([]cli.LLM, 0, len(detected))
	for _, llm := range detected {
		if providerNames[llm.Name] && llm.BaseURL == "" {
			continue // CLI twin of a provider-named entry
		}
		kept = append(kept, llm)
	}
	return kept
}

// ProviderSpecsFromConfig extracts the provider entries from cfg.LLMs
// (those with BaseURL set) into the runtime ProviderSpec shape
// DetectProviders consumes. Specs are returned in name-sorted order so
// doctor rows and the active-agent list are deterministic across runs
// — Go map iteration is randomised, which made output flicker between
// invocations (flagged by the PR 2 self-review).
//
// The api key has already been resolved by config.resolveAPIKeys
// (env-var lookup) before this point, so the APIKey field here is the
// actual value, not a key name.
func ProviderSpecsFromConfig(cfg config.Config) []cli.ProviderSpec {
	names := make([]string, 0, len(cfg.LLMs))
	for name, c := range cfg.LLMs {
		if strings.TrimSpace(c.BaseURL) == "" {
			continue // CLI entry (or whitespace-only typo), handled by DetectAllWithOverrides above
		}
		names = append(names, name)
	}
	sort.Strings(names)
	specs := make([]cli.ProviderSpec, 0, len(names))
	for _, name := range names {
		c := cfg.LLMs[name]
		specs = append(specs, cli.ProviderSpec{
			Name:       name,
			BaseURL:    strings.TrimSpace(c.BaseURL),
			Model:      c.Model,
			APIKey:     c.APIKey,
			APIKeyEnv:  c.APIKeyEnv,
			TimeoutSec: c.TimeoutSec,
		})
	}
	return specs
}

// Select picks which detected LLMs run, plus the names of any that were
// authed-but-disabled-in-config so the caller can show a discoverability
// hint. Decision tree, top-down:
//
//  1. If --only is set (the `only` arg), that wins absolutely (overrides
//     config disable) — EXCEPT for review-incapable experimental CLIs
//     (cli.IsReviewCapable), which are excluded even under --only because
//     they can't produce a usable review (e.g. antigravity's agentic
//     `--print` mode). Honoring `--only antigravity` would ship a broken
//     run, not user intent.
//  2. An LLM is "active" only if its readiness map says so (caller
//     supplies; in production this comes from doctor's classify).
//  3. If config explicitly sets enabled:false, skip — but report it
//     separately so we can tell the user about the override path.
//
// Select takes `now` (UTC) as a parameter rather than calling time.Now()
// internally so the sunset behaviour (v0.15+) is testable against a fixed
// clock — production callers pass time.Now().UTC() via pickAgents. Tests
// inject pre/post-sunset timestamps to exercise both the auto-disable
// branch and the force_after_sunset opt-out without waiting for the wall
// clock to cross the real 2026-06-18 cutoff.
//
// Returns three lists: `active` (the fan-out roster); `configDisabled`
// (agents skipped because cfg.LLMs[*].Enabled is false — the
// `--only <name>` override-hint path consumes this); `sunsetDropped`
// (agents skipped because their manufacturer-announced sunset date
// has passed and `force_after_sunset` isn't set). The two skip lists
// are kept separate because their override paths are different —
// configDisabled wants `--only`, sunsetDropped wants
// `llms.<name>.force_after_sunset: true` — and the original v0.15
// review caught that bundling them broke the `--only` suggestion
// in printAgentRoster.
func Select(detected []cli.LLM, ready map[string]bool, cfg config.Config, only string, now time.Time) (active []cli.LLM, configDisabled, sunsetDropped []string) {
	if want := ParseOnlyList(only); len(want) > 0 {
		for _, llm := range detected {
			if !want[llm.Name] {
				continue
			}
			if !cli.IsReviewCapable(llm.Name) {
				continue
			}
			if !ready[llm.Name] {
				continue
			}
			if isSunsetAndNotForced(llm, cfg, now) {
				// --only is an explicit allow-list; honour the user's
				// intent over the sunset auto-disable. Warn so they
				// see they're past the cutoff (force_after_sunset is
				// always implicit under --only of a sunset agent).
				fmt.Fprintf(os.Stderr, "Warning: --only %s past manufacturer sunset (%s) — running anyway (treat any failures as expected).\n",
					llm.Name, cli.AgentSunsetDate(llm.Name).Format("2006-01-02"))
			}
			active = append(active, applyConfig(llm, cfg))
		}
		return active, nil, nil
	}

	for _, llm := range detected {
		if !cli.IsReviewCapable(llm.Name) {
			continue
		}
		if !ready[llm.Name] {
			continue
		}
		if c, ok := cfg.LLMs[llm.Name]; ok && c.Enabled != nil && !*c.Enabled {
			configDisabled = append(configDisabled, llm.Name)
			continue
		}
		if isSunsetAndNotForced(llm, cfg, now) {
			// v0.15: drop the agent from the active set once its
			// manufacturer-announced sunset passes (today: gemini
			// after 2026-06-18). Without this, every default-mode
			// review post-cutoff burns ~10s on the pre-flight probe
			// only to surface a 401 or "model not found" error.
			// `llms.<name>.force_after_sunset: true` opts back in.
			// Reported separately from configDisabled so the
			// `--only <name>` override hint stays valid syntactically
			// and the sunset hint renders with its own
			// `force_after_sunset: true` advice.
			sunsetDropped = append(sunsetDropped, llm.Name)
			continue
		}
		active = append(active, applyConfig(llm, cfg))
	}
	return active, configDisabled, sunsetDropped
}

// isSunsetAndNotForced returns true when the agent's manufacturer
// sunset date has passed AND the user has NOT explicitly opted
// in via llms.<name>.force_after_sunset. Today only the Gemini CLI
// has a sunset; the predicate is no-op for everything else.
//
// The CLI/provider distinction matters: a sunset is a property of
// a *vendor binary* (Google's Gemini CLI binary stops serving on
// 2026-06-18), NOT of a name. A user-defined provider entry that
// happens to be called `llms.gemini:` (e.g. a self-hosted Gemini-
// compatible service) must NOT be auto-disabled. The
// `llm.BaseURL == ""` guard restricts the check to CLI subprocess
// agents — provider agents short-circuit out regardless of name.
// (v0.15 pre-release QA caught this with codex.)
func isSunsetAndNotForced(llm cli.LLM, cfg config.Config, now time.Time) bool {
	if llm.BaseURL != "" {
		// Provider agent (Ollama / vLLM / OpenAI-compat HTTP). The
		// manufacturer-sunset concept doesn't apply — the user
		// controls the endpoint.
		return false
	}
	if !cli.IsAgentSunset(llm.Name, now) {
		return false
	}
	if c, ok := cfg.LLMs[llm.Name]; ok && c.ForceAfterSunset != nil && *c.ForceAfterSunset {
		return false
	}
	return true
}

// ExperimentalOnlyNames returns the --only entries that name a
// detected-but-review-incapable CLI (cli.IsReviewCapable == false),
// e.g. `--only antigravity`. The caller uses this to explain WHY a
// named agent didn't run — without it, an all-experimental --only set
// produces an empty active set and the generic "matched no ready LLMs
// / check authentication" error, which misdiagnoses an intentional
// gate as an auth problem.
func ExperimentalOnlyNames(only string) []string {
	var out []string
	for name := range ParseOnlyList(only) {
		if !cli.IsReviewCapable(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out) // deterministic message ordering
	return out
}

// ParseOnlyList splits a comma-separated --only value into a set.
// Trims whitespace per element and drops empty entries so callers don't
// need a separate guard against `--only ""` or `--only " ,, "`.
func ParseOnlyList(s string) map[string]bool {
	out := make(map[string]bool)
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out
}

// applyConfig threads per-agent config (model, timeout) onto the
// detected LLM struct so it reaches the invoker — without this the
// Detector returns name+path+version only and per-agent --*-model /
// timeout config is silently dropped on the floor.
//
// Renamed from withTimeout to reflect the broader scope; the function
// now owns "everything from cfg.LLMs[llm.Name] that the invoker needs".
func applyConfig(llm cli.LLM, cfg config.Config) cli.LLM {
	if c, ok := cfg.LLMs[llm.Name]; ok {
		if c.TimeoutSec > 0 {
			llm.TimeoutSec = c.TimeoutSec
		}
		if c.Model != "" {
			llm.Model = c.Model
		}
		// APIKey is already resolved from c.APIKeyEnv by
		// config.resolveAPIKeys() during Load(), so the value is
		// either the user's custom-env-var key or empty (in which
		// case the CLI's own auth flow takes over).
		if c.APIKey != "" {
			llm.APIKey = c.APIKey
		}
	}
	if llm.TimeoutSec <= 0 {
		// Falls back to cli.DefaultTimeoutSec — same constant the
		// orchestrator's RunParallel fallback and the roster's
		// display fallback use, so what the user sees ("timeout:
		// Ns") matches what actually fires.
		// `<= 0` (rather than `== 0`) protects against a negative
		// `timeout_seconds: -1` typo in user config.
		llm.TimeoutSec = cli.DefaultTimeoutSec
	}
	return llm
}
