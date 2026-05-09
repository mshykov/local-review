package bench

import "testing"

func TestJaccard_FullAgreement(t *testing.T) {
	// Two runs producing identical (file, line) sets → 1.0.
	a := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "bar.go", Line: 5}}
	b := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "bar.go", Line: 5}}
	if got := jaccard([][]ProducedFinding{a, b}); got != 1.0 {
		t.Errorf("identical runs: got %v, want 1.0", got)
	}
}

func TestJaccard_NoOverlap(t *testing.T) {
	a := []ProducedFinding{{File: "foo.go", Line: 10}}
	b := []ProducedFinding{{File: "bar.go", Line: 99}}
	if got := jaccard([][]ProducedFinding{a, b}); got != 0.0 {
		t.Errorf("disjoint runs: got %v, want 0.0", got)
	}
}

func TestJaccard_PartialOverlap(t *testing.T) {
	// |∩| = {foo.go:10}; |∪| = {foo.go:10, bar.go:5, baz.go:7}
	// Jaccard = 1/3 ≈ 0.333
	a := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "bar.go", Line: 5}}
	b := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "baz.go", Line: 7}}
	got := jaccard([][]ProducedFinding{a, b})
	if got < 0.33 || got > 0.34 {
		t.Errorf("partial overlap: got %v, want ≈0.333", got)
	}
}

func TestJaccard_AllEmpty(t *testing.T) {
	// Two runs that both produced no findings → perfect agreement on
	// "nothing to flag." Matters most for clean cases — stable
	// silence is the correct behaviour.
	got := jaccard([][]ProducedFinding{{}, {}})
	if got != 1.0 {
		t.Errorf("both empty: got %v, want 1.0", got)
	}
}

func TestJaccard_OneEmptyOneNonEmpty(t *testing.T) {
	// One run silent, the other flagged something → no agreement.
	a := []ProducedFinding{}
	b := []ProducedFinding{{File: "foo.go", Line: 10}}
	if got := jaccard([][]ProducedFinding{a, b}); got != 0 {
		t.Errorf("one empty: got %v, want 0", got)
	}
}

func TestJaccard_FewerThanTwoRuns(t *testing.T) {
	// jaccard's contract is "len(runs) ≥ 2"; fewer returns 0.
	if got := jaccard([][]ProducedFinding{{{File: "x", Line: 1}}}); got != 0 {
		t.Errorf("len=1: got %v, want 0", got)
	}
	if got := jaccard(nil); got != 0 {
		t.Errorf("nil: got %v, want 0", got)
	}
}

func TestJaccard_IgnoresSeverityAndSnippet(t *testing.T) {
	// Same file:line, different severity and snippet — these come out
	// of two different runs that paraphrased the same finding. The
	// metric should treat them as identical.
	a := []ProducedFinding{{File: "foo.go", Line: 10, Severity: "major", Snippet: "first phrasing"}}
	b := []ProducedFinding{{File: "foo.go", Line: 10, Severity: "warning", Snippet: "different phrasing"}}
	if got := jaccard([][]ProducedFinding{a, b}); got != 1.0 {
		t.Errorf("paraphrased: got %v, want 1.0 (severity/snippet shouldn't matter)", got)
	}
}

func TestJaccard_StrictAcrossAllRuns(t *testing.T) {
	// 3 runs, intersection requires presence in all of them.
	// {foo:10} is in all three → counts. {bar:5} only in two → doesn't.
	a := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "bar.go", Line: 5}}
	b := []ProducedFinding{{File: "foo.go", Line: 10}, {File: "bar.go", Line: 5}}
	c := []ProducedFinding{{File: "foo.go", Line: 10}}
	// |∩| = 1, |∪| = 2 → 0.5
	got := jaccard([][]ProducedFinding{a, b, c})
	if got != 0.5 {
		t.Errorf("3-way: got %v, want 0.5", got)
	}
}
