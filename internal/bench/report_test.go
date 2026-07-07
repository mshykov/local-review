package bench

import (
	"bytes"
	"strings"
	"testing"
)

// TestWriteUpliftBlock_MeasuredAndUnmeasuredRows covers writeUpliftBlock's
// (and writeUpliftRow's) two branches: an LLM with real baseline numbers
// renders numeric deltas; an LLM with no baseline data at all renders the
// "(not measured)" status line instead. Neither had a direct test before
// the cognitive-complexity refactor pulled writeUpliftRow out of the loop
// — WriteText (report.go's public entry point) was itself untested end to
// end, so exercising writeUpliftBlock directly (this package's existing
// convention for unexported render helpers — see jaccard's own tests)
// covers the extracted code without depending on unrelated 0%-covered
// siblings like writeOverallTable.
func TestWriteUpliftBlock_MeasuredAndUnmeasuredRows(t *testing.T) {
	rep := Report{
		LLMReports: []LLMReport{
			{
				LLM:       "claude",
				F1:        0.91,
				Precision: 0.95,
				Recall:    0.88,
				NoiseRate: 0.1,
				Cases: []CaseScore{
					{BaselineError: "timeout"},
				},
				Baseline: &LLMBaselineAggregate{
					F1:                    0.60,
					Precision:             0.70,
					Recall:                0.55,
					NoiseRate:             0.3,
					MeasuredNonCleanCases: 4,
					MeasuredCleanCases:    2,
				},
			},
			{
				LLM:      "gemini",
				Baseline: nil, // uplift never measured for this LLM
			},
		},
	}
	var buf bytes.Buffer
	if err := writeUpliftBlock(&buf, rep); err != nil {
		t.Fatalf("writeUpliftBlock: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "claude") {
		t.Errorf("expected a row for claude, got:\n%s", out)
	}
	if !strings.Contains(out, "note: baseline failed on 1 case(s)") {
		t.Errorf("expected the partial-baseline-failure note for claude, got:\n%s", out)
	}
	if !strings.Contains(out, "gemini") || !strings.Contains(out, "(not measured)") {
		t.Errorf("expected gemini's row to say (not measured), got:\n%s", out)
	}
}

// fullReportFixture builds a Report that walks every optional section of
// the text renderer at once: a consistency-measured LLM (Cons. column),
// per-language splits, an uplift baseline with partial token-measured
// overhead (inline coverage note), a second LLM with none of those
// (dash/dash branches), and an errored case (":ERR" per-case cell).
func fullReportFixture() Report {
	cons := 0.92
	return Report{
		Dataset:   "bench/dataset",
		CaseCount: 2,
		Mode:      "replay",
		LLMReports: []LLMReport{
			{
				LLM:         "claude",
				Precision:   0.83,
				Recall:      0.71,
				F1:          0.77,
				NoiseRate:   0.50,
				MedianMs:    4500,
				P95Ms:       6100,
				Consistency: &cons,
				Languages: []LanguageScore{
					{Language: "go", F1: 0.89},
					{Language: "python", F1: 0.60},
				},
				Cases: []CaseScore{
					{CaseID: "go-nil-deref", TruePositives: 2, Produced: 2},
					{CaseID: "py-sql-injection", Error: "timeout"},
				},
				Baseline: &LLMBaselineAggregate{
					F1: 0.45, Precision: 0.50, Recall: 0.40, NoiseRate: 0.80,
					MeasuredNonCleanCases: 1, MeasuredCleanCases: 1,
				},
				Overhead: &OverheadAggregate{
					PairedCases:           2,
					TreatmentDurationMs:   9000,
					BaselineDurationMs:    5000,
					TokenMeasuredCases:    1,
					TreatmentInputTokens:  11000,
					TreatmentOutputTokens: 1500,
					BaselineInputTokens:   8000,
					BaselineOutputTokens:  500,
				},
			},
			{
				// No consistency, no languages, no baseline numbers, no
				// overhead: exercises the dash and "(not measured)" branches
				// alongside claude's populated row.
				LLM:       "gemini",
				Precision: 0.60,
				Recall:    0.55,
				F1:        0.57,
				NoiseRate: 1.00,
				MedianMs:  800,
				P95Ms:     950,
				Cases: []CaseScore{
					{CaseID: "go-nil-deref", TruePositives: 1, Produced: 3},
				},
				Baseline: &LLMBaselineAggregate{},
			},
		},
	}
}

// TestWriteText_FullReport drives the public text entry point end to end
// with every optional section active, pinning the overall table (with the
// Cons. column), the per-language grid, the uplift and overhead blocks
// (numeric, dashed, and partial-token-coverage variants), and the per-case
// detail including the ":ERR" cell.
func TestWriteText_FullReport(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, fullReportFixture()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Bench: dataset=bench/dataset  cases=2  mode=replay",
		"Cons.", // consistency column present
		"0.92",  // claude's measured consistency
		"4.5s",  // fmtMs seconds branch (median)
		"800ms", // fmtMs millisecond branch
		"Per-language F1:",
		"claude=0.89  gemini=-", // measured next to sentinel dash
		"Uplift over baseline",
		"0.77 (+0.32)",   // claude's F1 uplift cell
		"(not measured)", // gemini's zero-data baseline row
		"Overhead vs raw model",
		"4.5s (+2.00s)", // paired duration mean + signed delta
		"12k (+4.0k)",   // token mean in k-notation (12500 → "12k": ≥10k uses %.0f) + signed delta
		"tokens measured on 1 of 2 paired cases",
		"Per-case detail:",
		"claude:F1=1.00",
		"py-sql-injection",
		"claude:ERR",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("WriteText output missing %q, got:\n%s", want, out)
		}
	}
}

// TestWriteText_TerseReport pins the minimal shape: no consistency
// measured anywhere → no Cons. column; single language → no per-language
// block; no baseline → no uplift or overhead sections. Single-run benches
// must stay one screen tall.
func TestWriteText_TerseReport(t *testing.T) {
	rep := Report{
		Dataset:   "bench/dataset",
		CaseCount: 1,
		Mode:      "cli",
		LLMReports: []LLMReport{
			{
				LLM:       "codex",
				Precision: 1.0,
				Recall:    0.5,
				F1:        0.67,
				MedianMs:  1200,
				P95Ms:     1200,
				Cases:     []CaseScore{{CaseID: "go-nil-deref", TruePositives: 1, FalseNegatives: 1, Produced: 1}},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, rep); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()

	for _, absent := range []string{"Cons.", "Per-language F1:", "Uplift over baseline", "Overhead vs raw model"} {
		if strings.Contains(out, absent) {
			t.Errorf("terse report unexpectedly contains %q:\n%s", absent, out)
		}
	}
	for _, want := range []string{"codex", "Per-case detail:", "go-nil-deref"} {
		if !strings.Contains(out, want) {
			t.Errorf("terse report missing %q:\n%s", want, out)
		}
	}
}

// TestFmtTokens_UnitBoundaries pins the k-notation cutovers: plain count
// below 1000, one decimal to 10k, whole k above — matching the per-LLM
// completion-line shape users compare against.
func TestFmtTokens_UnitBoundaries(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{999, "999"},
		{1000, "1.0k"},
		{9999, "10.0k"},
		{10000, "10k"},
		{250000, "250k"},
	}
	for _, c := range cases {
		if got := fmtTokens(c.in); got != c.want {
			t.Errorf("fmtTokens(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
