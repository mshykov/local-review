package bench

import "strings"

// defaultLineWindow is the ±line tolerance used when ExpectedFinding
// .Window is unset. LLMs land within a few lines of the real bug
// location consistently — three is empirically big enough to absorb
// "the LLM pointed at the function header instead of the bad line"
// without being so big it counts neighbouring expected findings as
// the same one.
const defaultLineWindow = 3

// Score matches a slice of produced findings against the expected
// findings for one case and returns the resulting CaseScore.
//
// Matching algorithm: greedy "closest first" pass.
//
//  1. For each expected finding (in case-yaml order), find the
//     closest produced finding that shares a file (suffix match)
//     and lies within Window lines.
//  2. Each produced finding can satisfy at most one expected — once
//     used, it's removed from the candidate pool. This prevents one
//     verbose LLM bullet from "covering" multiple unrelated expected
//     findings on adjacent lines.
//  3. Unmatched expected → false negative ("MISSED").
//  4. Unmatched produced → false positive ("EXTRA").
//
// Phase 1 trade-off: greedy matching is order-dependent in
// pathological cases where two expected findings could each match
// either of two produced ones. For line-distance costs this rarely
// bites in practice (within-window matches at most differ by a few
// lines, and the closest-first pick approaches optimal whenever
// costs are monotonic in position). Phase 2 (≥30 cases) re-evaluates
// whether Hungarian-style bipartite matching is worth the
// dependency footprint — at the small Ns Phase 1 ships, the
// greedy outcome and the optimal one match on every case in the
// initial dataset.
//
// For Clean cases, every produced finding is a false positive and
// nothing is "missed" — recall is 1.0 by definition (CaseScore.Recall
// returns 1 when expected is empty).
func Score(c Case, produced []ProducedFinding) CaseScore {
	cs := CaseScore{
		CaseID:   c.ID,
		Produced: len(produced),
		// Clean propagates from the dataset (Case.Clean OR no
		// expected findings). Decoupled from TP/FN counts so
		// aggregate code paths don't have to re-derive it from
		// treatment-side scoring output.
		Clean: c.Clean || len(c.Expected) == 0,
	}

	// Make a mutable copy so we can mark items as consumed without
	// disturbing the caller's slice.
	pool := make([]*ProducedFinding, len(produced))
	for i := range produced {
		pool[i] = &produced[i]
	}

	for _, ef := range c.Expected {
		match, idx := bestMatch(ef, pool)
		if match == nil {
			cs.FalseNegatives++
			cs.Missed = append(cs.Missed, ef)
			continue
		}
		cs.TruePositives++
		cs.Matched = append(cs.Matched, MatchPair{Expected: ef, Produced: *match})
		pool[idx] = nil
	}

	for _, p := range pool {
		if p == nil {
			continue
		}
		cs.FalsePositives++
		cs.Extra = append(cs.Extra, *p)
	}

	return cs
}

// bestMatch finds the closest unconsumed produced finding for ef and
// returns it along with its index in pool, or (nil, -1) if no produced
// finding is within the line window of ef in the same file.
//
// "Closest" = smallest |ef.Line - p.Line|. Tie-breaks by index so the
// result is deterministic.
func bestMatch(ef ExpectedFinding, pool []*ProducedFinding) (*ProducedFinding, int) {
	window := ef.Window
	if window <= 0 {
		window = defaultLineWindow
	}

	bestDelta := window + 1
	bestIdx := -1
	for i, p := range pool {
		if p == nil {
			continue
		}
		if !filesMatch(ef.File, p.File) {
			continue
		}
		// Line 0 in expected = "anywhere in the file" (useful for
		// file-level findings that don't have a clean line locus).
		if ef.Line == 0 {
			return p, i
		}
		delta := abs(ef.Line - p.Line)
		if delta > window {
			continue
		}
		if delta < bestDelta {
			bestDelta = delta
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return nil, -1
	}
	return pool[bestIdx], bestIdx
}

// filesMatch implements the "loose path match" rule. Either side may
// be a partial path — LLMs commonly emit just the basename
// ("foo.go:42") for unambiguous diffs, while expected labels carry
// the full repo-relative path. We accept a match when one is a suffix
// of the other (split on "/" so "bar.go" doesn't match "foobar.go").
func filesMatch(a, b string) bool {
	if a == b {
		return true
	}
	return hasPathSuffix(a, b) || hasPathSuffix(b, a)
}

func hasPathSuffix(full, suffix string) bool {
	if !strings.HasSuffix(full, suffix) {
		return false
	}
	if len(full) == len(suffix) {
		return true
	}
	// Require the boundary to fall on a path separator so "foobar.go"
	// doesn't qualify as ending with "bar.go".
	return full[len(full)-len(suffix)-1] == '/'
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
