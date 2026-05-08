package bench

import "testing"

func TestParseFindings_HappyPath(t *testing.T) {
	md := `## Major Issues

- ` + "`src/foo.go:42`" + ` — nil deref after type assert
  Suggestion: add a guard.

## Warnings

- src/bar.go:17 — magic number
- src/baz.py line 3 — unused import

## Info / Notes

- ` + "`README.md:1`" + ` — typo
`
	got := ParseFindings(md)

	want := []ProducedFinding{
		{File: "src/foo.go", Line: 42, Severity: "major"},
		{File: "src/bar.go", Line: 17, Severity: "warning"},
		{File: "src/baz.py", Line: 3, Severity: "warning"},
		{File: "README.md", Line: 1, Severity: "info"},
	}
	if len(got) != len(want) {
		t.Fatalf("findings count: got %d want %d (got=%+v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].File != w.File || got[i].Line != w.Line || got[i].Severity != w.Severity {
			t.Errorf("finding %d: got %+v want %+v", i, got[i], w)
		}
	}
}

func TestParseFindings_DedupesWithinSeverity(t *testing.T) {
	md := `## Major Issues

- src/foo.go:42 — bad code
- src/foo.go:42 — same finding restated in detail
`
	got := ParseFindings(md)
	if len(got) != 1 {
		t.Fatalf("expected dedupe to 1 finding, got %d: %+v", len(got), got)
	}
}

func TestParseFindings_KeepsAcrossSeverities(t *testing.T) {
	// Same path:line under two different severity headings is two
	// findings — the LLM is making distinct claims, even if they
	// share a location.
	md := `## Critical Issues

- src/foo.go:42 — sql injection

## Warnings

- src/foo.go:42 — unrelated style issue at the same line
`
	got := ParseFindings(md)
	if len(got) != 2 {
		t.Fatalf("expected 2 findings across severities, got %d: %+v", len(got), got)
	}
	if got[0].Severity != "critical" || got[1].Severity != "warning" {
		t.Errorf("severities: got %q,%q want critical,warning", got[0].Severity, got[1].Severity)
	}
}

func TestParseFindings_IgnoresUnrelatedColons(t *testing.T) {
	// "version: 0.42" and SHA-like "abc123:def456" should not be
	// parsed as findings — both have no file extension so the regex
	// rejects them.
	md := `## Major Issues

The codex version: 0.42 returned exit 1 on commit abc123:def456.
But src/foo.go:9 has the actual problem.
`
	got := ParseFindings(md)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].File != "src/foo.go" || got[0].Line != 9 {
		t.Errorf("got %+v want src/foo.go:9", got[0])
	}
}

func TestParseFindings_EmptyInput(t *testing.T) {
	if got := ParseFindings(""); got != nil {
		t.Errorf("empty input should yield nil, got %+v", got)
	}
	if got := ParseFindings("   \n\n\t\n"); got != nil {
		t.Errorf("whitespace-only input should yield nil, got %+v", got)
	}
}

func TestParseFindings_GitHubLAnchor(t *testing.T) {
	got := ParseFindings("## Major Issues\n\n- src/foo.go:L42 — bug\n")
	if len(got) != 1 || got[0].File != "src/foo.go" || got[0].Line != 42 {
		t.Errorf("L-anchor not parsed: got %+v", got)
	}
}

func TestParseFindings_ExtensionlessFilenames(t *testing.T) {
	// Dockerfile / Makefile / etc. don't have file extensions but
	// reviewers absolutely flag findings on them. The earlier regex
	// only accepted `path.ext:LINE` shapes — codex flagged this in
	// self-review on the v0.7-bench commit.
	cases := []struct {
		md   string
		path string
		line int
	}{
		{"## Major Issues\n\n- Dockerfile:12 — running as root\n", "Dockerfile", 12},
		{"## Major Issues\n\n- Makefile:8 — recipe lacks .PHONY\n", "Makefile", 8},
		{"## Warnings\n\n- ops/Dockerfile:3 — old base image\n", "ops/Dockerfile", 3},
		{"## Major Issues\n\n- `Jenkinsfile:42` — credential leak\n", "Jenkinsfile", 42},
	}
	for _, tc := range cases {
		got := ParseFindings(tc.md)
		if len(got) != 1 {
			t.Errorf("md=%q → got %d findings, want 1: %+v", tc.md, len(got), got)
			continue
		}
		if got[0].File != tc.path || got[0].Line != tc.line {
			t.Errorf("md=%q → got %s:%d, want %s:%d", tc.md, got[0].File, got[0].Line, tc.path, tc.line)
		}
	}
}

func TestParseFindings_ExtensionlessRequiresWordBoundary(t *testing.T) {
	// "MyDockerfile:42" should NOT match the extensionless filename
	// alternation — that's a sub-string of a longer custom name.
	got := ParseFindings("## Major Issues\n\n- MyDockerfile:42 — fake\n")
	if len(got) != 0 {
		t.Errorf("MyDockerfile:42 should not match extensionless rule, got %+v", got)
	}
}
