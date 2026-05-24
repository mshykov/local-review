package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteText renders the audit report as a terminal-friendly text
// summary. Headline counts at the top, then per-package sections
// with findings inline. Matches the shape the review path uses so
// users don't have to learn two formats.
func WriteText(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintf(w, "Audit: topic=%s  llm=%s  packages=%d  findings=%d\n\n",
		rep.Topic, rep.LLM, len(rep.Packages), rep.TotalFindings); err != nil {
		return err
	}
	if err := writeTextSummary(w, rep); err != nil {
		return err
	}
	for _, pr := range rep.Packages {
		if err := writeTextPackage(w, pr); err != nil {
			return err
		}
	}
	return nil
}

func writeTextSummary(w io.Writer, rep Report) error {
	sevs := []string{"critical", "major", "warning", "info"}
	parts := []string{}
	for _, s := range sevs {
		if n := rep.FindingsBySeverity[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", s, n))
		}
	}
	bySev := "none"
	if len(parts) > 0 {
		bySev = strings.Join(parts, " ")
	}
	_, err := fmt.Fprintf(w, "Severity breakdown: %s\nPackages: %d with findings, %d clean, %d errored\n\n",
		bySev, rep.PackagesWithFindings, rep.PackagesClean, rep.PackagesErrored)
	return err
}

func writeTextPackage(w io.Writer, pr PackageReport) error {
	header := fmt.Sprintf("── %s ── %d file%s", pr.Package, len(pr.Files), pluralS(len(pr.Files)))
	if pr.Error != "" {
		_, err := fmt.Fprintf(w, "%s  [ERR] %s\n\n", header, pr.Error)
		return err
	}
	if pr.Clean || len(pr.Findings) == 0 {
		_, err := fmt.Fprintf(w, "%s  ✓ clean\n\n", header)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n", header); err != nil {
		return err
	}
	for _, f := range pr.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		if _, err := fmt.Fprintf(w, "  [%s] %s\n", f.Severity, loc); err != nil {
			return err
		}
		for _, bl := range strings.Split(strings.TrimSpace(f.Body), "\n") {
			if _, err := fmt.Fprintf(w, "    %s\n", bl); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return nil
}

// WriteJSON emits the full Report as indented JSON. Useful for
// downstream tooling that wants to diff audit deltas between
// commits.
func WriteJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// WriteMarkdown renders a committable audit report — same shape as
// the bench RESULTS.md leaderboard but for audit data. Default
// sink for the --out flag when the path ends in .md.
//
// Sections:
//
//	# Audit — <topic>
//	_Generated <ts>_ · _LLM: <llm>_ · _Packages: N_ · _Findings: N_
//
//	## Summary
//	| Severity | Count |
//	| ... | ... |
//
//	## <package> (N files)
//	- [severity] path:line — body
//	- ...
//
// Clean packages are folded into one trailing "Clean packages" line
// to keep the report scannable.
func WriteMarkdown(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintf(w, "# Audit — %s\n\n_Generated %s_ · _LLM: %s_ · _Packages: %d_ · _Findings: %d_\n\n",
		rep.Topic, rep.Generated.Format("2006-01-02 15:04 MST"), rep.LLM, len(rep.Packages), rep.TotalFindings); err != nil {
		return err
	}
	if err := writeMarkdownSummary(w, rep); err != nil {
		return err
	}
	// Group: packages with findings first, then clean (collapsed),
	// then errored (so they stand out at the bottom).
	var clean, errored []PackageReport
	for _, pr := range rep.Packages {
		if pr.Error != "" {
			errored = append(errored, pr)
			continue
		}
		if pr.Clean || len(pr.Findings) == 0 {
			clean = append(clean, pr)
			continue
		}
		if err := writeMarkdownPackage(w, pr); err != nil {
			return err
		}
	}
	if len(clean) > 0 {
		if err := writeMarkdownCleanList(w, clean); err != nil {
			return err
		}
	}
	if len(errored) > 0 {
		if err := writeMarkdownErroredList(w, errored); err != nil {
			return err
		}
	}
	return nil
}

func writeMarkdownSummary(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Summary"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Severity | Count |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	for _, s := range []string{"critical", "major", "warning", "info"} {
		if _, err := fmt.Fprintf(w, "| %s | %d |\n", s, rep.FindingsBySeverity[s]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func writeMarkdownPackage(w io.Writer, pr PackageReport) error {
	if _, err := fmt.Fprintf(w, "## %s\n\n_%d file%s audited_\n\n",
		pr.Package, len(pr.Files), pluralS(len(pr.Files))); err != nil {
		return err
	}
	for _, f := range pr.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		if _, err := fmt.Fprintf(w, "- **[%s]** `%s`\n", f.Severity, loc); err != nil {
			return err
		}
		for _, bl := range strings.Split(strings.TrimSpace(f.Body), "\n") {
			if _, err := fmt.Fprintf(w, "  %s\n", bl); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func writeMarkdownCleanList(w io.Writer, clean []PackageReport) error {
	if _, err := fmt.Fprintln(w, "## Clean packages"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	names := make([]string, 0, len(clean))
	for _, pr := range clean {
		names = append(names, pr.Package)
	}
	sort.Strings(names)
	if _, err := fmt.Fprintf(w, "_%d package%s with no findings:_ %s\n\n",
		len(names), pluralS(len(names)), strings.Join(names, ", ")); err != nil {
		return err
	}
	return nil
}

func writeMarkdownErroredList(w io.Writer, errored []PackageReport) error {
	if _, err := fmt.Fprintln(w, "## Errored packages"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, pr := range errored {
		if _, err := fmt.Fprintf(w, "- **%s** — %s\n", pr.Package, pr.Error); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
