package review

import "testing"

// realMergedReportWithMajorFinding is a real merged report (lightly
// trimmed) that a Major finding must trip the gate on. Pinned here so
// heuristic regressions can't silently let a real-world blocking review
// through.
const realMergedReportWithMajorFinding = `# Code Review — Consolidated Report

## Summary
- **Total unique findings**: 6
- **Recommendation**: REQUEST CHANGES

## Critical Issues

*None.*

## Major Issues

- ` + "`runner.go:198-219`" + ` — sectionHasContent is tightly coupled to bullet syntax

  The new implementation only counts a section as having content if it contains a Markdown list item.

  **Fix**: Be more permissive.

## Warnings

- ` + "`main.go:48-58`" + ` — Reintroduces golang.org/x/term

## Conclusion

The change has a major issue worth pushing on before merge.
`

func TestIsBlockingMarkdown_RealFixture(t *testing.T) {
	if !IsBlockingMarkdown(realMergedReportWithMajorFinding) {
		t.Error("real merged report with a Major finding must trigger the gate")
	}
}

func TestIsBlockingMarkdown(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want bool
	}{
		{
			name: "empty",
			md:   "",
			want: false,
		},
		{
			name: "no critical or major sections",
			md:   "# Code Review\n\n## Summary\n\n- 0 findings\n",
			want: false,
		},
		{
			name: "critical section with placeholder only",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "critical section with placeholder description only",
			md:   "## Critical Issues\n*(Block merge — will break production, lose data, or create security holes)*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "critical section with a real finding",
			md:   "## Critical Issues\n\n- **runner.go:42** — buffer overflow when input is very large\n  Fix: bounds-check before write.\n",
			want: true,
		},
		{
			name: "major section with a real finding (critical empty)",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n\n- **runner.go:42** — pre-commit gate broken\n",
			want: true,
		},
		{
			name: "warning-only finding does not block",
			md:   "## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n\n## Warnings\n\n- nit on naming\n",
			want: false,
		},
		{
			name: "prose finding (no list bullet) still blocks",
			md:   "## Critical Issues\nThe code path X has a race condition under load.\n\n## Major Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "numbered-list finding still blocks",
			md:   "## Critical Issues\n*(Block merge — ...)*\n\n1. file:42 — buffer overflow\n",
			want: true,
		},
		{
			name: "*None.* (italic, no parens) is treated as placeholder",
			md:   "## Critical Issues\n\n*None.*\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "bare 'None.' line is treated as placeholder",
			md:   "## Critical Issues\nNone.\n\n## Major Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "Recommendation: BLOCK MERGE blocks even with empty sections",
			md:   "## Summary\n- **Recommendation**: BLOCK MERGE\n\n## Critical Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "Recommendation: REQUEST CHANGES blocks too",
			md:   "## Summary\n**Recommendation**: REQUEST CHANGES\n\n## Critical Issues\n*(None)*\n",
			want: true,
		},
		{
			name: "Recommendation: APPROVE alone does not block",
			md:   "## Summary\n- **Recommendation**: APPROVE\n\n## Critical Issues\n*(None)*\n",
			want: false,
		},
		{
			name: "alternate heading 'Critical' (without 'Issues') with content blocks",
			md:   "## Critical\n- something is broken at file:42\n\n## Major\n*(None)*\n",
			want: true,
		},
		{
			name: "ALL-CAPS 'CRITICAL ISSUES' heading still blocks",
			md:   "## CRITICAL ISSUES\n- file:99 race condition\n",
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBlockingMarkdown(tc.md); got != tc.want {
				t.Errorf("IsBlockingMarkdown: got %v, want %v", got, tc.want)
			}
		})
	}
}
