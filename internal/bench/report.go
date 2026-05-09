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
//	LLM      Precision  Recall  F1     Noise  Cons.  Median   P95
//	claude   0.83       0.71    0.77   0.50   0.92   4.5s     6.1s
//	gemini   ...
//
//	Per-language F1 (Phase 2):
//	  go      claude=0.89  codex=0.71  gemini=0.50
//	  python  ...
//
//	Per-case detail:
//	  case-id              claude:F1=0.80  gemini:F1=0.50  codex:ERR
//	  ...
//
// The "Cons." column is omitted when no LLM measured consistency
// (single-run benches stay terse). The "Per-language F1" block is
// omitted when the dataset has only one language.
//
// We aim for one screen of useful signal — full per-finding diagnostics
// belong in --json output where consumers can filter at will.
func WriteText(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintf(w, "Bench: dataset=%s  cases=%d  mode=%s\n\n", rep.Dataset, rep.CaseCount, rep.Mode); err != nil {
		return err
	}

	if err := writeOverallTable(w, rep); err != nil {
		return err
	}

	if hasLanguageSplits(rep) {
		if err := writeLanguageBlock(w, rep); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "\nPer-case detail:"); err != nil {
		return err
	}
	for _, line := range perCaseLines(rep) {
		if _, err := fmt.Fprintln(w, "  "+line); err != nil {
			return err
		}
	}

	return nil
}

// writeOverallTable prints the top per-LLM aggregate row. Adds a
// Consistency column only when at least one LLM measured it
// (--repeat > 1 in live mode), so single-run benches stay terse.
//
// Visibility is gated on `Consistency != nil` (the runner sets the
// pointer only when a case had RunCount >= 2), not `> 0` — claude
// originally caught that "> 0" would hide a measured-but-zero
// outcome, which is exactly the worst case the metric is supposed
// to surface.
func writeOverallTable(w io.Writer, rep Report) error {
	showCons := anyConsistencyMeasured(rep)

	headers := []string{"LLM", "Precision", "Recall", "F1", "Noise"}
	if showCons {
		headers = append(headers, "Cons.")
	}
	headers = append(headers, "Median", "P95")
	if err := writeOverallHeader(w, showCons); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		if err := writeOverallRow(w, lr, showCons); err != nil {
			return err
		}
	}
	return nil
}

func writeOverallHeader(w io.Writer, withCons bool) error {
	var err error
	if withCons {
		_, err = fmt.Fprintf(w, "%-10s  %-9s  %-6s  %-6s  %-7s  %-6s  %-8s  %-8s\n",
			"LLM", "Precision", "Recall", "F1", "Noise", "Cons.", "Median", "P95")
	} else {
		_, err = fmt.Fprintf(w, "%-10s  %-9s  %-6s  %-6s  %-7s  %-8s  %-8s\n",
			"LLM", "Precision", "Recall", "F1", "Noise", "Median", "P95")
	}
	return err
}

func writeOverallRow(w io.Writer, lr LLMReport, withCons bool) error {
	var err error
	if withCons {
		// A measured case where every run disagreed renders as
		// "0.00" (a number we want users to see), not "—".
		cons := "—"
		if lr.Consistency != nil {
			cons = fmt.Sprintf("%.2f", *lr.Consistency)
		}
		_, err = fmt.Fprintf(w, "%-10s  %-9.2f  %-6.2f  %-6.2f  %-7.2f  %-6s  %-8s  %-8s\n",
			lr.LLM, lr.Precision, lr.Recall, lr.F1, lr.NoiseRate, cons,
			fmtMs(lr.MedianMs), fmtMs(lr.P95Ms))
	} else {
		_, err = fmt.Fprintf(w, "%-10s  %-9.2f  %-6.2f  %-6.2f  %-7.2f  %-8s  %-8s\n",
			lr.LLM, lr.Precision, lr.Recall, lr.F1, lr.NoiseRate,
			fmtMs(lr.MedianMs), fmtMs(lr.P95Ms))
	}
	return err
}

// anyConsistencyMeasured returns true when at least one LLM has a
// non-nil Consistency pointer, indicating --repeat > 1 in live
// mode produced an aggregate. Used to decide whether the
// Consistency column appears in either text or markdown output.
func anyConsistencyMeasured(rep Report) bool {
	for _, lr := range rep.LLMReports {
		if lr.Consistency != nil {
			return true
		}
	}
	return false
}

// hasLanguageSplits returns true when at least one LLM has per-
// language aggregates populated (the runner skips the split when the
// dataset is single-language).
func hasLanguageSplits(rep Report) bool {
	for _, lr := range rep.LLMReports {
		if len(lr.Languages) > 0 {
			return true
		}
	}
	return false
}

// writeLanguageBlock prints "Per-language F1" — one row per language,
// columns are LLMs in alphabetical order. Cell is the LLM's F1 on
// that language, or "-" if the LLM has no scores for it (typically
// because every case errored).
func writeLanguageBlock(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "\nPer-language F1:"); err != nil {
		return err
	}
	langs := collectLanguages(rep)
	width := longestID(langs)
	for _, lang := range langs {
		var parts []string
		for _, lr := range rep.LLMReports {
			f1 := languageF1(lr, lang)
			if f1 < 0 {
				parts = append(parts, fmt.Sprintf("%s=-", lr.LLM))
			} else {
				parts = append(parts, fmt.Sprintf("%s=%.2f", lr.LLM, f1))
			}
		}
		if _, err := fmt.Fprintf(w, "  %-*s  %s\n", width, lang, strings.Join(parts, "  ")); err != nil {
			return err
		}
	}
	return nil
}

// collectLanguages returns every language id present in any LLM's
// per-language aggregates, sorted for deterministic output.
func collectLanguages(rep Report) []string {
	seen := make(map[string]struct{})
	for _, lr := range rep.LLMReports {
		for _, ls := range lr.Languages {
			seen[ls.Language] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// languageF1 returns the LLM's F1 on the given language, or -1 when
// the LLM has no aggregate for that language. We use a sentinel
// rather than 0 so the report can render "-" instead of misleading
// "0.00" (which suggests "the LLM tried and missed everything"
// rather than "the LLM never scored this language").
func languageF1(lr LLMReport, lang string) float64 {
	for _, ls := range lr.Languages {
		if ls.Language == lang {
			return ls.F1
		}
	}
	return -1
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
