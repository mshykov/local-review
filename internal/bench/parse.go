package bench

import (
	"regexp"
	"strconv"
	"strings"
)

// fileLineRE captures "<path>:<line>" tokens in LLM markdown output.
//
// Path is a permissive run of identifier-like characters plus the
// usual path separators and the dot. Line is a positive integer.
//
// We accept four common shapes the agents emit:
//
//	src/foo.go:42
//	`src/foo.go:42`            (markdown inline-code wrap)
//	src/foo.go line 42
//	src/foo.go:L42             (GitHub-style anchor)
//
// The first two alternations require a path-extension boundary
// (`\.[a-zA-Z]+`) so we don't match arbitrary "name:42" tokens like
// git SHAs or "version: 0.42" text in headings.
//
// The trailing alternation matches a curated set of well-known
// extensionless filenames (Dockerfile, Makefile, Rakefile, Procfile,
// Gemfile, Jenkinsfile) so findings on those files aren't silently
// dropped. Adding a new extensionless filename is a one-line edit;
// adding broad extensionless-anywhere matching would create too many
// false positives ("repo: 42 commits" → finding).
var fileLineRE = regexp.MustCompile(
	`(?:` + // outer alternation
		`([\w./\-+]+\.[a-zA-Z]+):L?(\d+)` + // path.ext:42 or path.ext:L42
		`|` +
		`([\w./\-+]+\.[a-zA-Z]+)\s+line\s+(\d+)` + // path.ext line 42
		`|` +
		// Curated extensionless filenames, optionally with a leading
		// directory path. Word boundary on the left rejects matches
		// where the filename appears as a suffix of a longer token
		// (e.g. "MyDockerfile" should not match).
		`(?:^|[^\w/])((?:[\w./\-+]+/)?(?:Dockerfile|Makefile|Rakefile|Procfile|Gemfile|Jenkinsfile)):L?(\d+)` +
		`)`,
)

// severityHeadingRE picks up the multi-LLM markdown override sections.
// The override pins headings to "## Critical Issues / ## Major Issues /
// ## Warnings / ## Info / Notes" but LLMs paraphrase, so we match a
// permissive set: any `## ` line whose text contains one of the known
// severity words.
var severityHeadingRE = regexp.MustCompile(`(?im)^#{1,3}\s+([^\n]+)$`)

// ParseFindings extracts ProducedFindings from an LLM's review markdown.
//
// Strategy: scan line by line. Track the current severity from the most
// recent `## <heading>` line. For every line containing a path:line
// token, emit one finding with that location and the current severity.
// Dedupe by (file, line) within the same severity — LLMs sometimes
// repeat the same anchor across "summary" and "detail" subsections.
//
// Best-effort by design. We don't try to identify "category" or pull
// the suggestion-fix block — those are inconsistent across reviewers
// and v1 scoring doesn't need them.
func ParseFindings(markdown string) []ProducedFinding {
	if strings.TrimSpace(markdown) == "" {
		return nil
	}

	var out []ProducedFinding
	seen := make(map[string]struct{})
	severity := ""

	for _, line := range strings.Split(markdown, "\n") {
		if h := severityHeadingRE.FindStringSubmatch(line); h != nil {
			// Only update severity when the heading carries a
			// recognised severity word. The previous form
			// unconditionally clobbered it on every heading, so a
			// `### Details` or `### Suggested fix` sub-heading inside
			// a "## Major Issues" block would silently strip severity
			// from the findings underneath. Inherit the parent
			// severity instead — that's what reviewers actually mean
			// when they nest sub-sections.
			if s := inferSeverity(h[1]); s != "" {
				severity = s
			}
			continue
		}

		matches := fileLineRE.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			file, lineStr := pickPathAndLine(m)
			n, err := strconv.Atoi(lineStr)
			if err != nil || n <= 0 {
				continue
			}
			key := severity + "|" + file + ":" + lineStr
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ProducedFinding{
				File:     file,
				Line:     n,
				Severity: severity,
				Snippet:  strings.TrimSpace(line),
			})
		}
	}
	return out
}

// pickPathAndLine pulls the non-empty (path, line) pair out of the
// regex submatch.
//
// fileLineRE has three alternations; the resulting capture-group
// layout is (m[0] is the full match):
//
//	m[1], m[2] — path.ext:N or path.ext:LN
//	m[3], m[4] — path.ext line N
//	m[5], m[6] — extensionless filename (Dockerfile, Makefile, …) :N
//
// Go's regexp engine returns "" for unused branches, so we walk the
// pairs and return the first populated one. **If you add an
// alternation to fileLineRE, add a branch here** — silently dropping
// the pair would just stop emitting that finding shape with no
// other signal.
func pickPathAndLine(m []string) (string, string) {
	if m[1] != "" {
		return m[1], m[2]
	}
	if m[3] != "" {
		return m[3], m[4]
	}
	return m[5], m[6]
}

// inferSeverity normalises a heading line into one of the canonical
// severity tiers. Returns "" when the heading has no severity word —
// findings under such a heading inherit the empty-string severity,
// which the scorer treats as "unknown" (still counts toward TP/FP, but
// not used to bias matching).
func inferSeverity(heading string) string {
	h := strings.ToLower(heading)
	switch {
	case strings.Contains(h, "critical"):
		return "critical"
	case strings.Contains(h, "major"):
		return "major"
	case strings.Contains(h, "warning"):
		return "warning"
	case strings.Contains(h, "info"), strings.Contains(h, "note"):
		return "info"
	case strings.Contains(h, "nit"):
		return "nit"
	}
	return ""
}
