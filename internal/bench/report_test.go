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
