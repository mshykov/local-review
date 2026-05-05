package main

import (
	"reflect"
	"sort"
	"testing"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
)

// fakeDetected is the standard 3-CLI setup used across selectAgents tests.
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

func TestSelectAgents_AllActiveNoConfig(t *testing.T) {
	// Default case: 3 detected, all authed, empty config — all 3 run.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, disabled := selectAgents(fakeDetected(), ready, config.Config{}, &sharedFlags{})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty, got %v", disabled)
	}
}

func TestSelectAgents_SkipsUnauthed(t *testing.T) {
	// Codex installed but not authed → skipped silently. Not "disabled
	// in config" — that's a separate state.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": false}
	active, disabled := selectAgents(fakeDetected(), ready, config.Config{}, &sharedFlags{})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled should be empty for unauthed (not config-disabled): %v", disabled)
	}
}

func TestSelectAgents_ConfigDisabledIsReported(t *testing.T) {
	// Codex authed, but config sets enabled:false → skipped AND reported
	// in the configDisabled return so the caller can hint about --only.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: boolPtr(false)},
		},
	}
	active, disabled := selectAgents(fakeDetected(), ready, cfg, &sharedFlags{})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if got, want := disabled, []string{"codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("disabled: got %v, want %v", got, want)
	}
}

func TestSelectAgents_ConfigEnabledNilTreatedAsActive(t *testing.T) {
	// Enabled is *bool; nil must be treated as "run if active". This is
	// the path that lets codex run by default in v0.5+ (was opt-in pre).
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: nil}, // explicit nil
		},
	}
	active, disabled := selectAgents(fakeDetected(), ready, cfg, &sharedFlags{})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty, got %v", disabled)
	}
}

func TestSelectAgents_OnlyFilter(t *testing.T) {
	// --only narrows to listed agents. Config disable is NOT consulted —
	// the flag is the user's explicit override, by design.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"codex": {Enabled: boolPtr(false)}, // would be disabled normally
		},
	}
	active, disabled := selectAgents(fakeDetected(), ready, cfg, &sharedFlags{only: "claude,codex"})
	if got, want := names(active), []string{"claude", "codex"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
	// configDisabled is not populated when --only is set; the user is
	// already aware they're overriding.
	if len(disabled) != 0 {
		t.Errorf("disabled: want empty when --only is set, got %v", disabled)
	}
}

func TestSelectAgents_OnlySkipsNotReady(t *testing.T) {
	// --only mentioning an unauthed agent silently drops it (vs erroring),
	// matching how the no-flag path behaves. Doctor is the diagnostic; the
	// runner stays quiet for clean script output.
	ready := map[string]bool{"claude": true, "gemini": false, "codex": false}
	active, _ := selectAgents(fakeDetected(), ready, config.Config{}, &sharedFlags{only: "gemini,claude"})
	if got, want := names(active), []string{"claude"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

func TestSelectAgents_OnlyTrimsSpaces(t *testing.T) {
	// `--only  claude , gemini ` (extra whitespace from copy-paste) must
	// still parse. Common typo, easy to handle, no reason to be strict.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, _ := selectAgents(fakeDetected(), ready, config.Config{}, &sharedFlags{only: " claude , gemini "})
	if got, want := names(active), []string{"claude", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

func TestSelectAgents_TimeoutCarriesOver(t *testing.T) {
	// Per-LLM timeout from config must be threaded through; previously a
	// codex review with 240s timeout would silently get the default 120
	// because withTimeout wasn't called consistently.
	ready := map[string]bool{"claude": true}
	cfg := config.Config{
		LLMs: map[string]config.LLMConfig{
			"claude": {TimeoutSec: 240},
		},
	}
	active, _ := selectAgents(fakeDetected(), ready, cfg, &sharedFlags{})
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

func TestApplyFlagsToConfig_PerAgentModelOverride(t *testing.T) {
	cfg := config.Defaults()
	sf := &sharedFlags{
		claudeModel: "claude-opus-4-7",
		geminiModel: "gemini-2.0-flash",
		codexModel:  "gpt-5",
	}
	applyFlagsToConfig(&cfg, sf)
	if got := cfg.LLMs["claude"].Model; got != "claude-opus-4-7" {
		t.Errorf("claude model: got %q", got)
	}
	if got := cfg.LLMs["gemini"].Model; got != "gemini-2.0-flash" {
		t.Errorf("gemini model: got %q", got)
	}
	if got := cfg.LLMs["codex"].Model; got != "gpt-5" {
		t.Errorf("codex model: got %q", got)
	}
}

func TestApplyFlagsToConfig_PerAgentModelOnEmptyMap(t *testing.T) {
	// User config can omit `llms:` entirely; setting --claude-model on
	// an empty config used to nil-deref. Must initialize the map.
	cfg := config.Config{}
	sf := &sharedFlags{claudeModel: "claude-opus-4-7"}
	applyFlagsToConfig(&cfg, sf)
	if got := cfg.LLMs["claude"].Model; got != "claude-opus-4-7" {
		t.Errorf("model on empty cfg: got %q", got)
	}
}

func TestApplyFlagsToConfig_v0SingleLLMFlags(t *testing.T) {
	cfg := config.Defaults()
	sf := &sharedFlags{
		model:       "gpt-4o",
		baseURL:     "https://example.test/v1",
		minSeverity: "major",
		maxFindings: 5,
	}
	applyFlagsToConfig(&cfg, sf)
	if cfg.Provider.Model != "gpt-4o" {
		t.Errorf("provider model: %q", cfg.Provider.Model)
	}
	if cfg.Provider.BaseURL != "https://example.test/v1" {
		t.Errorf("base url: %q", cfg.Provider.BaseURL)
	}
	if cfg.Review.MinSeverity != "major" {
		t.Errorf("min severity: %q", cfg.Review.MinSeverity)
	}
	if cfg.Review.MaxFindings != 5 {
		t.Errorf("max findings: %d", cfg.Review.MaxFindings)
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
		{"", nil},          // empty input → empty set, callers don't need a separate guard
		{" ", nil},         // whitespace-only → empty set
		{",,, ", nil},      // delimiters with no names → empty set
		{"claude,,", []string{"claude"}},
	}
	for _, tc := range cases {
		got := parseOnlyList(tc.in)
		var gotKeys []string
		for k := range got {
			gotKeys = append(gotKeys, k)
		}
		sort.Strings(gotKeys)
		sort.Strings(tc.want)
		if !reflect.DeepEqual(gotKeys, tc.want) {
			t.Errorf("parseOnlyList(%q): got %v, want %v", tc.in, gotKeys, tc.want)
		}
	}
}

func TestSelectAgents_OnlyWhitespaceFallsThrough(t *testing.T) {
	// `--only " "` previously bypassed the multi-LLM run because " " was
	// non-empty but parsed to {""}, matching no LLMs. Now whitespace-only
	// is treated as "no filter set" and the default behavior kicks in.
	ready := map[string]bool{"claude": true, "gemini": true, "codex": true}
	active, _ := selectAgents(fakeDetected(), ready, config.Config{}, &sharedFlags{only: "   "})
	if got, want := names(active), []string{"claude", "codex", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Errorf("active: got %v, want %v", got, want)
	}
}

func TestMergedHasBlocking(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want bool
	}{
		{
			name: "empty",
			md:   "",
			want: false,
		},
		{
			name: "no critical or major sections",
			md:   "# Code Review\n\n## Summary\n\n- 0 findings\n",
			want: false,
		},
		{
			name: "critical section with placeholder only",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "critical section with placeholder description only",
			md:   "## Critical Issues\n*(Block merge — will break production, lose data, or create security holes)*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "critical section with a real finding",
			md:   "## Critical Issues\n\n- **runner.go:42** — buffer overflow when input is very large\n  Fix: bounds-check before write.\n",
			want: true,
		},
		{
			name: "major section with a real finding (critical empty)",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n\n- **runner.go:42** — pre-commit gate broken\n",
			want: true,
		},
		{
			name: "warning-only finding does not block",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n\n## Warnings\n\n- nit on naming\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergedHasBlocking(tc.md); got != tc.want {
				t.Errorf("mergedHasBlocking: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateMergeWith(t *testing.T) {
	active := []cli.LLM{{Name: "claude"}, {Name: "gemini"}}
	cases := []struct {
		mergeWith string
		wantErr   bool
	}{
		{"", false},        // unset is fine
		{"auto", false},    // sentinel is fine
		{"claude", false},  // member of active set
		{"codex", true},    // not active — typo or misconfig
		{"claud", true},    // typo — must error, not silently fall through
	}
	for _, tc := range cases {
		err := validateMergeWith(&sharedFlags{mergeWith: tc.mergeWith}, active)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateMergeWith(%q): err=%v, wantErr=%v", tc.mergeWith, err, tc.wantErr)
		}
	}
}
