package bench

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestWriteMarkdown_Sections covers the three table sections + the
// header line. Snapshot-style — we don't hash the whole output (too
// brittle as fields evolve) but we assert that each documented row
// is present with the right cells, so a regression that drops a
// section or mis-orders columns surfaces immediately.
func TestWriteMarkdown_Sections(t *testing.T) {
	rep := Report{
		Dataset:   "bench/dataset",
		CaseCount: 4,
		Mode:      "replay",
		Generated: time.Date(2026, 5, 9, 7, 14, 0, 0, time.UTC),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.80, Recall: 1.00, F1: 0.89, NoiseRate: 0.00,
				Languages: []LanguageScore{
					{Language: "go", Cases: 2, F1: 1.00},
					{Language: "typescript", Cases: 1, F1: 0.67},
				},
				Cases: []CaseScore{
					{CaseID: "go-bug-1", Language: "go", TruePositives: 2},
					{CaseID: "ts-sql-1", Language: "typescript", TruePositives: 1, FalsePositives: 1},
				},
			},
			{
				LLM:       "codex",
				Precision: 0.50, Recall: 0.50, F1: 0.50, NoiseRate: 0.00,
				Languages: []LanguageScore{
					{Language: "go", Cases: 2, F1: 0.50},
					{Language: "typescript", Cases: 1, F1: 1.00},
				},
				Cases: []CaseScore{
					{CaseID: "go-bug-1", Language: "go", TruePositives: 1, FalsePositives: 1},
					{CaseID: "ts-sql-1", Language: "typescript", TruePositives: 1},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()

	wants := []string{
		"# local-review bench leaderboard",
		"_Dataset: bench/dataset (4 cases)_",
		"_Mode: replay_",
		"## Overall",
		"| LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |",
		"| claude | 0.80 | 1.00 | 0.89 | 0.00 | — |", // no consistency, so —
		"## Per-language F1",
		"| LLM | go (2) | typescript (1) |",
		"| claude | 1.00 | 0.67 |",
		"## Per-case detail",
		"| Case | Lang | claude | codex |",
		"| go-bug-1 | go | F1=",
		"| ts-sql-1 | typescript | F1=",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("markdown output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

func TestWriteMarkdown_OmitsLanguagesWhenSingleLanguage(t *testing.T) {
	// LLMReport.Languages is empty when the dataset has only one
	// language (runner skips the split). The markdown should omit
	// the Per-language section in that case.
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "replay",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Cases: []CaseScore{{CaseID: "go-1", Language: "go"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "## Per-language F1") {
		t.Errorf("Per-language section should be omitted for single-language datasets, got:\n%s", buf.String())
	}
}

func TestWriteMarkdown_RendersConsistencyWhenPresent(t *testing.T) {
	cons := 0.92
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Consistency: &cons, Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "0.92") {
		t.Errorf("consistency 0.92 not rendered:\n%s", buf.String())
	}
}

// TestWriteMarkdown_RendersUpliftBlock verifies the --uplift
// section appears with treatment / baseline / signed delta cells
// when at least one LLM has a Baseline aggregate. Single-pass
// benches (no Baseline) must omit the section entirely.
func TestWriteMarkdown_RendersUpliftBlock(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.83, Recall: 1.00, F1: 0.91, NoiseRate: 0.00,
				Cases: []CaseScore{{CaseID: "x"}},
				Baseline: &LLMBaselineAggregate{
					Precision: 0.59, Recall: 0.70, F1: 0.64, NoiseRate: 0.30,
					MeasuredNonCleanCases: 1, MeasuredCleanCases: 1,
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"## Uplift over baseline",
		"| LLM | F1 (Δ) | Precision (Δ) | Recall (Δ) | Noise (Δ) | Baseline errors |",
		"| claude |", // row exists
		"+0.27",      // F1 delta = 0.91 - 0.64 = +0.27
		"+0.30",      // Recall delta = 1.00 - 0.70 = +0.30
		"-0.30",      // Noise delta = 0.00 - 0.30 = -0.30 (regression direction is good here)
		"| 0 |",      // no baseline errors → "0" in the rightmost cell
	} {
		if !strings.Contains(out, want) {
			t.Errorf("uplift block missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestWriteMarkdown_FlagsBaselineErrors verifies that when --uplift
// recorded baseline failures, the uplift section is shown (so users
// see the partial-coverage warning rather than seeing nothing) and
// the per-LLM row carries a baseline-error count. Without the flag,
// a leaderboard could quietly compare treatment-of-N against
// baseline-of-M-<-N and inflate apparent uplift.
func TestWriteMarkdown_FlagsBaselineErrors(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 2, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.83, Recall: 1.00, F1: 0.91, NoiseRate: 0.0,
				Cases: []CaseScore{
					{CaseID: "x", BaselineError: "timeout after 120s"},
					{CaseID: "y"},
				},
				// Direct-renderer test: simulate an older Report (pre
				// iter-2 fix) or a hand-constructed one where Baseline
				// is nil but BaselineError is set on a case. The
				// renderer must still surface the failure count
				// regardless of how the aggregate got there — robust
				// to both runner-produced and externally-constructed
				// reports.
				Baseline: nil,
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Uplift over baseline") {
		t.Errorf("uplift block must appear when any case has BaselineError; got:\n%s", out)
	}
	if !strings.Contains(out, "1 ⚠") {
		t.Errorf("expected baseline-error count cell to render '1 ⚠'; got:\n%s", out)
	}
}

// TestWriteMarkdown_DashesOutZeroMeasuredCases verifies that an
// attempted-but-fully-failed --uplift run renders "—" in the
// numeric delta cells, not "0.91 (+0.91)" against a zero
// baseline that nobody actually measured. Iter-3 self-review
// flagged the misleading-headline bug here as the major issue
// to close before the PR ships. The aggregate stays in the JSON
// (signal: feature attempted) but the renderer must refuse to
// invent a numeric delta from a phantom baseline.
func TestWriteMarkdown_DashesOutZeroMeasuredCases(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.83, Recall: 1.00, F1: 0.91, NoiseRate: 0.0,
				Cases: []CaseScore{
					{CaseID: "x", BaselineError: "timeout"},
				},
				Baseline: &LLMBaselineAggregate{MeasuredNonCleanCases: 0, MeasuredCleanCases: 0},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Uplift over baseline") {
		t.Fatalf("uplift block must appear; got:\n%s", out)
	}
	// The row must NOT carry numeric delta cells like "0.91 (+0.91)"
	// — the renderer should fall through to the "—" branch.
	if strings.Contains(out, "0.91 (+0.91)") {
		t.Errorf("zero-measured baseline must not produce numeric delta; got:\n%s", out)
	}
	if !strings.Contains(out, "| claude | — | — | — | — |") {
		t.Errorf("expected dashed-out row for claude, got:\n%s", out)
	}
}

// TestWriteMarkdown_AsymmetricCoverageDashesUnmeasuredHalf verifies
// that when baseline succeeded only on clean cases (or only on
// non-clean cases), the renderer shows the half it has data for
// and dashes out the other half. Iter-4 self-review flagged the
// previous all-or-nothing gate as letting a "baseline succeeded
// 0/3 non-clean cases" run still print "F1 0.91 (+0.91)" against
// the phantom zero baseline.
func TestWriteMarkdown_AsymmetricCoverageDashesUnmeasuredHalf(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 2, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				Precision: 0.83, Recall: 1.00, F1: 0.91, NoiseRate: 0.0,
				Cases: []CaseScore{
					{CaseID: "x"},
					{CaseID: "clean", Clean: true},
				},
				// Baseline: only clean coverage measured; non-clean
				// pass errored on every case it touched.
				Baseline: &LLMBaselineAggregate{
					NoiseRate:             0.30,
					MeasuredNonCleanCases: 0,
					MeasuredCleanCases:    1,
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Noise must render numerically (-0.30 delta).
	if !strings.Contains(out, "-0.30") {
		t.Errorf("expected noise delta to render numerically; got:\n%s", out)
	}
	// F1 / Precision / Recall must dash out — no non-clean baseline
	// ever scored. The row shape is "| claude | — | — | — | 0.00 (-0.30) | 0 |".
	if !strings.Contains(out, "| claude | — | — | — |") {
		t.Errorf("expected F1/Precision/Recall to dash out for zero non-clean coverage; got:\n%s", out)
	}
}

func TestWriteMarkdown_OmitsUpliftWhenNotMeasured(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "replay",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Uplift over baseline") {
		t.Errorf("uplift section should be omitted when no LLM has a Baseline; got:\n%s", buf.String())
	}
}

// TestWriteMarkdown_RendersZeroConsistency: a measured-but-zero
// consistency (every run produced totally different findings) must
// render as "0.00" — not be collapsed to "—" alongside unmeasured.
// Codex flagged this in self-review as the worst case the metric
// is supposed to surface.
func TestWriteMarkdown_RendersZeroConsistency(t *testing.T) {
	zero := 0.0
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "cli",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Consistency: &zero, Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatal(err)
	}
	// The cell must show "0.00", and the "—" must not appear inside
	// the consistency column. We assert against the row pattern so
	// the test doesn't false-pass on stray dashes elsewhere.
	if !strings.Contains(buf.String(), "| 0.00 |") {
		t.Errorf("zero consistency should render as 0.00, not be collapsed to —:\n%s", buf.String())
	}
}

// TestWriteJSON_ConsistencyNullForSingleRun verifies that a nil
// Consistency pointer emits as JSON null (field present with null value)
// rather than being absent entirely. The omitempty tag was removed so
// downstream consumers can distinguish "not measured" (null) from
// "measured, zero overlap" (0.0) without a missing-field special case.
func TestWriteJSON_ConsistencyNullForSingleRun(t *testing.T) {
	rep := Report{
		Dataset: "x", CaseCount: 1, Mode: "replay",
		Generated: time.Now(),
		LLMReports: []LLMReport{
			{LLM: "claude", Cases: []CaseScore{{CaseID: "x"}}},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"consistency": null`) {
		t.Errorf("nil Consistency should appear as null in JSON, not be absent:\n%s", out)
	}
}
