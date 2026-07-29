package multi

import (
	"strings"
	"testing"
)

// dogfoodReport is the shape of a real 2026-07 malformed report: the
// summary claims 5 findings, but exactly one finding is rendered — under
// BOTH "Major Issues" and "Info / Notes". Both defects must be caught.
const dogfoodReport = "# Code Review — Single-LLM Report\n" + `
## Summary
- **Reviewer**: ollama (single source — no cross-model consensus)
- **Total findings**: 5
- **Recommendation**: REQUEST CHANGES

## Critical Issues
*(Block merge — will break production, lose data, or create security holes)*

No critical issues found.

## Major Issues
*(Should fix before merge — likely bugs, perf issues, or security concerns)*

- **` + "`CHANGELOG.md:26`" + `** — Dev Solidgate payments could never reach the sandbox channel.

  Explanation body.

  **Fix**: Some fix.

## Warnings
*(Design/maintainability problems worth addressing)*

No warnings found.

## Info / Notes
*(Context the author may want to know — not blocking)*

- **` + "`CHANGELOG.md:26`" + `** — Dev Solidgate payments could never reach the sandbox channel.

  Explanation body.
`

func TestValidateReport_CatchesCountMismatchAndCrossSectionDuplicate(t *testing.T) {
	problems := ValidateReport(dogfoodReport)
	if len(problems) != 2 {
		t.Fatalf("expected 2 problems (count mismatch + duplicate), got %d: %v", len(problems), problems)
	}
	joined := strings.Join(problems, "\n")
	// "renders 2": the duplicated finding is TWO rendered bullets, which
	// is exactly what a reader counts on screen — the fact that they are
	// the same finding is the separate cross-section warning below.
	if !strings.Contains(joined, "claims 5 finding(s) but renders 2") {
		t.Errorf("count-mismatch warning missing or wrong: %v", problems)
	}
	if !strings.Contains(joined, "CHANGELOG.md:26") ||
		!strings.Contains(joined, "Info / Notes") ||
		!strings.Contains(joined, "Major Issues") {
		t.Errorf("cross-section duplicate warning must name the finding and both sections: %v", problems)
	}
}

// TestValidateReport_AcceptsWellFormedReport is the false-positive
// guard: a correct report (accurate count, each finding in exactly one
// section) must produce ZERO warnings, or the channel becomes noise
// users learn to ignore.
func TestValidateReport_AcceptsWellFormedReport(t *testing.T) {
	good := "# Code Review — Consolidated Report\n" + `
## Summary
- **Reviewer**: claude, codex
- **Total findings**: 3
- **Recommendation**: REQUEST CHANGES

## Critical Issues
*(Block merge)*

- **` + "`api/pay.go:12`" + `** — Secret logged in plaintext.

  Body.

## Major Issues
*(Should fix before merge)*

- **` + "`api/pay.go:88`" + `** — Missing error check.

  Body.

## Warnings
*(Design)*

No warnings found.

## Info / Notes
*(Context)*

- **` + "`README.md:4`" + `** — Stale docs link.

  Body.
`
	if problems := ValidateReport(good); len(problems) != 0 {
		t.Errorf("well-formed report must produce no warnings, got: %v", problems)
	}
}

// TestValidateReport_CleanReportAndEdgeCases covers the shapes that must
// stay silent: a zero-finding report, a report with no count claim at
// all, and empty input.
func TestValidateReport_CleanReportAndEdgeCases(t *testing.T) {
	clean := `## Summary
- **Total findings**: 0
- **Recommendation**: APPROVE

## Critical Issues
No critical issues found.

## Major Issues
No major issues found.
`
	if problems := ValidateReport(clean); len(problems) != 0 {
		t.Errorf("clean 0-finding report must be silent, got: %v", problems)
	}

	noClaim := "## Major Issues\n\n- **`a.go:1`** — Something.\n"
	if problems := ValidateReport(noClaim); len(problems) != 0 {
		t.Errorf("report without a Total-findings claim has nothing to contradict, got: %v", problems)
	}

	for _, empty := range []string{"", "   \n\t "} {
		if problems := ValidateReport(empty); problems != nil {
			t.Errorf("empty report must yield nil, got: %v", problems)
		}
	}
}

// TestValidateReport_SameFindingTwiceInOneSectionIsNotCrossSection
// pins the boundary: repeating a finding within ONE section is a count
// problem (both bullets are rendered and counted), not a severity
// contradiction — the duplicate check must not fire on it.
func TestValidateReport_SameFindingTwiceInOneSectionIsNotCrossSection(t *testing.T) {
	rep := "## Summary\n- **Total findings**: 2\n\n## Major Issues\n\n- **`a.go:1`** — X.\n\n- **`a.go:1`** — X again.\n"
	problems := ValidateReport(rep)
	for _, p := range problems {
		if strings.Contains(p, "multiple severity sections") {
			t.Errorf("intra-section repetition must not be reported as a cross-section duplicate: %v", problems)
		}
	}
}
