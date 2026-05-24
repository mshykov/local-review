package audit

import (
	"bytes"
	"encoding/json"
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
