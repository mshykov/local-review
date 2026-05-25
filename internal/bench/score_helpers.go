package bench

// score_helpers.go is the single source of truth for the
// precision/recall/F1 formulas. Both BaselineScore (from --uplift's
// baseline pass) and CaseScore (the primary treatment pass) used to
// implement these methods inline with identical formulas; the
// duplication was flagged in v0.10.0's audit/tech-debt.md (major
// finding on internal/bench/types.go) as a hidden-divergence risk —
// any future improvement to the scoring math had to land in two
// places, and a missed update would silently produce two different
// leaderboard numbers for "the same" metric.
//
// These helpers are unexported because no caller outside the bench
// package needs the raw TP/FP/FN → float math. The exported surface
// is still BaselineScore.Precision() / Recall() / F1() and the
// CaseScore equivalents; those now route here.
//
// All three functions are pure: no side effects, no allocations,
// fully determined by their arguments. Test posture: pinned in
// score_helpers_test.go with the edge cases that drove the
// "Returns 0 / Returns 1" conventions (clean cases, empty sets).

// scorePrecision returns TP / (TP + FP).
//
// Returns 0 when no findings were produced (TP+FP == 0). The
// "0 on empty" convention exists because the alternative —
// returning NaN, sentinel −1, or panicking — would each leak into
// the leaderboard's float aggregation downstream. NaN propagates
// through sums; sentinel −1 would silently weight averages
// negatively; panic would crash a partial run.
func scorePrecision(tp, fp int) float64 {
	if tp+fp == 0 {
		return 0
	}
	return float64(tp) / float64(tp+fp)
}

// scoreRecall returns TP / (TP + FN).
//
// Returns 1 when no expected findings exist (TP+FN == 0). The
// "1 on empty" convention is asymmetric with scorePrecision's
// "0 on empty" because the semantic meaning differs:
//
//   - "No produced findings, nothing to evaluate precision against"
//     reads cleanest as 0 — the reviewer didn't earn any precision.
//   - "No expected findings (clean case), nothing for the reviewer
//     to catch" reads cleanest as 1 — the reviewer "caught all of
//     the (zero) bugs we expected." Noise on clean cases is
//     captured separately in CaseReport.NoiseRate (not folded into
//     recall) so this convention doesn't shadow a real failure.
//
// This pair of conventions is load-bearing for the per-language
// aggregator: a language slice with all-clean cases gets
// recall=1, precision=0, F1=0 — a shape that downstream code
// recognises as "no signal, skip from the aggregate" rather than
// "perfect score on a degenerate set."
func scoreRecall(tp, fn int) float64 {
	if tp+fn == 0 {
		return 1
	}
	return float64(tp) / float64(tp+fn)
}

// scoreF1 is the harmonic mean of precision and recall:
// 2*P*R / (P+R). Returns 0 when both are zero.
//
// Takes precomputed P and R (rather than TP/FP/FN) so callers can
// reuse the values they've already computed for display — saves
// two redundant computations in the BaselineScore.F1() and
// CaseScore.F1() call sites that print all three.
func scoreF1(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}
