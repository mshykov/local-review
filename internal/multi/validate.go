package multi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ValidateReport checks a merged/formatted report for internal
// contradictions and returns one human-readable warning per problem
// (empty when the report is self-consistent).
//
// Why this exists: the merge step asks an LLM to reformat findings into
// a fixed template with a stated total, one severity section per
// finding, and no duplicates (see merge_prompt.md's "Deduplicate
// Findings"). A weak merge model can silently violate that contract. A
// 2026-07 dogfood run produced a report claiming "Total findings: 5"
// while rendering exactly two bullets — the SAME finding, listed under
// both "Major Issues" and "Info / Notes". Nothing detected it, so a
// malformed report shipped looking authoritative.
//
// These checks are mechanical (the report is our own template, not free
// text), so they catch a bad merge from ANY model rather than
// blacklisting particular ones. They never rewrite the report: the
// findings may still be valuable, and silently "fixing" a count would
// hide that the merge model is unreliable. Warn, show the report, let
// the human judge.
func ValidateReport(markdown string) []string {
	if strings.TrimSpace(markdown) == "" {
		return nil
	}
	sections := findingsBySection(markdown)

	var warnings []string
	if w := checkCountClaim(markdown, sections); w != "" {
		warnings = append(warnings, w)
	}
	warnings = append(warnings, checkCrossSectionDuplicates(sections)...)
	return warnings
}

// severitySections are the finding-bearing headings of the report
// template, in the template's own order.
var severitySections = []string{"Critical Issues", "Major Issues", "Warnings", "Info / Notes"}

var (
	// totalFindingsRE captures the summary's claimed count, e.g.
	// "- **Total findings**: 5".
	totalFindingsRE = regexp.MustCompile(`(?m)^\s*-\s*\*\*Total findings\*\*:\s*(\d+)`)

	// findingBulletRE matches a rendered finding bullet and captures its
	// location key: "- **`path/file.go:42`** — Title". The location is
	// what the template mandates, and it's the only stable identity a
	// finding has across sections.
	findingBulletRE = regexp.MustCompile("(?m)^\\s*-\\s*\\*\\*`([^`]+)`\\*\\*")

	// headingRE matches any level-2 heading, used to slice the report
	// into sections.
	headingRE = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
)

// findingsBySection slices the report at its "## " headings and returns
// the finding location keys rendered under each severity section.
func findingsBySection(markdown string) map[string][]string {
	out := make(map[string][]string)
	idx := headingRE.FindAllStringSubmatchIndex(markdown, -1)
	for i, m := range idx {
		name := strings.TrimSpace(markdown[m[2]:m[3]])
		if !isSeveritySection(name) {
			continue
		}
		end := len(markdown)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := markdown[m[1]:end]
		for _, b := range findingBulletRE.FindAllStringSubmatch(body, -1) {
			out[name] = append(out[name], strings.TrimSpace(b[1]))
		}
	}
	return out
}

func isSeveritySection(name string) bool {
	for _, s := range severitySections {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// checkCountClaim compares the summary's "Total findings" against the
// bullets actually rendered. A mismatch means the reader is being told
// a number the report doesn't substantiate — in the dogfood case, "5"
// against two rendered bullets.
func checkCountClaim(markdown string, sections map[string][]string) string {
	m := totalFindingsRE.FindStringSubmatch(markdown)
	if m == nil {
		return "" // no claim to contradict
	}
	claimed, err := strconv.Atoi(m[1])
	if err != nil {
		return ""
	}
	rendered := 0
	for _, f := range sections {
		rendered += len(f)
	}
	if claimed == rendered {
		return ""
	}
	return fmt.Sprintf("the report claims %d finding(s) but renders %d — the merge model did not follow the report template, so treat both the count and the findings as unreliable", claimed, rendered)
}

// checkCrossSectionDuplicates reports a finding rendered under more than
// one severity section. The template gives each finding exactly one
// severity, and merge_prompt.md explicitly instructs deduplication, so
// this is always a merge-model failure — and a consequential one: the
// same issue counted as both "Major" (blocks merge) and "Info"
// (non-blocking) makes the recommendation meaningless.
func checkCrossSectionDuplicates(sections map[string][]string) []string {
	placement := make(map[string]map[string]bool) // finding -> set of sections
	for name, findings := range sections {
		for _, f := range findings {
			if placement[f] == nil {
				placement[f] = make(map[string]bool)
			}
			placement[f][name] = true
		}
	}

	var warnings []string
	for f, secs := range placement {
		if len(secs) < 2 {
			continue
		}
		names := make([]string, 0, len(secs))
		for s := range secs {
			names = append(names, s)
		}
		sort.Strings(names)
		warnings = append(warnings, fmt.Sprintf("finding %q appears under multiple severity sections (%s) — the merge model duplicated it instead of assigning one severity", f, strings.Join(names, ", ")))
	}
	// Deterministic order: map iteration above is randomised, and a
	// warning list that reshuffles between runs is hard to diff.
	sort.Strings(warnings)
	return warnings
}
