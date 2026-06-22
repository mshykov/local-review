package review

import (
	"regexp"
	"strings"
)

// IsBlockingMarkdown reports whether a review report (markdown) indicates
// blocking findings — the heuristic the exit-gate consults to decide
// between exit 0 (clean) and exit 2 (major/critical present, which
// pre-commit hooks act on). It is applied in two places by the runner:
// against the consolidated merged report (the primary signal) and against
// each per-LLM output before merge-time truncation (a backstop, so a
// finding past the truncation cutoff can't silently disable the gate).
//
// Two independent signals are used so LLM drift on one output shape
// doesn't quietly disable the gate:
//
//  1. The Recommendation line in the Summary block. The merge prompt
//     pins this to "BLOCK MERGE" / "REQUEST CHANGES" / "APPROVE" — the
//     first two count as blocking. Strongest signal: it's an explicit
//     decision the merger already made.
//  2. Any non-placeholder content under a Critical / Major section
//     heading (with a few common heading variants). Backstop for when
//     the LLM omits the Recommendation line but still lists findings.
//
// Either signal independently trips the gate. False positives are
// preferred to false negatives — this is a security gate: the cost of
// over-blocking is a re-run, the cost of under-blocking is a shipped bug.
func IsBlockingMarkdown(markdown string) bool {
	if markdown == "" {
		return false
	}
	if recommendationIsBlocking(markdown) {
		return true
	}
	for _, name := range []string{
		"Critical Issues", "Critical issues", "CRITICAL ISSUES", "Critical",
		"Major Issues", "Major issues", "MAJOR ISSUES", "Major",
	} {
		if sectionHasContent(markdown, name) {
			return true
		}
	}
	return false
}

// recommendationRE matches the "**Recommendation**: <verdict>" line the
// merge prompt emits in the Summary block. Pre-compiled at package level
// so callers don't pay regexp.MustCompile on every per-LLM output (one
// call per reviewer, but adds up in tests and on big PRs).
var recommendationRE = regexp.MustCompile(`(?im)^\s*-?\s*\**Recommendation\**\s*:\s*(.+?)\s*$`)

// recommendationIsBlocking parses the "**Recommendation**: <verdict>"
// line the merge prompt emits in the Summary block. Returns true when
// the verdict is BLOCK MERGE or REQUEST CHANGES (case-insensitive).
// APPROVE / unrecognized verdicts return false — the section-content
// backstop in IsBlockingMarkdown still runs.
func recommendationIsBlocking(markdown string) bool {
	m := recommendationRE.FindStringSubmatch(markdown)
	if m == nil {
		return false
	}
	verdict := strings.ToUpper(strings.Trim(m[1], "* `"))
	return strings.Contains(verdict, "BLOCK MERGE") || strings.Contains(verdict, "REQUEST CHANGES")
}

// sectionHasContent returns true when a "## <name>" heading has any real
// content before the next "## " heading. We skip blank lines, the
// italicized section descriptions the merge prompt template prescribes
// (`*(...)*`), and a small set of common "no findings" placeholder shapes
// the LLM sometimes uses (`*(None)*`, `*None.*`, `_None_`, bare `None.`).
//
// This is a security gate — false negatives let blocking findings through
// silently — so we lean toward false positives. If the LLM emits findings
// as bullets, prose, numbered lists, or tables, they all count.
func sectionHasContent(markdown, name string) bool {
	re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(name) + `\s*$`)
	loc := re.FindStringIndex(markdown)
	if loc == nil {
		return false
	}
	body := markdown[loc[1]:]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next]
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isNonePlaceholder(line) {
			continue
		}
		return true
	}
	return false
}

// isNonePlaceholder recognizes the empty-section markers the merge prompt
// template emits or that LLMs commonly substitute. Kept narrow on purpose
// — too lenient and it swallows real one-line findings.
func isNonePlaceholder(line string) bool {
	// `*(...)*` — italic parenthetical (section description or *(None)*)
	if strings.HasPrefix(line, "*(") && strings.HasSuffix(line, ")*") {
		return true
	}
	// Bare italic/underscored "None"/"None." with no surrounding content.
	switch strings.ToLower(line) {
	case "*none*", "*none.*", "_none_", "_none._", "none", "none.":
		return true
	}
	return false
}
