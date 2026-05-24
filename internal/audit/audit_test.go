package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseFindings_HeaderRowsAndBodies covers the audit-pack-
// mandated finding shape: `[severity] path:line` header followed
// by body lines until the next header or EOF.
func TestParseFindings_HeaderRowsAndBodies(t *testing.T) {
	out := `[critical] internal/cli/parsers.go:42
SQL built by string concatenation in search query.
Why: turns user input into executable SQL — textbook SQLi.
Suggest: parameterize the query.

[warning] internal/cli/parsers.go:120
Generic Exception catch swallows real errors.
Why: hides programmer bugs behind the retry loop.
Suggest: catch specific error types only.
`
	findings := parseFindings(out)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings; got %d: %+v", len(findings), findings)
	}
	if findings[0].Severity != "critical" || findings[0].Path != "internal/cli/parsers.go" || findings[0].Line != 42 {
		t.Errorf("first finding shape wrong: %+v", findings[0])
	}
	if !strings.Contains(findings[0].Body, "SQL built by string concatenation") {
		t.Errorf("first finding body missing the lead line: %q", findings[0].Body)
	}
	if findings[1].Severity != "warning" || findings[1].Line != 120 {
		t.Errorf("second finding shape wrong: %+v", findings[1])
	}
}

// TestParseFindings_LineRangeCaptured covers the v0.10.0-c PR-
// review fix: the header regex accepts `:LINE-LINE` ranges in
// addition to plain `:LINE`. Audit packs document the range
// shape; without the regex update, `file.go:12-18` parsed as
// Line=12 and silently dropped the end-line.
func TestParseFindings_LineRangeCaptured(t *testing.T) {
	out := `[warning] foo.go:12-18
duplicated try/except chain across function body
Why: divergence in 3 of the 6 copies; bug fixes only land in one.
Suggest: extract a shared helper.
`
	findings := parseFindings(out)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].Line != 12 || findings[0].LineEnd != 18 {
		t.Errorf("expected Line=12 LineEnd=18; got Line=%d LineEnd=%d", findings[0].Line, findings[0].LineEnd)
	}
}

// TestFormatLocation_HandlesSingleLineAndRange covers the
// renderer-side rendering of Finding.Line / Finding.LineEnd.
// Single-line stays "path:line"; range becomes "path:start-end";
// elided line drops the suffix entirely.
func TestFormatLocation_HandlesSingleLineAndRange(t *testing.T) {
	cases := []struct {
		f    Finding
		want string
	}{
		{Finding{Path: "foo.go", Line: 12}, "foo.go:12"},
		{Finding{Path: "foo.go", Line: 12, LineEnd: 18}, "foo.go:12-18"},
		{Finding{Path: "foo.go"}, "foo.go"},
		// LineEnd == Line collapses to single-line shape (LLM
		// occasionally emits redundant range like "12-12").
		{Finding{Path: "foo.go", Line: 12, LineEnd: 12}, "foo.go:12"},
	}
	for _, tc := range cases {
		if got := formatLocation(tc.f); got != tc.want {
			t.Errorf("formatLocation(%+v) = %q, want %q", tc.f, got, tc.want)
		}
	}
}

// TestIsCleanSentinel_RecognizesPackPhrasing covers the two audit
// packs' clean sentinels plus a tolerance test for surrounding
// whitespace / commentary the LLM might add.
func TestIsCleanSentinel_RecognizesPackPhrasing(t *testing.T) {
	for _, body := range []string{
		"[clean] no security findings in this package",
		"[clean] no tech-debt findings in this package",
		"  [clean] no security findings in this package  ",
		"After review:\n[clean] no security findings in this package\n",
	} {
		if !isCleanSentinel(body) {
			t.Errorf("expected clean sentinel for %q", body)
		}
	}
	// Negative cases: the LLM produces findings but says "clean"
	// somewhere — the sentinel must require the exact shape.
	for _, body := range []string{
		"[warning] foo.go:1\nlooks clean otherwise",
		"the code is clean",
		"no findings",
	} {
		if isCleanSentinel(body) {
			t.Errorf("unexpected clean match for %q", body)
		}
	}
}

// TestFillAggregates_CountsAcrossPackagesAndStatuses verifies the
// summary counters: severities, package status (with-findings /
// clean / errored), and token totals.
func TestFillAggregates_CountsAcrossPackagesAndStatuses(t *testing.T) {
	rep := Report{
		FindingsBySeverity: map[string]int{},
		Packages: []PackageReport{
			{
				Package: "internal/cli",
				Findings: []Finding{
					{Severity: "critical"},
					{Severity: "warning"},
				},
				InputTokens:  1000,
				OutputTokens: 200,
			},
			{Package: "internal/git", Clean: true, InputTokens: 500, OutputTokens: 50},
			{Package: "internal/multi", Error: "timeout", InputTokens: 100, OutputTokens: 0},
		},
	}
	fillAggregates(&rep)
	if rep.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, want 2", rep.TotalFindings)
	}
	if rep.FindingsBySeverity["critical"] != 1 || rep.FindingsBySeverity["warning"] != 1 {
		t.Errorf("FindingsBySeverity wrong: %v", rep.FindingsBySeverity)
	}
	if rep.PackagesWithFindings != 1 || rep.PackagesClean != 1 || rep.PackagesErrored != 1 {
		t.Errorf("package status counts wrong: %+v", rep)
	}
	if rep.TotalInputTokens != 1600 || rep.TotalOutputTokens != 250 {
		t.Errorf("token totals wrong: in=%d out=%d", rep.TotalInputTokens, rep.TotalOutputTokens)
	}
}

// TestWalker_PathPassesFilters covers the include / exclude
// prefix logic. Include defaults to "match anything" when empty;
// exclude always filters.
func TestWalker_PathPassesFilters(t *testing.T) {
	cases := []struct {
		path    string
		include []string
		exclude []string
		want    bool
	}{
		{"internal/cli/foo.go", nil, nil, true},
		{"internal/cli/foo.go", []string{"internal/cli"}, nil, true},
		{"internal/cli/foo.go", []string{"internal/multi"}, nil, false},
		{"internal/cli/foo.go", nil, []string{"internal/cli"}, false},
		{"internal/cli/foo.go", []string{"internal"}, []string{"internal/cli"}, false},
		// Directory-boundary cases (regression for the v0.10.0-c
		// PR review fix): `internal/cli` must NOT match
		// `internal/cli2/foo.go`. Raw HasPrefix used to do this
		// wrong and pull unintended files into the filter.
		{"internal/cli2/foo.go", []string{"internal/cli"}, nil, false},
		{"internal/cli2/foo.go", nil, []string{"internal/cli"}, true},
		// Exact-match (no trailing slash, file at the directory's
		// own path) and trailing-slash tolerance.
		{"internal/cli", []string{"internal/cli"}, nil, true},
		{"internal/cli/foo.go", []string{"internal/cli/"}, nil, true},
	}
	for _, tc := range cases {
		if got := pathPassesFilters(tc.path, tc.include, tc.exclude); got != tc.want {
			t.Errorf("filter(%q, inc=%v, exc=%v) = %v, want %v", tc.path, tc.include, tc.exclude, got, tc.want)
		}
	}
}

// TestWalker_IsAuditable verifies the extension allowlist: known
// languages route via lang.Detect, the built-in extras catch
// common config / shell shapes, and lockfiles are explicitly
// skipped.
func TestWalker_IsAuditable(t *testing.T) {
	for _, path := range []string{
		"main.go", "util.py", "build.gradle.kts",
		"scripts/install.sh",
		".github/workflows/ci.yml",
		"sql/migrations/001.sql",
	} {
		if !isAuditable(path) {
			t.Errorf("expected auditable: %s", path)
		}
	}
	for _, path := range []string{
		"go.sum",
		"package-lock.json",
		"vendor/dep/Cargo.lock",
		"image.png",
		"archive.tar.gz",
	} {
		if isAuditable(path) {
			t.Errorf("expected NOT auditable: %s", path)
		}
	}
}

// TestWriteJSON_RoundTrip ensures the Report serialises and back
// without losing the headline fields. Catches accidental json-tag
// drift on schema changes.
func TestWriteJSON_RoundTrip(t *testing.T) {
	rep := Report{
		Topic:                "security",
		Generated:            time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		LLM:                  "claude",
		TotalFindings:        3,
		PackagesWithFindings: 1,
		FindingsBySeverity:   map[string]int{"critical": 1, "warning": 2},
		Packages: []PackageReport{
			{
				Package:  "internal/cli",
				Files:    []string{"internal/cli/parsers.go"},
				Findings: []Finding{{Severity: "critical", Path: "internal/cli/parsers.go", Line: 42, Body: "x"}},
			},
		},
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, rep); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Topic != "security" || back.TotalFindings != 3 || back.FindingsBySeverity["critical"] != 1 {
		t.Errorf("round-trip lost data: %+v", back)
	}
}

// TestWriteMarkdown_StructureForMixedPackageStatuses verifies the
// markdown shape: summary table, per-package sections for
// findings-bearing packages, a collapsed "Clean packages" line,
// and an "Errored packages" tail. Anchored on substrings so
// future formatting tweaks don't false-fail.
func TestWriteMarkdown_StructureForMixedPackageStatuses(t *testing.T) {
	rep := Report{
		Topic:              "security",
		Generated:          time.Now(),
		LLM:                "claude",
		FindingsBySeverity: map[string]int{"critical": 1, "warning": 1},
		TotalFindings:      2,
		Packages: []PackageReport{
			{
				Package: "internal/cli",
				Files:   []string{"a.go"},
				Findings: []Finding{
					{Severity: "critical", Path: "internal/cli/a.go", Line: 12, Body: "SQLi"},
					{Severity: "warning", Path: "internal/cli/a.go", Line: 40, Body: "broad catch"},
				},
			},
			{Package: "internal/git", Files: []string{"b.go"}, Clean: true},
			{Package: "internal/multi", Files: []string{"c.go"}, Error: "timeout"},
		},
	}
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, rep); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Audit — security",
		"## Summary",
		"| critical | 1 |",
		"| warning | 1 |",
		"## internal/cli",
		"**[critical]**", "**[warning]**",
		"## Clean packages",
		"internal/git",
		"## Errored packages",
		"timeout",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestSplitChunk_FitsInOnePackageWhenUnderCap verifies the
// no-split happy path: a package whose total body fits under
// maxBytes emits a single chunk with the package name unchanged.
// No "[part N/M]" suffix when the package wasn't split.
func TestSplitChunk_FitsInOnePackageWhenUnderCap(t *testing.T) {
	root := t.TempDir()
	mkFile(t, root, "a.go", "package a\n")
	mkFile(t, root, "b.go", "package a\n")
	chunks, err := splitChunk(root, ".", []string{"a.go", "b.go"}, 64*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk; got %d", len(chunks))
	}
	if chunks[0].Package != "." {
		t.Errorf("single chunk should keep package name; got %q", chunks[0].Package)
	}
	if !strings.Contains(chunks[0].Body, "// === FILE: a.go ===") {
		t.Errorf("body missing file marker for a.go: %q", chunks[0].Body)
	}
}

// TestSplitChunk_SplitsAcrossBinsWhenOverCap verifies the
// headline fix: a package whose body exceeds maxBytes is
// auto-split into multiple chunks labelled `pkg [part N/M]`, each
// at-or-below the cap, with file adjacency preserved (greedy
// bin-pack in input order).
//
// Reproduces the user-reported failure shape from PR #73 dogfood
// where 321 of 343 Android packages errored with prompt_too_long
// because the runner just shipped oversized chunks to Claude.
func TestSplitChunk_SplitsAcrossBinsWhenOverCap(t *testing.T) {
	root := t.TempDir()
	// Each file ~30 KiB; cap 50 KiB → expect 4 bins for 4 files
	// (one file per bin since two would exceed the cap).
	body := strings.Repeat("// filler\n", 3000) // ~30 KiB each
	mkFile(t, root, "a.go", body)
	mkFile(t, root, "b.go", body)
	mkFile(t, root, "c.go", body)
	mkFile(t, root, "d.go", body)
	chunks, err := splitChunk(root, "pkg", []string{"a.go", "b.go", "c.go", "d.go"}, 50*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for over-cap package; got %d", len(chunks))
	}
	// Each chunk must be labelled with the [part N/M] suffix.
	for _, c := range chunks {
		if !strings.Contains(c.Package, "pkg [part ") {
			t.Errorf("split chunk missing [part N/M] suffix: %q", c.Package)
		}
		// Each individual chunk should be at-or-under the cap.
		// (Single oversized files are the exception covered by
		// the other test; here all files are 30 KiB and the cap
		// is 50 KiB so all bins must fit.)
		if c.SizeBytes > 50*1024 {
			t.Errorf("split bin exceeded cap: %s (%d bytes)", c.Package, c.SizeBytes)
		}
	}
}

// TestSplitChunk_AdjacencyPreserved verifies the greedy-pack
// invariant: files end up bin-mates with their input neighbours,
// not interleaved. Important because the audit pack treats a
// chunk as a "coherent unit of scope" — interleaved files would
// degrade LLM reasoning about cross-file relationships within a
// package.
func TestSplitChunk_AdjacencyPreserved(t *testing.T) {
	root := t.TempDir()
	// 2 files of 30 KiB each → fit together in a 70 KiB cap.
	// A third 30 KiB file forces a new bin.
	body := strings.Repeat("// filler\n", 3000)
	mkFile(t, root, "a.go", body)
	mkFile(t, root, "b.go", body)
	mkFile(t, root, "c.go", body)
	chunks, err := splitChunk(root, "pkg", []string{"a.go", "b.go", "c.go"}, 70*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	// Expected packing: [a, b] [c]. Verify the first bin contains
	// a and b (in that order), the second contains c.
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for 3×30KiB files in 70KiB cap; got %d", len(chunks))
	}
	if len(chunks[0].Files) != 2 || chunks[0].Files[0] != "a.go" || chunks[0].Files[1] != "b.go" {
		t.Errorf("first chunk should hold [a.go, b.go] in order; got %v", chunks[0].Files)
	}
	if len(chunks[1].Files) != 1 || chunks[1].Files[0] != "c.go" {
		t.Errorf("second chunk should hold [c.go]; got %v", chunks[1].Files)
	}
}

// TestSplitChunk_OversizedSingleFileEmitsOneChunkAndWarns
// covers the no-split-mid-file invariant: a file individually
// over maxBytes goes through as a single chunk (over the cap)
// and surfaces a warning via the writer. The LLM will probably
// reject it with prompt_too_long but splitting a source file at
// an arbitrary line boundary would produce semantically broken
// chunks (split mid-function, no scope context) — worse than
// failing loudly. Caller can `--include` around the bad file.
func TestSplitChunk_OversizedSingleFileEmitsOneChunkAndWarns(t *testing.T) {
	root := t.TempDir()
	// 200 KiB file; cap 50 KiB.
	big := strings.Repeat("// huge\n", 25000)
	mkFile(t, root, "huge.go", big)
	mkFile(t, root, "small.go", "package x\n")
	var warn bytes.Buffer
	chunks, err := splitChunk(root, "pkg", []string{"huge.go", "small.go"}, 50*1024, &warn)
	if err != nil {
		t.Fatalf("splitChunk: %v", err)
	}
	// Expect: huge.go in its own bin (oversized), small.go in
	// another bin. So 2 chunks, both labelled with [part N/M].
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (oversized huge.go + sibling small.go); got %d", len(chunks))
	}
	// One of the chunks must contain just huge.go and exceed the
	// cap. Locate it by index first, then read fields off the
	// slice — avoids holding a pointer-to-slice-element across
	// the assertions (anti-idiomatic in Go even when safe here).
	hugeIdx := -1
	for i, c := range chunks {
		for _, f := range c.Files {
			if f == "huge.go" {
				hugeIdx = i
			}
		}
	}
	if hugeIdx == -1 {
		t.Fatal("no chunk contained huge.go")
	}
	huge := chunks[hugeIdx]
	if len(huge.Files) != 1 {
		t.Errorf("oversized file should be alone in its chunk; got %v", huge.Files)
	}
	if huge.SizeBytes <= 50*1024 {
		t.Errorf("oversized chunk should exceed cap by definition; got %d", huge.SizeBytes)
	}
	// Warning must mention the oversized file by name.
	if !strings.Contains(warn.String(), "huge.go") {
		t.Errorf("warning should name the oversized file; got %q", warn.String())
	}
	if !strings.Contains(warn.String(), "cannot split") {
		t.Errorf("warning should explain why we can't split; got %q", warn.String())
	}
}

// TestSplitChunk_NoWarningWhenWriterIsNil pins the contract that
// tests can pass nil for the warn writer without triggering a nil
// deref. The audit Run path passes os.Stderr, but library
// consumers may not want progress noise.
func TestSplitChunk_NoWarningWhenWriterIsNil(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("// huge\n", 25000)
	mkFile(t, root, "huge.go", big)
	// Should not panic.
	chunks, err := splitChunk(root, "pkg", []string{"huge.go"}, 50*1024, nil)
	if err != nil {
		t.Fatalf("splitChunk with nil warn: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk; got %d", len(chunks))
	}
}

// mkFile writes a file under root with the given relative path
// and contents. Used by the splitChunk tests to fabricate input
// trees of controlled size.
func mkFile(t *testing.T, root, name, body string) {
	t.Helper()
	full := filepath.Join(root, name)
	if dir := filepath.Dir(full); dir != root {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// Compile-time pin: the formatter helpers stay referenced even if
// a test refactor temporarily drops their last call site. Cheap
// insurance against `go test` failing on "imported and not used"
// during in-flight edits.
var _ = fmt.Sprintf
