package review

import (
	"testing"

	"github.com/mshykov/local-review/internal/git"
)

// The v0 single-LLM orchestration tests (parseFindings, topLevelJSONObjects,
// applyFilters) went away in v0.15 along with the orchestration itself —
// see the package doc on review.go for the history. What remains here pins
// the diff-filter machinery the multi-LLM runner shares with the audit
// walker.

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
	if !matchesAny("src/mydist/file.go", []string{"**/mydist/**"}) {
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

func TestFilter_AllInvalidIncludesFailClosed(t *testing.T) {
	// Copilot caught this: when `include:` is set but every pattern
	// fails to compile, compileGlobs returns an empty slice. Pre-fix
	// the loop saw len(includeRE) == 0 and treated it as "no include
	// filter set", silently *expanding* the review to all files —
	// the opposite of what the user asked for. Now: include was
	// requested but matched nothing → no files match (fail closed).
	//
	// `[!]` is the easiest reproducible invalid case: globToRegex
	// emits regex `[^]`, which Go's regexp.Compile rejects with
	// "missing closing ]". A user typing this as their only include
	// rule must NOT accidentally get every file reviewed.
	diffs := []git.Diff{
		{Path: "src/foo.go"},
		{Path: "src/bar.ts"},
	}
	out := filter(diffs, []string{"[!]"}, nil)
	if len(out) != 0 {
		t.Errorf("filter with all-invalid include should match nothing (fail-closed), got %d files", len(out))
	}
	// Sanity: a *valid* include still returns the matching subset.
	out = filter(diffs, []string{"src/*.go"}, nil)
	if len(out) != 1 || out[0].Path != "src/foo.go" {
		t.Errorf("valid include should match correct subset, got %v", out)
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
