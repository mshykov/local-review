package bench

import "testing"

func TestScore_PerfectMatch(t *testing.T) {
	c := Case{
		ID: "t",
		Expected: []ExpectedFinding{
			{File: "foo.go", Line: 10, Window: 2},
			{File: "bar.go", Line: 30, Window: 2},
		},
	}
	produced := []ProducedFinding{
		{File: "foo.go", Line: 11, Severity: "major"}, // within window
		{File: "bar.go", Line: 30, Severity: "warning"},
	}

	cs := Score(c, produced)
	if cs.TruePositives != 2 || cs.FalsePositives != 0 || cs.FalseNegatives != 0 {
		t.Errorf("got TP=%d FP=%d FN=%d, want 2/0/0", cs.TruePositives, cs.FalsePositives, cs.FalseNegatives)
	}
	if got := cs.Precision(); got != 1.0 {
		t.Errorf("precision=%v want 1.0", got)
	}
	if got := cs.Recall(); got != 1.0 {
		t.Errorf("recall=%v want 1.0", got)
	}
	if got := cs.F1(); got != 1.0 {
		t.Errorf("f1=%v want 1.0", got)
	}
}

func TestScore_MissedAndExtra(t *testing.T) {
	c := Case{
		ID: "t",
		Expected: []ExpectedFinding{
			{File: "foo.go", Line: 10},
			{File: "bar.go", Line: 30, Note: "should catch this"},
		},
	}
	produced := []ProducedFinding{
		{File: "foo.go", Line: 10},                 // matches
		{File: "baz.go", Line: 5, Severity: "nit"}, // extra
	}

	cs := Score(c, produced)
	if cs.TruePositives != 1 || cs.FalsePositives != 1 || cs.FalseNegatives != 1 {
		t.Errorf("got TP=%d FP=%d FN=%d, want 1/1/1", cs.TruePositives, cs.FalsePositives, cs.FalseNegatives)
	}
	if len(cs.Missed) != 1 || cs.Missed[0].File != "bar.go" {
		t.Errorf("missed: got %+v want bar.go", cs.Missed)
	}
	if len(cs.Extra) != 1 || cs.Extra[0].File != "baz.go" {
		t.Errorf("extra: got %+v want baz.go", cs.Extra)
	}
}

func TestScore_OneProducedDoesntCoverMultipleExpected(t *testing.T) {
	// Two distinct expected findings on adjacent lines — one produced
	// finding shouldn't satisfy both.
	c := Case{
		ID: "t",
		Expected: []ExpectedFinding{
			{File: "foo.go", Line: 10, Window: 5},
			{File: "foo.go", Line: 11, Window: 5},
		},
	}
	produced := []ProducedFinding{
		{File: "foo.go", Line: 10},
	}

	cs := Score(c, produced)
	if cs.TruePositives != 1 || cs.FalseNegatives != 1 {
		t.Errorf("got TP=%d FN=%d, want 1/1 (one expected uncovered)", cs.TruePositives, cs.FalseNegatives)
	}
}

func TestScore_PathSuffixMatch(t *testing.T) {
	c := Case{
		ID:       "t",
		Expected: []ExpectedFinding{{File: "src/auth/login.go", Line: 50}},
	}
	// LLM emits just the basename — common when there's only one file
	// in the diff. Suffix match should still hit.
	produced := []ProducedFinding{{File: "login.go", Line: 50}}

	cs := Score(c, produced)
	if cs.TruePositives != 1 {
		t.Errorf("path suffix didn't match: TP=%d (matched=%+v)", cs.TruePositives, cs.Matched)
	}
}

func TestScore_PathSuffixRequiresBoundary(t *testing.T) {
	// "bar.go" should NOT match "foobar.go" — suffix has to land on a
	// path separator.
	c := Case{
		ID:       "t",
		Expected: []ExpectedFinding{{File: "bar.go", Line: 1}},
	}
	produced := []ProducedFinding{{File: "foobar.go", Line: 1}}

	cs := Score(c, produced)
	if cs.TruePositives != 0 {
		t.Errorf("foobar.go should not match bar.go via suffix; got TP=%d", cs.TruePositives)
	}
}

func TestScore_LineWindowRespected(t *testing.T) {
	c := Case{
		ID:       "t",
		Expected: []ExpectedFinding{{File: "foo.go", Line: 100, Window: 1}},
	}
	produced := []ProducedFinding{{File: "foo.go", Line: 105}}

	cs := Score(c, produced)
	if cs.TruePositives != 0 {
		t.Errorf("line outside window matched anyway: TP=%d", cs.TruePositives)
	}
}

func TestScore_ZeroLineMatchesAnywhereInFile(t *testing.T) {
	// expected.line=0 means "file-level finding, line doesn't matter"
	// — useful for "this whole file should be deleted" or "missing
	// import" findings.
	c := Case{
		ID:       "t",
		Expected: []ExpectedFinding{{File: "foo.go", Line: 0}},
	}
	produced := []ProducedFinding{{File: "foo.go", Line: 9999}}

	cs := Score(c, produced)
	if cs.TruePositives != 1 {
		t.Errorf("line-0 expected didn't match anywhere-in-file: TP=%d", cs.TruePositives)
	}
}

func TestScore_CleanCaseAllProducedAreFalsePositives(t *testing.T) {
	c := Case{ID: "clean", Clean: true}
	produced := []ProducedFinding{
		{File: "foo.go", Line: 1},
		{File: "foo.go", Line: 2},
	}

	cs := Score(c, produced)
	if cs.TruePositives != 0 || cs.FalseNegatives != 0 {
		t.Errorf("clean case got TP=%d FN=%d, want 0/0", cs.TruePositives, cs.FalseNegatives)
	}
	if cs.FalsePositives != 2 {
		t.Errorf("clean case FP: got %d want 2", cs.FalsePositives)
	}
	// Recall is 1.0 by definition for clean cases — see CaseScore.Recall.
	if cs.Recall() != 1.0 {
		t.Errorf("clean recall: got %v want 1.0", cs.Recall())
	}
}
