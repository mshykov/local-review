package bench

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteTextSWE prints the SWE-bench catch-rate summary to w. Format
// mirrors the existing bench WriteText for consistency:
//
//	Bench (swe-bench-lite): dataset=<path>  cases=N  mode=<cli|replay>
//
//	LLM        Tasks  Caught  Missed  Errors  Catch rate
//	claude     10     7       3       0       70%
//	codex      10     6       4       0       60%
//	...
//
//	Per-task detail:
//	  task-id                       claude:caught  codex:caught  gemini:missed
//	  ...
//
// Caller picks where to write — stdout for interactive, file for
// committing into bench/RESULTS.md.
func WriteTextSWE(w io.Writer, rep SWEBenchReport) error {
	if _, err := fmt.Fprintf(w, "Bench (swe-bench-lite): dataset=%s  cases=%d  mode=%s\n\n", rep.Dataset, rep.CaseCount, rep.Mode); err != nil {
		return err
	}
	if err := writeSWEOverallTable(w, rep); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nPer-task detail:"); err != nil {
		return err
	}
	return writeSWEPerCase(w, rep)
}

// WriteJSONSWE emits the full SWE-bench report as indented JSON.
// Consumers diff catch rates across commits the same way they
// diff the existing bench JSON output.
func WriteJSONSWE(w io.Writer, rep SWEBenchReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// WriteMarkdownSWE emits the SWE-bench section in a shape suitable
// for committing to bench/RESULTS.md, sibling to the existing
// labelled-dataset leaderboard:
//
//	## SWE-bench-lite catch rate
//
//	_Generated <ts>_ · _Dataset: <path> (N cases)_ · _Mode: <mode>_
//
//	| LLM | Tasks | Caught | Missed | Errors | Catch rate |
//	| --- | ---   | ---    | ---    | ---    | ---        |
//	| claude | 10 | 7      | 3      | 0      | 70%        |
//
//	### Per-task detail
//	...
//
// Both sections (existing bench leaderboard + SWE-bench catch rate)
// can be committed to the same RESULTS.md by running the bench
// twice — once normal, once with --swe-bench — and concatenating.
// A future commit may add a --combined flag that runs both in one
// pass; for v1 the responsibility is on the caller.
func WriteMarkdownSWE(w io.Writer, rep SWEBenchReport) error {
	if _, err := fmt.Fprintf(w,
		"## SWE-bench-lite catch rate\n\n"+
			"_Generated %s_ · _Dataset: %s (%d cases)_ · _Mode: %s_\n\n",
		rep.Generated.Format("2006-01-02 15:04 MST"),
		rep.Dataset, rep.CaseCount, rep.Mode,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| LLM | Tasks | Caught | Missed | Errors | Catch rate |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		missed := lr.Tasks - lr.CaughtCount - lr.Errors
		if _, err := fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %s |\n",
			lr.LLM, lr.Tasks, lr.CaughtCount, missed, lr.Errors, fmtSWEPercent(lr.CatchRate),
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return writeSWEMarkdownPerCase(w, rep)
}

func writeSWEOverallTable(w io.Writer, rep SWEBenchReport) error {
	if _, err := fmt.Fprintf(w, "%-10s  %-5s  %-6s  %-6s  %-6s  %-10s\n",
		"LLM", "Tasks", "Caught", "Missed", "Errors", "Catch rate"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		missed := lr.Tasks - lr.CaughtCount - lr.Errors
		if _, err := fmt.Fprintf(w, "%-10s  %-5d  %-6d  %-6d  %-6d  %-10s\n",
			lr.LLM, lr.Tasks, lr.CaughtCount, missed, lr.Errors, fmtSWEPercent(lr.CatchRate),
		); err != nil {
			return err
		}
	}
	return nil
}

func writeSWEPerCase(w io.Writer, rep SWEBenchReport) error {
	for _, line := range sweBenchPerCaseLines(rep) {
		if _, err := fmt.Fprintln(w, "  "+line); err != nil {
			return err
		}
	}
	return nil
}

func writeSWEMarkdownPerCase(w io.Writer, rep SWEBenchReport) error {
	if _, err := fmt.Fprintln(w, "### Per-task detail"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	caseIDs := collectSWECaseIDs(rep)
	scores := indexSWEScores(rep)

	header := []string{"Task"}
	for _, lr := range rep.LLMReports {
		header = append(header, lr.LLM)
	}
	if err := writeMDRow(w, header); err != nil {
		return err
	}
	if err := writeMDSeparator(w, len(header)); err != nil {
		return err
	}
	for _, id := range caseIDs {
		row := []string{id}
		for _, lr := range rep.LLMReports {
			cs := scores[id][lr.LLM]
			row = append(row, formatSWECell(cs))
		}
		if err := writeMDRow(w, row); err != nil {
			return err
		}
	}
	return nil
}

func formatSWECell(cs *SWEBenchScore) string {
	if cs == nil {
		return "—"
	}
	if cs.Error != "" {
		return "ERR"
	}
	if cs.Caught {
		return "✓"
	}
	return "✗"
}

func collectSWECaseIDs(rep SWEBenchReport) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, lr := range rep.LLMReports {
		for _, cs := range lr.Cases {
			if _, ok := seen[cs.CaseID]; ok {
				continue
			}
			seen[cs.CaseID] = struct{}{}
			ids = append(ids, cs.CaseID)
		}
	}
	return ids
}

func indexSWEScores(rep SWEBenchReport) map[string]map[string]*SWEBenchScore {
	out := make(map[string]map[string]*SWEBenchScore)
	for i := range rep.LLMReports {
		lr := &rep.LLMReports[i]
		for j := range lr.Cases {
			cs := &lr.Cases[j]
			if out[cs.CaseID] == nil {
				out[cs.CaseID] = make(map[string]*SWEBenchScore)
			}
			out[cs.CaseID][lr.LLM] = cs
		}
	}
	return out
}

func sweBenchPerCaseLines(rep SWEBenchReport) []string {
	caseIDs := collectSWECaseIDs(rep)
	scores := indexSWEScores(rep)
	var lines []string
	width := longestID(caseIDs)
	for _, id := range caseIDs {
		var parts []string
		for _, lr := range rep.LLMReports {
			cs := scores[id][lr.LLM]
			if cs == nil {
				continue
			}
			label := "caught"
			if cs.Error != "" {
				label = "ERR"
			} else if !cs.Caught {
				label = "missed"
			}
			parts = append(parts, fmt.Sprintf("%s:%s", lr.LLM, label))
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, id, joinWithDouble(parts)))
	}
	return lines
}

// fmtSWEPercent renders a fractional rate as "NN%" with sensible
// rounding. "—" when the denominator was zero (Tasks == 0, which
// shouldn't happen in practice but stays robust against an empty
// dataset). Always integer percent in v1 for at-a-glance reading;
// JSON output carries the exact float for tools that want more
// precision.
func fmtSWEPercent(r float64) string {
	if r < 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", r*100)
}

// joinWithDouble joins parts with two spaces — same separator
// the existing bench per-case lines use; keeps the columns
// scannable when LLM names are short.
func joinWithDouble(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "  "
		}
		out += p
	}
	return out
}
