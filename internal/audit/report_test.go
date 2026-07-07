package audit

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// reportFixture builds a Report exercising all three package states the
// renderers branch on: findings-bearing, clean, and errored. Shared by the
// text and markdown renderer tests so both formats are asserted against the
// same input shape.
func reportFixture() Report {
	return Report{
		Topic:     "security",
		Generated: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		LLM:       "claude",
		Packages: []PackageReport{
			{
				Package: "internal/config",
				Files:   []string{"internal/config/config.go", "internal/config/env.go"},
				Findings: []Finding{
					{Path: "internal/config/config.go", Line: 42, Severity: "critical", Body: "first line\nsecond line"},
					{Path: "internal/config/env.go", Line: 10, LineEnd: 14, Severity: "warning", Body: "ranged finding"},
				},
			},
			{
				Package: "internal/lang",
				Files:   []string{"internal/lang/detect.go"},
				Clean:   true,
			},
			{
				Package: "internal/llm",
				Files:   []string{"internal/llm/client.go"},
				Error:   "context deadline exceeded",
			},
		},
		TotalFindings:        2,
		FindingsBySeverity:   map[string]int{"critical": 1, "warning": 1},
		PackagesWithFindings: 1,
		PackagesClean:        1,
		PackagesErrored:      1,
	}
}

// TestWriteText_RendersAllPackageStates pins the terminal renderer's
// contract: headline counts, severity breakdown, and the three per-package
// shapes (findings inline with indented bodies, "✓ clean", "[ERR]").
func TestWriteText_RendersAllPackageStates(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, reportFixture()); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Audit: topic=security  llm=claude  packages=3  findings=2",
		"Severity breakdown: critical=1 warning=1",
		"Packages: 1 with findings, 1 clean, 1 errored",
		"── internal/config ── 2 files",
		"[critical] internal/config/config.go:42",
		"    first line",
		"    second line",
		"[warning] internal/config/env.go:10-14",
		"── internal/lang ── 1 file  ✓ clean",
		"── internal/llm ── 1 file  [ERR] context deadline exceeded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("WriteText output missing %q, got:\n%s", want, out)
		}
	}
}

// TestWriteText_NoFindingsRendersSeverityNone covers the empty-severity
// branch: a fully clean audit says "none" instead of an empty breakdown.
func TestWriteText_NoFindingsRendersSeverityNone(t *testing.T) {
	rep := Report{
		Topic: "tech-debt",
		LLM:   "codex",
		Packages: []PackageReport{
			{Package: "internal/lang", Files: []string{"internal/lang/detect.go"}, Clean: true},
		},
		PackagesClean: 1,
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, rep); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if !strings.Contains(buf.String(), "Severity breakdown: none") {
		t.Errorf("expected severity breakdown 'none' for a clean audit, got:\n%s", buf.String())
	}
}

// TestWriteMarkdown_OrdersFindingsThenCleanThenErrored pins the committable
// report's section order: findings-bearing packages render as their own
// sections first, clean packages collapse into one line, errored packages
// land at the bottom so failures stand out.
func TestWriteMarkdown_OrdersFindingsThenCleanThenErrored(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, reportFixture()); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"# Audit — security",
		"_LLM: claude_ · _Packages: 3_ · _Findings: 2_",
		"| critical | 1 |",
		"| major | 0 |",
		"## internal/config",
		"_2 files audited_",
		"- **[critical]** `internal/config/config.go:42`",
		"## Clean packages",
		"_1 package with no findings:_ internal/lang",
		"## Errored packages",
		"- **internal/llm** — context deadline exceeded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("WriteMarkdown output missing %q, got:\n%s", want, out)
		}
	}

	findingsIdx := strings.Index(out, "## internal/config")
	cleanIdx := strings.Index(out, "## Clean packages")
	erroredIdx := strings.Index(out, "## Errored packages")
	if !(findingsIdx < cleanIdx && cleanIdx < erroredIdx) {
		t.Errorf("expected findings < clean < errored section order, got indices %d, %d, %d:\n%s",
			findingsIdx, cleanIdx, erroredIdx, out)
	}
}
