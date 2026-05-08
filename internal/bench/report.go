package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteText prints a human-readable summary of the report. Format:
//
//	Bench: dataset=<path>  cases=N  mode=<cli|replay>
//
//	LLM      Precision  Recall  F1     Noise  Median   P95
//	claude   0.83       0.71    0.77   0.50   4.5s     6.1s
//	gemini   ...
//
//	Per-case detail:
//	  case-id              claude:F1=0.80  gemini:F1=0.50  codex:ERR
//	  ...
//
// We aim for one screen of useful signal — full per-finding diagnostics
// belong in --json output where consumers can filter at will.
func WriteText(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintf(w, "Bench: dataset=%s  cases=%d  mode=%s\n\n", rep.Dataset, rep.CaseCount, rep.Mode); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "%-10s  %-9s  %-6s  %-6s  %-7s  %-8s  %-8s\n", "LLM", "Precision", "Recall", "F1", "Noise", "Median", "P95"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		if _, err := fmt.Fprintf(w, "%-10s  %-9.2f  %-6.2f  %-6.2f  %-7.2f  %-8s  %-8s\n",
			lr.LLM, lr.Precision, lr.Recall, lr.F1, lr.NoiseRate,
			fmtMs(lr.MedianMs), fmtMs(lr.P95Ms),
		); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "\nPer-case detail:"); err != nil {
		return err
	}
	caseLines := perCaseLines(rep)
	for _, line := range caseLines {
		if _, err := fmt.Fprintln(w, "  "+line); err != nil {
			return err
		}
	}

	return nil
}

// WriteJSON emits the full report as indented JSON. Consumers can diff
// numbers across commits or feed it into a leaderboard generator.
func WriteJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// perCaseLines returns one row per case showing per-LLM scores side by
// side. Order: cases by ID (sorted), LLMs in their report order
// (already sorted alphabetically by Run).
func perCaseLines(rep Report) []string {
	caseIDs := collectCaseIDs(rep)
	sort.Strings(caseIDs)
	scoreByCaseLLM := indexScores(rep)

	var lines []string
	width := longestID(caseIDs)
	for _, id := range caseIDs {
		var parts []string
		for _, lr := range rep.LLMReports {
			cs := scoreByCaseLLM[id][lr.LLM]
			if cs == nil {
				continue
			}
			if cs.Error != "" {
				parts = append(parts, fmt.Sprintf("%s:ERR", lr.LLM))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s:F1=%.2f", lr.LLM, cs.F1()))
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, id, strings.Join(parts, "  ")))
	}
	return lines
}

func collectCaseIDs(rep Report) []string {
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

func indexScores(rep Report) map[string]map[string]*CaseScore {
	out := make(map[string]map[string]*CaseScore)
	for i := range rep.LLMReports {
		lr := &rep.LLMReports[i]
		for j := range lr.Cases {
			cs := &lr.Cases[j]
			if out[cs.CaseID] == nil {
				out[cs.CaseID] = make(map[string]*CaseScore)
			}
			out[cs.CaseID][lr.LLM] = cs
		}
	}
	return out
}

func longestID(ids []string) int {
	max := 0
	for _, id := range ids {
		if len(id) > max {
			max = len(id)
		}
	}
	return max
}

// fmtMs formats a millisecond duration for the text report.
//
// Negative values are ambiguous (impossible with our inputs but a
// sentinel some callers might use) and render as "-". Zero is a
// legitimate value for replay-mode runs that complete in under a
// millisecond — render it as "0ms" rather than the misleading "-"
// that Copilot caught in review. The caller decides whether to
// short-circuit on "no data" *before* reaching here.
func fmtMs(ms int64) string {
	if ms < 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}
