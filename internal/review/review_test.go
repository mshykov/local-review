package review

import (
	"testing"

	"github.com/mshykov/local-review/internal/git"
)

func TestParseFindings(t *testing.T) {
	cases := map[string]int{
		`{"findings":[]}`: 0,
		`{"findings":[{"file":"a.go","line":1,"severity":"major","title":"x","body":"y"}]}`:               1,
		"```json\n" + `{"findings":[{"file":"a","severity":"warning","title":"t","body":"b"}]}` + "\n```": 1,
		`prose before {"findings":[{"file":"a","severity":"info","title":"t","body":"b"}]} prose after`:   1,
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

func TestParseFindings_PrefersLastTopLevelObject(t *testing.T) {
	// Multi-block LLM output: a non-conforming "example" object first,
	// the actual envelope second. The legacy first-{..last-} extractor
	// concatenated both into one substring and failed to unmarshal;
	// the brace-counter must skip past the example and parse the real
	// answer.
	raw := `Here is an example of the schema:
{"example": "ignore me", "shape": {"foo": "bar"}}

And here is my actual review:
{"findings":[{"file":"a.go","severity":"major","title":"t","body":"b"}]}`
	got, err := parseFindings(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Title != "t" {
		t.Errorf("got %+v, expected one finding titled 't'", got)
	}
}

func TestParseFindings_FallsBackToEarlierCandidates(t *testing.T) {
	// If the *last* object isn't our envelope shape (e.g., the LLM
	// trailed off into a "next steps" block), an earlier balanced
	// object that *is* our shape should still parse. Defends against
	// "post-answer chitchat" output drift.
	raw := `{"findings":[{"file":"a","severity":"warning","title":"x","body":"y"}]}

Note: I don't have access to the build system, so {"missing": "context"}.`
	got, err := parseFindings(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 finding, got %d", len(got))
	}
}

func TestTopLevelJSONObjects_HandlesBraceInString(t *testing.T) {
	// `}` inside a JSON string literal must not close the object.
	// Real LLM output regularly contains code snippets in `body`
	// fields, e.g. "if err != nil { return err }".
	in := `{"a": "if err != nil { return err }"}`
	got := topLevelJSONObjects(in)
	if len(got) != 1 || got[0] != in {
		t.Errorf("expected one object %q, got %v", in, got)
	}
}

func TestTopLevelJSONObjects_HandlesEscapedQuote(t *testing.T) {
	// Escaped quotes inside strings must not flip the in-string state
	// — otherwise `\"` would look like end-of-string and the next `}`
	// would prematurely close the object.
	in := `{"a": "she said \"hi\" } no really"}`
	got := topLevelJSONObjects(in)
	if len(got) != 1 || got[0] != in {
		t.Errorf("expected one object %q, got %v", in, got)
	}
}

func TestTopLevelJSONObjects_StrayCloseBraceIgnored(t *testing.T) {
	// A garbage `}` before the first `{` shouldn't cause a panic or
	// misalign the scanner.
	in := `} stray { "ok": true }`
	got := topLevelJSONObjects(in)
	if len(got) != 1 || got[0] != `{ "ok": true }` {
		t.Errorf("got %v", got)
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

func TestMatchesAny_PathSegmentBoundary(t *testing.T) {
	// Pre-fix `**/dist/**` emitted `.*dist/.*` which happily matched
	// src/mydist/file because `.*` doesn't enforce a path boundary.
	// New emission `(?:.*/)?dist/(?:.*)` requires `dist` at the start
	// of a path segment.
	if matchesAny("src/mydist/file.go", []string{"**/dist/**"}) {
		t.Error("**/dist/** must NOT match src/mydist/file.go (no path-segment boundary)")
	}
	if matchesAny("src/mydist/file.go", []string{"**/mydist/**"}) {
		// Sanity: the same path matches when the directory name is correct.
	} else {
		t.Error("**/mydist/** should match src/mydist/file.go")
	}
	// Cases that must still match: a directory named exactly `dist`.
	for _, path := range []string{"dist/file.go", "src/dist/file.go", "a/b/c/dist/file.go"} {
		if !matchesAny(path, []string{"**/dist/**"}) {
			t.Errorf("**/dist/** should match %q", path)
		}
	}
}

func TestMatchesAny_CharacterClass(t *testing.T) {
	// Bracket support: previously the matcher escaped `[` to a literal,
	// so a glob like `**/foo[0-9].go` became regex \[0-9\] and matched
	// nothing useful. Now bracket classes work like filepath.Match,
	// with [!...] negation translated to regex [^...].
	if !matchesAny("src/foo3.go", []string{"**/foo[0-9].go"}) {
		t.Error("**/foo[0-9].go should match src/foo3.go")
	}
	if matchesAny("src/fooA.go", []string{"**/foo[0-9].go"}) {
		t.Error("**/foo[0-9].go must NOT match src/fooA.go")
	}
	if !matchesAny("src/fooA.go", []string{"**/foo[!0-9].go"}) {
		t.Error("**/foo[!0-9].go (negated class) should match src/fooA.go")
	}
}

// Pin that compileGlobs is called O(globs) times, not O(globs * files).
// This is a performance contract: filter() must amortize regex
// compilation across the per-file loop, not pay it inside the loop.
func TestFilterCompilesGlobsOnce(t *testing.T) {
	// We can't directly observe regexp.Compile call counts without
	// instrumentation, but we can confirm the public surface still
	// produces correct output even when many files share the same
	// glob set — the slow O(N*M) path passed too, this just guards
	// the wiring stays through compileGlobs.
	diffs := make([]git.Diff, 100)
	for i := range diffs {
		diffs[i].Path = "src/file.go"
	}
	diffs[0].Path = "vendor/x.go"
	out := filter(diffs, nil, []string{"vendor/**"})
	if len(out) != 99 {
		t.Errorf("after excluding vendor/**: want 99 diffs, got %d", len(out))
	}
}
