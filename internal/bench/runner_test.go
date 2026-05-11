package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

func TestRun_ReplayMode_EndToEnd(t *testing.T) {
	dataset := t.TempDir()
	mkCase(t, dataset, "go-bug-1", `id: go-bug-1
title: nil deref
language: go
expected:
  - file: foo.go
    line: 10
    note: nil deref after err check
`)
	mkCase(t, dataset, "clean-1", `id: clean-1
title: a clean diff
language: go
clean: true
`)

	fixtures := t.TempDir()
	mkFixture(t, fixtures, "go-bug-1", "claude", "## Major Issues\n\n- foo.go:11 — possible nil deref\n")
	mkFixture(t, fixtures, "go-bug-1", "gemini", "## Warnings\n\n- bar.go:99 — irrelevant\n")
	mkFixture(t, fixtures, "clean-1", "claude", "No issues found.\n")
	mkFixture(t, fixtures, "clean-1", "gemini", "## Warnings\n\n- a.go:1 — spurious\n- a.go:2 — spurious\n")

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}, {Name: "gemini"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rep.CaseCount != 2 || rep.Mode != "replay" || len(rep.LLMReports) != 2 {
		t.Fatalf("report shape unexpected: %+v", rep)
	}

	claude := rep.LLMReports[0]
	if claude.LLM != "claude" {
		t.Fatalf("expected claude first (alphabetical), got %s", claude.LLM)
	}
	// claude caught the bug (within ±3 of line 10) and produced no
	// findings on the clean case → precision=recall=F1=1.0, noise=0.
	if claude.Precision != 1.0 || claude.Recall != 1.0 || claude.F1 != 1.0 {
		t.Errorf("claude scores: P=%v R=%v F1=%v", claude.Precision, claude.Recall, claude.F1)
	}
	if claude.NoiseRate != 0 {
		t.Errorf("claude noise: got %v want 0", claude.NoiseRate)
	}

	gemini := rep.LLMReports[1]
	// gemini missed the real bug AND produced 2 spurious findings on
	// the clean case → recall=0, noise=2.
	if gemini.Recall != 0 {
		t.Errorf("gemini recall: got %v want 0", gemini.Recall)
	}
	if gemini.NoiseRate != 2.0 {
		t.Errorf("gemini noise: got %v want 2.0", gemini.NoiseRate)
	}
}

func TestRun_ReplayMode_MissingFixtureRecordedAsError(t *testing.T) {
	dataset := t.TempDir()
	mkCase(t, dataset, "case-x", `id: case-x
title: x
language: go
expected:
  - file: x.go
    line: 1
`)

	fixtures := t.TempDir() // no fixture written

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(rep.LLMReports) != 1 || len(rep.LLMReports[0].Cases) != 1 {
		t.Fatalf("report shape: %+v", rep)
	}
	cs := rep.LLMReports[0].Cases[0]
	if cs.Error == "" {
		t.Errorf("missing fixture should record error on the CaseScore, got %+v", cs)
	}
}

func TestRun_NoLLMsIsError(t *testing.T) {
	if _, err := Run(context.Background(), []Case{{ID: "x"}}, Options{}); err == nil {
		t.Error("expected error when LLMs slice is empty")
	}
}

func TestRun_ReplayWithoutDirIsError(t *testing.T) {
	_, err := Run(context.Background(), []Case{{ID: "x"}}, Options{
		LLMs:   []cli.LLM{{Name: "claude"}},
		Source: SourceReplay,
	})
	if err == nil {
		t.Error("expected error when replay mode is selected without a fixtures dir")
	}
}

func TestFillAggregates_P95UsesCeilNearestRank(t *testing.T) {
	// Regression: the prior `(n*95)/100` form picked the maximum for
	// n=20 instead of the documented "second-highest of 20". Build
	// a synthetic LLMReport with 20 cases and verify p95 is the
	// 19th-highest value (index 18), not the 20th.
	lr := &LLMReport{}
	durations := make([]time.Duration, 20)
	for i := range durations {
		durations[i] = time.Duration(i+1) * time.Millisecond // 1ms..20ms
	}
	fillAggregates(lr, durations)
	if got, want := lr.P95Ms, int64(19); got != want {
		t.Errorf("p95 for n=20: got %dms, want %dms (index 18, value 19ms)", got, want)
	}

	// n=1 stays at the only sample, no panic.
	lr2 := &LLMReport{}
	fillAggregates(lr2, []time.Duration{42 * time.Millisecond})
	if lr2.P95Ms != 42 {
		t.Errorf("p95 for n=1: got %dms, want 42ms", lr2.P95Ms)
	}
}

func TestReadFixture_RejectsPathTraversal(t *testing.T) {
	// Replay-mode fixture lookups must refuse identifiers that
	// could escape the replay root. The case id and llm name flow
	// from --only and from on-disk directory names, both of which
	// are user-controlled; a malicious "../../etc/passwd" id used
	// to read whatever the bench process could.
	cases := []struct{ caseID, llmName string }{
		{"../etc", "claude"},
		{"normal", "../passwd"},
		{".", "claude"},
		{"..", "claude"},
		{"a/b", "claude"},
		{"normal", "claude/x"},
		{"normal", ""},
		{"", "claude"},
	}
	for _, tc := range cases {
		_, err := readFixture(t.TempDir(), tc.caseID, tc.llmName)
		if err == nil {
			t.Errorf("readFixture(%q, %q) should reject as unsafe identifier", tc.caseID, tc.llmName)
		}
	}
}

func TestRun_PerLanguageAggregates(t *testing.T) {
	// Phase-2: when the dataset spans more than one language, every
	// LLMReport should carry per-language aggregates sorted by id.
	dataset := t.TempDir()
	mkCase(t, dataset, "go-bug-1", `id: go-bug-1
title: go bug
language: go
expected:
  - file: foo.go
    line: 10
`)
	mkCase(t, dataset, "ts-bug-1", `id: ts-bug-1
title: ts bug
language: typescript
expected:
  - file: bar.ts
    line: 5
`)

	fixtures := t.TempDir()
	mkFixture(t, fixtures, "go-bug-1", "claude", "## Major Issues\n\n- foo.go:10 — bug\n")
	mkFixture(t, fixtures, "ts-bug-1", "claude", "## Major Issues\n\n- bar.ts:5 — bug\n")

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	lr := rep.LLMReports[0]
	if len(lr.Languages) != 2 {
		t.Fatalf("expected 2 language aggregates (go, typescript), got %d: %+v", len(lr.Languages), lr.Languages)
	}
	if lr.Languages[0].Language != "go" || lr.Languages[1].Language != "typescript" {
		t.Errorf("languages should be sorted alphabetically: got %s, %s", lr.Languages[0].Language, lr.Languages[1].Language)
	}
	for _, ls := range lr.Languages {
		if ls.F1 != 1.0 {
			t.Errorf("language %s F1 = %v, want 1.0", ls.Language, ls.F1)
		}
	}
	// Per-case scores should also carry the language.
	for _, cs := range lr.Cases {
		if cs.Language == "" {
			t.Errorf("case %q missing Language; should be propagated from Case.Language", cs.CaseID)
		}
	}
}

func TestRun_PerLanguageAggregates_OmittedWhenSingleLanguage(t *testing.T) {
	// When the dataset is single-language the aggregator skips the
	// split — saves clutter in the report and JSON for the common
	// "I'm only benching Go right now" case.
	dataset := t.TempDir()
	mkCase(t, dataset, "go-bug-1", `id: go-bug-1
title: go bug
language: go
expected:
  - file: foo.go
    line: 10
`)
	fixtures := t.TempDir()
	mkFixture(t, fixtures, "go-bug-1", "claude", "## Major Issues\n\n- foo.go:10 — bug\n")

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.LLMReports[0].Languages) != 0 {
		t.Errorf("single-language dataset should have no per-language split; got %+v", rep.LLMReports[0].Languages)
	}
}

func TestRun_ReplayWithUpliftIsError(t *testing.T) {
	// --uplift is meaningless in replay mode (need real LLM calls
	// to measure the baseline pass). Run must reject the
	// combination so users don't ship a misleading "uplift = 0"
	// to their leaderboard from cached fixtures.
	_, err := Run(context.Background(), []Case{{ID: "x"}}, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: t.TempDir(),
		Uplift:    true,
	})
	if err == nil {
		t.Error("expected error when --uplift is used with replay mode")
	}
}

// TestFillBaselineAggregate_MicroAveragesNonClean verifies the
// baseline rollup uses the same "skip clean cases for P/R/F1,
// count them for noise" rule the treatment side uses. Without
// matching semantics, treatment-vs-baseline deltas become apples
// to oranges.
func TestFillBaselineAggregate_MicroAveragesNonClean(t *testing.T) {
	lr := &LLMReport{
		Cases: []CaseScore{
			// Non-clean case: bug at line 10, baseline missed it,
			// treatment caught it.
			{
				CaseID:        "go-bug-1",
				TruePositives: 1, FalsePositives: 0, FalseNegatives: 0,
				Matched: []MatchPair{{}},
				Baseline: &BaselineScore{
					TruePositives: 0, FalsePositives: 0, FalseNegatives: 1,
					DurationMs: 100,
				},
			},
			// Clean case: treatment silent, baseline produced 2 noisy findings.
			{
				CaseID:   "clean-1",
				Clean:    true,
				Produced: 0,
				Baseline: &BaselineScore{
					Produced:   2,
					DurationMs: 80,
				},
			},
		},
	}
	fillBaselineAggregate(lr)
	if lr.Baseline == nil {
		t.Fatal("expected Baseline to be non-nil after fillBaselineAggregate")
	}
	// Baseline P/R/F1 over the single non-clean case: TP=0, FN=1
	// → recall=0, precision=0, F1=0.
	if lr.Baseline.F1 != 0 {
		t.Errorf("baseline F1 = %v, want 0 (1/1 missed)", lr.Baseline.F1)
	}
	// Baseline noise: 2 findings on 1 clean case → 2.0
	if lr.Baseline.NoiseRate != 2.0 {
		t.Errorf("baseline noise = %v, want 2.0", lr.Baseline.NoiseRate)
	}
	// Total duration sums both passes.
	if lr.Baseline.TotalDurationMs != 180 {
		t.Errorf("baseline total duration = %v, want 180", lr.Baseline.TotalDurationMs)
	}
	// MeasuredNonCleanCases / MeasuredCleanCases are the per-bucket
	// coverage counts. Renderers gate F1/Precision/Recall on the
	// non-clean count and Noise on the clean count — splitting the
	// gates is what stops a "baseline succeeded only on clean
	// cases" run from rendering a phantom F1 delta against zero.
	if lr.Baseline.MeasuredNonCleanCases != 1 {
		t.Errorf("MeasuredNonCleanCases = %d, want 1", lr.Baseline.MeasuredNonCleanCases)
	}
	if lr.Baseline.MeasuredCleanCases != 1 {
		t.Errorf("MeasuredCleanCases = %d, want 1", lr.Baseline.MeasuredCleanCases)
	}
}

func TestFillBaselineAggregate_NilWhenNoCaseMeasured(t *testing.T) {
	// Single-pass benches (no --uplift) must have a nil Baseline
	// on the LLMReport so JSON consumers can distinguish "not
	// measured" (absent) from "measured-zero" (present, all zeros).
	lr := &LLMReport{
		Cases: []CaseScore{
			{CaseID: "x", TruePositives: 1, Matched: []MatchPair{{}}},
		},
	}
	fillBaselineAggregate(lr)
	if lr.Baseline != nil {
		t.Errorf("Baseline should be nil when no case was measured, got %+v", lr.Baseline)
	}
}

// TestFillBaselineAggregate_ZeroAggregateWhenAllBaselinesErrored
// covers the "uplift attempted, every baseline pass errored" gap.
// Without an explicit zero-valued aggregate in this case, JSON
// consumers can't distinguish "uplift not run" (Baseline absent)
// from "uplift run, every baseline crashed" — the latter is
// actionable signal (re-record fixtures, raise the timeout) the
// former isn't. Iter-2 self-review (codex consensus) called this
// out as a major aggregate-contract bug.
func TestFillBaselineAggregate_ZeroAggregateWhenAllBaselinesErrored(t *testing.T) {
	lr := &LLMReport{
		Cases: []CaseScore{
			{CaseID: "x", TruePositives: 1, Matched: []MatchPair{{}}, BaselineError: "timeout"},
			{CaseID: "y", TruePositives: 0, BaselineError: "exit 1"},
		},
	}
	fillBaselineAggregate(lr)
	if lr.Baseline == nil {
		t.Fatal("Baseline must be a (zero-valued) aggregate when --uplift was attempted; got nil")
	}
	if lr.Baseline.F1 != 0 || lr.Baseline.Precision != 0 || lr.Baseline.Recall != 0 || lr.Baseline.NoiseRate != 0 {
		t.Errorf("aggregate should be zero-valued when every baseline errored, got %+v", lr.Baseline)
	}
	// Both per-bucket counts must be zero when every baseline
	// errored — that's the renderer's signal to render "—" in
	// every delta cell. Without these, the report would print
	// "0.91 (+0.91)" against an unmeasured baseline.
	if lr.Baseline.MeasuredNonCleanCases != 0 || lr.Baseline.MeasuredCleanCases != 0 {
		t.Errorf("Measured*Cases must be 0 when every baseline errored, got non-clean=%d clean=%d",
			lr.Baseline.MeasuredNonCleanCases, lr.Baseline.MeasuredCleanCases)
	}
}

// TestFillTreatmentTokenAggregate_SumsAcrossSuccessfulCases verifies
// that the new v0.9.0 token rollup on LLMReport sums tokens only
// from non-error case frames and uses MeasuredCases as the
// denominator. Error frames carry zero tokens by construction
// (buildBaseScore on err path); averaging over len(Cases) would
// have understated mean-per-case whenever any case errored, which
// is exactly the noise the "Overhead vs raw model" leaderboard is
// supposed to surface honestly.
func TestFillTreatmentTokenAggregate_SumsAcrossSuccessfulCases(t *testing.T) {
	lr := &LLMReport{
		Cases: []CaseScore{
			{CaseID: "ok-1", InputTokens: 1000, OutputTokens: 200},
			{CaseID: "ok-2", InputTokens: 1500, OutputTokens: 300},
			{CaseID: "err-1", Error: "timeout"}, // must not be counted
		},
	}
	fillTreatmentTokenAggregate(lr)
	if lr.MeasuredCases != 2 {
		t.Errorf("MeasuredCases = %d, want 2 (errored case excluded)", lr.MeasuredCases)
	}
	if lr.TotalInputTokens != 2500 {
		t.Errorf("TotalInputTokens = %d, want 2500", lr.TotalInputTokens)
	}
	if lr.TotalOutputTokens != 500 {
		t.Errorf("TotalOutputTokens = %d, want 500", lr.TotalOutputTokens)
	}
}

// TestFillBaselineAggregate_SumsTokens covers the baseline-side
// counterpart: the rolled-up LLMBaselineAggregate must carry
// TotalInputTokens / TotalOutputTokens so the leaderboard can
// compute the per-case baseline mean against which the treatment
// mean is compared in the "Overhead vs raw model" table.
func TestFillBaselineAggregate_SumsTokens(t *testing.T) {
	lr := &LLMReport{
		Cases: []CaseScore{
			{
				CaseID:        "a",
				TruePositives: 1, Matched: []MatchPair{{}},
				Baseline: &BaselineScore{
					TruePositives: 1, DurationMs: 100,
					InputTokens: 800, OutputTokens: 120,
				},
			},
			{
				CaseID: "b", Clean: true,
				Baseline: &BaselineScore{
					Produced: 1, DurationMs: 80,
					InputTokens: 600, OutputTokens: 60,
				},
			},
		},
	}
	fillBaselineAggregate(lr)
	if lr.Baseline == nil {
		t.Fatal("expected non-nil Baseline aggregate")
	}
	if lr.Baseline.TotalInputTokens != 1400 {
		t.Errorf("baseline TotalInputTokens = %d, want 1400", lr.Baseline.TotalInputTokens)
	}
	if lr.Baseline.TotalOutputTokens != 180 {
		t.Errorf("baseline TotalOutputTokens = %d, want 180", lr.Baseline.TotalOutputTokens)
	}
}

func TestRun_ReplayWithRepeatIsError(t *testing.T) {
	// --repeat > 1 is meaningless in replay (fixtures are
	// deterministic; Jaccard would always be 1.0). Run must reject
	// the combination so users don't ship a meaningless number to
	// their leaderboard.
	_, err := Run(context.Background(), []Case{{ID: "x"}}, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: t.TempDir(),
		Repeat:    5,
	})
	if err == nil {
		t.Error("expected error when --repeat > 1 in replay mode")
	}
}

func TestRun_RecallZeroWhenAllNonCleanCasesError(t *testing.T) {
	// Regression: prior fillAggregates returned Recall=1.0 when
	// tp+fn==0, which masked the failure mode where every non-clean
	// fixture errored. The aggregate should now stay at 0 so a
	// regression is visible.
	dataset := t.TempDir()
	mkCase(t, dataset, "case-x", `id: case-x
title: x
language: go
expected:
  - file: x.go
    line: 1
`)
	mkCase(t, dataset, "clean-y", `id: clean-y
title: clean
language: go
clean: true
`)

	fixtures := t.TempDir()
	// Only the clean case has a fixture; case-x will error.
	mkFixture(t, fixtures, "clean-y", "claude", "## Info / Notes\n\n*(None)*\n")

	cases, err := LoadDataset(dataset)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	rep, err := Run(context.Background(), cases, Options{
		LLMs:      []cli.LLM{{Name: "claude"}},
		Source:    SourceReplay,
		ReplayDir: fixtures,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(rep.LLMReports) != 1 {
		t.Fatalf("expected 1 llm report, got %d", len(rep.LLMReports))
	}
	lr := rep.LLMReports[0]
	if lr.Precision != 0 || lr.Recall != 0 || lr.F1 != 0 {
		t.Errorf("when no non-clean case scored, all aggregates should be 0; got P=%v R=%v F1=%v",
			lr.Precision, lr.Recall, lr.F1)
	}
}

func TestFillLanguageAggregates_OmitsLanguageWhenAllCasesErrored(t *testing.T) {
	// Per-language aggregate for a language where every case errored
	// should be omitted (not emit F1=0.00, which looks like "missed
	// everything" rather than "no data"). The text/markdown renderer
	// then shows "—" for that language via the languageF1 sentinel.
	lr := &LLMReport{
		Cases: []CaseScore{
			{CaseID: "go-bug-1", Language: "go", TruePositives: 1},
			{CaseID: "ts-bug-1", Language: "typescript", Error: "api error"},
		},
	}
	fillLanguageAggregates(lr)

	if len(lr.Languages) != 1 {
		t.Fatalf("expected 1 language aggregate (go only), got %d: %+v", len(lr.Languages), lr.Languages)
	}
	if lr.Languages[0].Language != "go" {
		t.Errorf("expected go, got %s", lr.Languages[0].Language)
	}
}

func TestCaseScore_JaccardPointerDistinguishesMeasuredZero(t *testing.T) {
	// Jaccard is *float64 so a measured-but-zero value (no overlap
	// across any run) is distinguishable from "not measured" (nil).
	// This pins the fix for the omitempty-float64 bug where 0.0
	// would be silently dropped from JSON output.
	notMeasured := CaseScore{RunCount: 0}
	if notMeasured.Jaccard != nil {
		t.Errorf("single-run CaseScore should have nil Jaccard, got %v", notMeasured.Jaccard)
	}

	zero := 0.0
	measured := CaseScore{RunCount: 2, Jaccard: &zero}
	if measured.Jaccard == nil {
		t.Error("multi-run CaseScore with zero Jaccard should have non-nil pointer")
	}
	if *measured.Jaccard != 0.0 {
		t.Errorf("expected 0.0, got %v", *measured.Jaccard)
	}
}

func mkCase(t *testing.T, root, id, yaml string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "case.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), []byte("placeholder diff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkFixture(t *testing.T, root, caseID, llmName, body string) {
	t.Helper()
	dir := filepath.Join(root, caseID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, llmName+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
