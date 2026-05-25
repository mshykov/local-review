package bench

import (
	"math"
	"testing"
)

// These tests pin the conventions of scorePrecision / scoreRecall /
// scoreF1 — most importantly the asymmetric "0 on empty" /
// "1 on empty" pair, which is load-bearing for the per-language
// aggregator. Names follow CLAUDE.md rule 9 (encode the invariant)
// so a regression makes the failing line readable from the failure
// header alone.

func TestScorePrecision(t *testing.T) {
	cases := []struct {
		name   string
		tp, fp int
		want   float64
	}{
		{"perfect", 5, 0, 1.0},
		{"half right", 1, 1, 0.5},
		{"none found", 0, 0, 0}, // no findings produced → 0
		{"only false positives", 0, 3, 0},
		{"two thirds", 2, 1, 2.0 / 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scorePrecision(tc.tp, tc.fp)
			if !nearlyEqual(got, tc.want) {
				t.Errorf("scorePrecision(%d, %d) = %g, want %g", tc.tp, tc.fp, got, tc.want)
			}
		})
	}
}

// scoreRecall's "1 when no expected" convention is the asymmetric
// half that drives clean-case scoring. If a regression flips this
// to 0, every clean case in the leaderboard silently shifts its
// per-LLM F1 average down. Pin it explicitly.
func TestScoreRecall(t *testing.T) {
	cases := []struct {
		name   string
		tp, fn int
		want   float64
	}{
		{"perfect", 5, 0, 1.0},
		{"missed everything", 0, 3, 0},
		{"half caught", 1, 1, 0.5},
		{"clean case (no expected)", 0, 0, 1.0}, // load-bearing convention
		{"two thirds caught", 2, 1, 2.0 / 3.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreRecall(tc.tp, tc.fn)
			if !nearlyEqual(got, tc.want) {
				t.Errorf("scoreRecall(%d, %d) = %g, want %g", tc.tp, tc.fn, got, tc.want)
			}
		})
	}
}

func TestScoreF1(t *testing.T) {
	cases := []struct {
		name string
		p, r float64
		want float64
	}{
		{"perfect", 1.0, 1.0, 1.0},
		{"both zero", 0, 0, 0}, // 0 / 0 would NaN — pinned to 0 instead
		{"half/half", 0.5, 0.5, 0.5},
		{"high precision low recall", 1.0, 0.5, 2.0 * 1.0 * 0.5 / 1.5},
		{"low precision high recall", 0.5, 1.0, 2.0 * 0.5 * 1.0 / 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreF1(tc.p, tc.r)
			if !nearlyEqual(got, tc.want) {
				t.Errorf("scoreF1(%g, %g) = %g, want %g", tc.p, tc.r, got, tc.want)
			}
		})
	}
}

// Sanity check: BaselineScore and CaseScore both delegate to the
// helpers, so identical inputs must produce identical outputs
// across the two score types. Without this, a future maintainer
// who edits one delegation but forgets the other gets a green
// build but a divergent leaderboard. The test exists as a sentinel.
func TestScoreTypes_ShareSameMath(t *testing.T) {
	cases := []struct {
		tp, fp, fn int
	}{
		{5, 0, 0}, // perfect
		{0, 0, 0}, // empty
		{2, 1, 2}, // mixed
		{0, 3, 0}, // noise on clean
		{0, 0, 5}, // missed everything
	}
	for _, tc := range cases {
		baseline := BaselineScore{
			TruePositives:  tc.tp,
			FalsePositives: tc.fp,
			FalseNegatives: tc.fn,
		}
		caseS := CaseScore{
			TruePositives:  tc.tp,
			FalsePositives: tc.fp,
			FalseNegatives: tc.fn,
		}
		if !nearlyEqual(baseline.Precision(), caseS.Precision()) {
			t.Errorf("Precision diverges at (%d,%d,%d): baseline=%g caseScore=%g", tc.tp, tc.fp, tc.fn, baseline.Precision(), caseS.Precision())
		}
		if !nearlyEqual(baseline.Recall(), caseS.Recall()) {
			t.Errorf("Recall diverges at (%d,%d,%d): baseline=%g caseScore=%g", tc.tp, tc.fp, tc.fn, baseline.Recall(), caseS.Recall())
		}
		if !nearlyEqual(baseline.F1(), caseS.F1()) {
			t.Errorf("F1 diverges at (%d,%d,%d): baseline=%g caseScore=%g", tc.tp, tc.fp, tc.fn, baseline.F1(), caseS.F1())
		}
	}
}

// nearlyEqual is a float comparison helper for assertions that
// would otherwise mis-fire on representation noise (e.g. 0.5
// vs 0.49999999...). 1e-9 is well below the precision the
// leaderboard ever renders (4 decimal places) so it can't mask
// a real bug.
func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
