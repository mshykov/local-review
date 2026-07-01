package bench

import "fmt"

// jaccard returns |∩| / |∪| of finding-location sets across runs.
// Each run is reduced to a set of "<file>:<line>" keys; severity and
// snippet are deliberately ignored because LLMs paraphrase the same
// finding's title and severity tier across runs even when they're
// pointing at the same underlying issue.
//
// Special cases:
//   - Fewer than 2 runs: returns 0 (caller should not have called).
//   - All runs are empty: returns 1.0 (perfect agreement on "nothing
//     to flag" — matters most for clean cases, where stable silence
//     is the desired behaviour).
//
// The metric is computed across ALL N runs at once, not pairwise.
// |∩| is the count of (file:line) pairs present in every run; |∪| is
// the count of distinct (file:line) pairs present in any run. This
// is stricter than mean-pairwise-Jaccard and is what users actually
// mean by "did the reviewer say the same thing every time?".
func jaccard(runs [][]ProducedFinding) float64 {
	if len(runs) < 2 {
		return 0
	}

	sets, allEmpty := findingSets(runs)
	if allEmpty {
		return 1
	}

	// Union: every key seen in any run.
	union := make(map[string]struct{})
	for _, s := range sets {
		for k := range s {
			union[k] = struct{}{}
		}
	}

	if len(union) == 0 {
		return 1
	}
	return float64(countIntersection(sets)) / float64(len(union))
}

// findingSets reduces each run to its set of "<file>:<line>" keys.
// allEmpty is true only when every run's set is empty.
func findingSets(runs [][]ProducedFinding) (sets []map[string]struct{}, allEmpty bool) {
	sets = make([]map[string]struct{}, len(runs))
	allEmpty = true
	for i, r := range runs {
		s := make(map[string]struct{}, len(r))
		for _, f := range r {
			s[findingKey(f)] = struct{}{}
		}
		sets[i] = s
		if len(s) > 0 {
			allEmpty = false
		}
	}
	return sets, allEmpty
}

// countIntersection counts keys present in every one of sets[0:].
func countIntersection(sets []map[string]struct{}) int {
	intersection := 0
	for k := range sets[0] {
		inAll := true
		for _, s := range sets[1:] {
			if _, ok := s[k]; !ok {
				inAll = false
				break
			}
		}
		if inAll {
			intersection++
		}
	}
	return intersection
}

// findingKey returns the dedupe key Jaccard works on. file:line is
// the smallest unit that survives LLM paraphrasing — title, body, and
// severity routinely vary across runs even when the underlying claim
// is the same.
func findingKey(f ProducedFinding) string {
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}
