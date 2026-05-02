package review

import "testing"

func TestParseFindings(t *testing.T) {
	cases := map[string]int{
		`{"findings":[]}`: 0,
		`{"findings":[{"file":"a.go","line":1,"severity":"major","title":"x","body":"y"}]}`: 1,
		"```json\n" + `{"findings":[{"file":"a","severity":"warning","title":"t","body":"b"}]}` + "\n```": 1,
		`prose before {"findings":[{"file":"a","severity":"info","title":"t","body":"b"}]} prose after`: 1,
	}
	for input, want := range cases {
		got, err := parseFindings(input)
		if err != nil {
			t.Errorf("parseFindings error on %q: %v", input, err)
			continue
		}
		if len(got) != want {
			t.Errorf("parseFindings(%q) returned %d, want %d", input, len(got), want)
		}
	}
}

func TestApplyFilters_SeverityCutoff(t *testing.T) {
	in := []Finding{
		{File: "a", Severity: SeverityNit, Title: "n"},
		{File: "b", Severity: SeverityWarning, Title: "w"},
		{File: "c", Severity: SeverityMajor, Title: "m"},
	}
	out := applyFilters(in, SeverityWarning, 0)
	if len(out) != 2 {
		t.Errorf("got %d findings, want 2", len(out))
	}
	// Sort: major first
	if out[0].Severity != SeverityMajor {
		t.Errorf("first finding sev = %v, want major", out[0].Severity)
	}
}

func TestApplyFilters_MaxCap(t *testing.T) {
	in := []Finding{
		{Severity: SeverityMajor, File: "a"},
		{Severity: SeverityMajor, File: "b"},
		{Severity: SeverityMajor, File: "c"},
	}
	out := applyFilters(in, SeverityWarning, 2)
	if len(out) != 2 {
		t.Errorf("len = %d, want 2 (capped)", len(out))
	}
}

func TestMatchesAny(t *testing.T) {
	if !matchesAny("dist/foo.js", []string{"**/dist/**"}) {
		t.Error("expected **/dist/** to match dist/foo.js")
	}
	if !matchesAny("dist/foo.js", []string{"dist/**"}) {
		t.Error("expected dist/** to match dist/foo.js")
	}
	if !matchesAny("foo.lock", []string{"**/*.lock"}) {
		t.Error("expected **/*.lock to match foo.lock")
	}
	if matchesAny("src/foo.ts", []string{"**/*.lock"}) {
		t.Error("did not expect **/*.lock to match src/foo.ts")
	}
}
