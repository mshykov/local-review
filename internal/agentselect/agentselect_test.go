package agentselect

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
)

// fakeDetected is the standard 3-CLI setup used across Select tests.
func fakeDetected() []cli.LLM {
	return []cli.LLM{
		{Name: "claude", Path: "/x/claude", Version: "2.1", Available: true},
		{Name: "gemini", Path: "/x/gemini", Version: "0.40", Available: true},
		{Name: "codex", Path: "/x/codex", Version: "0.128", Available: true},
	}
}

func boolPtr(b bool) *bool { return &b }

func names(llms []cli.LLM) []string {
	out := make([]string, len(llms))
	for i, l := range llms {
		out[i] = l.Name
	}
	sort.Strings(out)
	return out
}

func TestSelect_AllActiveNoConfig(t *testing.T) {
	// Default case: 3 detected, all authed, empty config — all 3 run.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, disabled, _ := Select(fakeDetected(), ready, config.Config{}, "", time.Time{})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty, got %v", disabled)
	}
}

func TestSelect_SkipsUnauthed(t *testing.T) {
	// Codex installed but not authed → skipped silently. Not "disabled
	// in config" — that's a separate state.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": false}
	active, disabled, _ := Select(fakeDetected(), ready, config.Config{}, "", time.Time{})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled should be empty for unauthed (not config-disabled): %v", disabled)
	}
}

func TestSelect_ConfigDisabledIsReported(t *testing.T) {
	// Codex authed, but config sets enabled:false → skipped AND reported
	// in the configDisabled return so the caller can hint about --only.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: boolPtr(false)},
		},
	}
	active, disabled, _ := Select(fakeDetected(), ready, cfg, "", time.Time{})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if got, want := disabled, []string{"codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("disabled: got %v, want %v", got, want)
	}
}

func TestSelect_ConfigEnabledNilTreatedAsActive(t *testing.T) {
	// Enabled is *bool; nil must be treated as "run if active". This is
	// the path that lets codex run by default in v0.5+ (was opt-in pre).
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: nil}, // explicit nil
		},
	}
	active, disabled, _ := Select(fakeDetected(), ready, cfg, "", time.Time{})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty, got %v", disabled)
	}
}

func TestSelect_OnlyFilter(t *testing.T) {
	// --only narrows to listed agents. Config disable is NOT consulted —
	// the flag is the user's explicit override, by design.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: boolPtr(false)}, // would be disabled normally
		},
	}
	active, disabled, _ := Select(fakeDetected(), ready, cfg, "claude,codex", time.Time{})
	if got, want := names(active), []string{"claude", "codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	// configDisabled is not populated when --only is set; the user is
	// already aware they're overriding.
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty when --only is set, got %v", disabled)
	}
}

func TestSelect_OnlySkipsNotReady(t *testing.T) {
	// --only mentioning an unauthed agent silently drops it (vs erroring),
	// matching how the no-flag path behaves. Doctor is the diagnostic; the
	// runner stays quiet for clean script output.
	ready := map[string]bool{"claude": true, "gemini": false, "codex": false}
	active, _, _ := Select(fakeDetected(), ready, config.Config{}, "gemini,claude", time.Time{})
	if got, want := names(active), []string{"claude"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

func TestSelect_OnlyTrimsSpaces(t *testing.T) {
	// `--only  claude , gemini ` (extra whitespace from copy-paste) must
	// still parse. Common typo, easy to handle, no reason to be strict.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, _, _ := Select(fakeDetected(), ready, config.Config{}, " claude , gemini ", time.Time{})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

func TestSelect_TimeoutCarriesOver(t *testing.T) {
	// Per-LLM timeout from config must be threaded through; previously a
	// codex review with 240s timeout would silently get the default 120
	// because applyConfig wasn't called consistently.
	ready := map[string]bool{"claude": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"claude": {TimeoutSec: 240},
		},
	}
	active, _, _ := Select(fakeDetected(), ready, cfg, "", time.Time{})
	var got int
	for _, llm := range active {
		if llm.Name == "claude" {
			got = llm.TimeoutSec
		}
	}
	if got != 240 {
		t.Errorf("claude.TimeoutSec: want 240 (from config), got %d", got)
	}
}

func TestSelect_ModelCarriesOver(t *testing.T) {
	// Per-LLM model from config (or --claude-model flag, which writes
	// to cfg before pickAgents runs) must reach the runtime LLM struct
	// so the invoker can pass it on the CLI command line. Pre-fix this
	// silently dropped on the floor — the roster printed the configured
	// model but the invoker only saw Path.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"claude": {Model: "claude-opus-4-7"},
			"gemini": {Model: "gemini-2.0-flash"},
			"codex":  {Model: "gpt-5"},
		},
	}
	active, _, _ := Select(fakeDetected(), ready, cfg, "", time.Time{})
	want := map[string]string{"claude": "claude-opus-4-7", "gemini": "gemini-2.0-flash", "codex": "gpt-5"}
	for _, llm := range active {
		if llm.Model != want[llm.Name] {
			t.Errorf("%s.Model: want %q, got %q", llm.Name, want[llm.Name], llm.Model)
		}
	}
}

func TestParseOnlyList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"claude", []string{"claude"}},
		{"claude,gemini", []string{"claude", "gemini"}},
		{" claude , gemini ", []string{"claude", "gemini"}},
		{"", nil},     // empty input → empty set, callers don't need a separate guard
		{" ", nil},    // whitespace-only → empty set
		{",,, ", nil}, // delimiters with no names → empty set
		{"claude,,", []string{"claude"}},
	}
	for _, tc := range cases {
		got := ParseOnlyList(tc.in)
		var gotKeys []string
		for k := range got {
			gotKeys = append(gotKeys, k)
		}
		sort.Strings(gotKeys)
		sort.Strings(tc.want)
		if !reflect.DeepEqual(gotKeys, tc.want) {
			t.Errorf("ParseOnlyList(%q): got %v, want %v", tc.in, gotKeys, tc.want)
		}
	}
}

func TestSelect_OnlyWhitespaceFallsThrough(t *testing.T) {
	// `--only " "` previously bypassed the multi-LLM run because " " was
	// non-empty but parsed to {""}, matching no LLMs. Now whitespace-only
	// is treated as "no filter set" and the default behavior kicks in.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, _, _ := Select(fakeDetected(), ready, config.Config{}, "   ", time.Time{})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

// TestDropCLITwins covers the roster dedup: a name with base_url in config
// is a provider agent, so its CLI twin must be dropped before the provider
// twin is appended — otherwise `llms.claude.base_url: ...` double-runs.
func TestDropCLITwins(t *testing.T) {
	cliClaude := cli.LLM{Name: "claude"}                                   // BaseURL == "" → CLI agent
	cliGemini := cli.LLM{Name: "gemini"}                                   // unrelated CLI agent
	provClaude := cli.LLM{Name: "claude", BaseURL: "http://192.0.2.10/v1"} // provider twin

	t.Run("drops CLI twin when name has base_url in config", func(t *testing.T) {
		cfg := config.Config{LLMs: map[string]config.LLMConfig{
			"claude": {BaseURL: "http://192.0.2.10/v1"},
		}}
		got := DropCLITwins([]cli.LLM{cliClaude, cliGemini}, cfg)
		if len(got) != 1 || got[0].Name != "gemini" {
			t.Fatalf("expected only the gemini CLI agent to survive, got %+v", got)
		}
	})

	t.Run("no base_url config leaves the roster unchanged", func(t *testing.T) {
		cfg := config.Config{LLMs: map[string]config.LLMConfig{
			"claude": {Model: "claude-opus"},
		}}
		got := DropCLITwins([]cli.LLM{cliClaude, cliGemini}, cfg)
		if len(got) != 2 {
			t.Fatalf("expected both CLI agents to survive, got %+v", got)
		}
	})

	t.Run("provider twin itself is preserved (only CLI twins drop)", func(t *testing.T) {
		cfg := config.Config{LLMs: map[string]config.LLMConfig{
			"claude": {BaseURL: "http://192.0.2.10/v1"},
		}}
		got := DropCLITwins([]cli.LLM{cliClaude, provClaude}, cfg)
		if len(got) != 1 || got[0].BaseURL == "" {
			t.Fatalf("expected only the provider claude (with BaseURL) to survive, got %+v", got)
		}
	})
}

// --- v0.15 Gemini sunset hardening --------------------------------------

// These tests inject a fixed `now` into Select to exercise the three
// sunset branches without waiting for the wall clock to cross 2026-06-18.
// The matching pure-function tests live in internal/cli/sunset_test.go
// (the date constant, the predicate).

func TestSelect_PostSunsetGeminiAutoDisabled(t *testing.T) {
	// Default behaviour after 2026-06-18: gemini is dropped from
	// the active set and surfaces in the NEW sunsetDropped return
	// — NOT configDisabled. The separation matters because
	// printAgentRoster reuses configDisabled to build the
	// `--only <names>` override suggestion (caught by the v0.15
	// self-review on PR 2/4); bundling sunset reasons in there
	// produced a syntactically broken hint.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	active, configDisabled, sunsetDropped := Select(fakeDetected(), ready, config.Config{}, "", postSunset)
	if got, want := names(active), []string{"claude", "codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v (gemini must be dropped post-sunset)", got, want)
	}
	if len(configDisabled) != 0 {
		t.Errorf("configDisabled MUST stay agent-names-only and empty here (sunset isn't a config-disable): got %v", configDisabled)
	}
	if got, want := sunsetDropped, []string{"gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sunsetDropped: got %v, want %v", got, want)
	}
}

func TestSelect_PostSunsetForceKeepsGemini(t *testing.T) {
	// llms.gemini.force_after_sunset: true opts back in — gemini
	// stays in the active set, both skip lists are empty.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	force := true
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"gemini": {ForceAfterSunset: &force},
		},
	}
	active, configDisabled, sunsetDropped := Select(fakeDetected(), ready, cfg, "", postSunset)
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v (force_after_sunset must keep gemini in)", got, want)
	}
	if len(configDisabled) != 0 || len(sunsetDropped) != 0 {
		t.Errorf("override path: both skip lists must be empty, got configDisabled=%v sunsetDropped=%v", configDisabled, sunsetDropped)
	}
}

func TestSelect_PreSunsetGeminiStaysActive(t *testing.T) {
	// Sanity: before the cutoff, no special behaviour — gemini
	// behaves identically to claude/codex. Guards against an
	// accidentally-flipped predicate.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	preSunset := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)
	active, configDisabled, sunsetDropped := Select(fakeDetected(), ready, config.Config{}, "", preSunset)
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(configDisabled) != 0 || len(sunsetDropped) != 0 {
		t.Errorf("pre-sunset: both skip lists must be empty, got configDisabled=%v sunsetDropped=%v", configDisabled, sunsetDropped)
	}
}

func TestSelect_PostSunsetDoesNotAffectProviderNamedGemini(t *testing.T) {
	// v0.15 pre-release QA (codex) catch: the sunset check matched
	// on `llm.Name` alone, which would auto-drop a user-named
	// provider entry like `llms.gemini: { base_url: http://my-llm/v1 }`
	// post-2026-06-18 — even though the sunset only applies to
	// Google's Gemini CLI subprocess, NOT any agent that happens
	// to share the name. The fix gates the predicate on
	// `llm.BaseURL == ""` (CLI only); providers short-circuit out
	// regardless of name. This test pins that semantic so a future
	// refactor can't quietly re-broaden it.
	ready := map[string]bool{"gemini": true}
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Detected as a PROVIDER named "gemini" (BaseURL set is the
	// kind discriminator used across the codebase).
	detected := []cli.LLM{
		{Name: "gemini", BaseURL: "http://my-self-hosted-llm/v1", Available: true},
	}
	active, _, sunsetDropped := Select(detected, ready, config.Config{}, "", postSunset)
	if got, want := names(active), []string{"gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("user-named provider 'gemini' must NOT be sunset-dropped (the cutoff applies to Google's CLI subprocess only), got active=%v", got)
	}
	if len(sunsetDropped) != 0 {
		t.Errorf("sunsetDropped must be empty for a provider entry named 'gemini', got %v", sunsetDropped)
	}
}

func TestSelect_PostSunsetForceFalseAlsoAutoDisables(t *testing.T) {
	// Edge case: `force_after_sunset: false` is explicitly set
	// (distinct from the default nil). Both should auto-disable —
	// false means "don't force"; only true opts back in.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	force := false
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"gemini": {ForceAfterSunset: &force},
		},
	}
	active, _, sunsetDropped := Select(fakeDetected(), ready, cfg, "", postSunset)
	if got, want := names(active), []string{"claude", "codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v (force=false should NOT keep gemini in)", got, want)
	}
	if got, want := sunsetDropped, []string{"gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sunsetDropped: got %v, want %v", got, want)
	}
}
