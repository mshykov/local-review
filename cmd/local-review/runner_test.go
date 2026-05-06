package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/multi"
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
	// because applyConfig wasn't called consistently.
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

func TestSelectAgents_ModelCarriesOver(t *testing.T) {
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
	active, _ := selectAgents(fakeDetected(), ready, cfg, &sharedFlags{})
	want := map[string]string{"claude": "claude-opus-4-7", "gemini": "gemini-2.0-flash", "codex": "gpt-5"}
	for _, llm := range active {
		if llm.Model != want[llm.Name] {
			t.Errorf("%s.Model: want %q, got %q", llm.Name, want[llm.Name], llm.Model)
		}
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

func TestApplyFlagsToConfig_MergeWithReflectsInConfig(t *testing.T) {
	// `local-review config --merge-with claude` should print the
	// chosen agent in the rendered YAML's merge.preferred_llm. Pre-fix
	// applyFlagsToConfig didn't touch Merge, so the preview was
	// misleading even though runtime merge selection honored the flag.
	cfg := config.Defaults()
	sf := &sharedFlags{mergeWith: "claude"}
	applyFlagsToConfig(&cfg, sf)
	if cfg.Merge.PreferredLLM != "claude" {
		t.Errorf("Merge.PreferredLLM: want claude, got %q", cfg.Merge.PreferredLLM)
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

// realMergedReportWithMajorFinding is a captured fixture from a real
// claude merge run on this codebase. The previous bullet-only heuristic
// would have correctly blocked on this; the new "any non-placeholder
// line" heuristic does too. Pinned here so heuristic regressions can't
// silently let a real-world blocking review through.
const realMergedReportWithMajorFinding = `# Code Review — Consolidated Report

## Summary
- **Total unique findings**: 6
- **Recommendation**: REQUEST CHANGES

## Critical Issues

*None.*

## Major Issues

- ` + "`runner.go:198-219`" + ` — sectionHasContent is tightly coupled to bullet syntax

  The new implementation only counts a section as having content if it contains a Markdown list item.

  **Fix**: Be more permissive.

## Warnings

- ` + "`main.go:48-58`" + ` — Reintroduces golang.org/x/term

## Conclusion

The change has a major issue worth pushing on before merge.
`

func TestMergedHasBlocking_RealFixture(t *testing.T) {
	if !mergedHasBlocking(realMergedReportWithMajorFinding) {
		t.Error("real merged report with a Major finding must trigger the gate")
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
		{
			name: "prose finding (no list bullet) still blocks",
			md:   "## Critical Issues\nThe code path X has a race condition under load.\n\n## Major Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "numbered-list finding still blocks",
			md:   "## Critical Issues\n*(Block merge — ...)*\n\n1. file:42 — buffer overflow\n",
			want: true,
		},
		{
			name: "*None.* (italic, no parens) is treated as placeholder",
			md:   "## Critical Issues\n\n*None.*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "bare 'None.' line is treated as placeholder",
			md:   "## Critical Issues\nNone.\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "Recommendation: BLOCK MERGE blocks even with empty sections",
			md:   "## Summary\n- **Recommendation**: BLOCK MERGE\n\n## Critical Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "Recommendation: REQUEST CHANGES blocks too",
			md:   "## Summary\n**Recommendation**: REQUEST CHANGES\n\n## Critical Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "Recommendation: APPROVE alone does not block",
			md:   "## Summary\n- **Recommendation**: APPROVE\n\n## Critical Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "alternate heading 'Critical' (without 'Issues') with content blocks",
			md:   "## Critical\n- something is broken at file:42\n\n## Major\n*(None)*\n",
			want: true,
		},
		{
			name: "ALL-CAPS 'CRITICAL ISSUES' heading still blocks",
			md:   "## CRITICAL ISSUES\n- file:99 race condition\n",
			want: true,
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

// Note: this exercises the syntheticDetachedBranch helper directly,
// not resolveCommitBranch (which shells out to git). The shape it pins
// is the user-visible promise: detached HEAD must produce a stable,
// per-commit synthetic name so storage doesn't collide. A future
// "just error in detached HEAD" regression should fail this test loudly.
func TestAnyPerLLMHasBlocking_DefendsAgainstMergerTruncation(t *testing.T) {
	// MaxReviewBytesForMerge truncates each per-LLM review to 8 KB
	// before feeding the merger. A reviewer that places a Critical
	// finding on byte 9000+ would have it dropped from the merger
	// input → merged output → mergedHasBlocking. The on-disk file
	// still has it, but the gate would exit 0. Independent
	// pre-truncation scan closes that gap.
	clean := "## Summary\n- **Recommendation**: APPROVE\n\n## Critical Issues\n*(None)*\n"
	withBlock := strings.Repeat("Filler line.\n", 1000) +
		"\n## Critical Issues\n- **file.go:42** — buffer overflow under load\n"

	if anyPerLLMHasBlocking([]multi.ReviewResult{{LLM: "x", Output: clean}}) {
		t.Error("clean output should not trip the gate")
	}
	if !anyPerLLMHasBlocking([]multi.ReviewResult{{LLM: "x", Output: withBlock}}) {
		t.Error("blocking finding past 8 KB cutoff must still trip the gate")
	}
	mixed := []multi.ReviewResult{
		{LLM: "a", Output: clean},
		{LLM: "b", Output: withBlock},
	}
	if !anyPerLLMHasBlocking(mixed) {
		t.Error("any blocking review in the set must trip the gate")
	}
	if anyPerLLMHasBlocking([]multi.ReviewResult{{LLM: "x", Output: ""}}) {
		t.Error("empty review output must not be treated as blocking")
	}
}

func TestSyntheticDetachedBranch(t *testing.T) {
	for _, branch := range []string{"HEAD", "unknown"} {
		const sha = "abc123def456789012345678901234567890aaaa"
		got := syntheticDetachedBranch(branch, sha)
		want := "detached-abc123d"
		if got != want {
			t.Errorf("syntheticDetachedBranch(%q, %q) = %q, want %q", branch, sha, got, want)
		}
	}
	// A real branch name passes through unchanged.
	if got := syntheticDetachedBranch("feature/x", "abc1234"); got != "feature/x" {
		t.Errorf("real branch should pass through unchanged, got %q", got)
	}
}

func TestClassifyRunMode(t *testing.T) {
	ok := func(name string) multi.ReviewResult { return multi.ReviewResult{LLM: name, Output: "ok"} }
	fail := func(name string) multi.ReviewResult { return multi.ReviewResult{LLM: name, Error: errBlockingFindings} }

	cases := []struct {
		name    string
		results []multi.ReviewResult
		want    runMode
	}{
		{
			name:    "two of three succeed — still a real merge",
			results: []multi.ReviewResult{ok("claude"), ok("gemini"), fail("codex")},
			want:    runModeMerge,
		},
		{
			name:    "all three succeed — real merge",
			results: []multi.ReviewResult{ok("claude"), ok("gemini"), ok("codex")},
			want:    runModeMerge,
		},
		{
			name:    "one of three succeed — degraded, no consensus",
			results: []multi.ReviewResult{ok("claude"), fail("gemini"), fail("codex")},
			want:    runModeDegraded,
		},
		{
			name:    "one of two succeed — degraded",
			results: []multi.ReviewResult{ok("claude"), fail("gemini")},
			want:    runModeDegraded,
		},
		{
			name:    "user picked --only claude — solo, expected",
			results: []multi.ReviewResult{ok("claude")},
			want:    runModeSolo,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRunMode(tc.results); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
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
