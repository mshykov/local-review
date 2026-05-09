package bench

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteMarkdown emits a leaderboard-style report suitable for
// committing to bench/RESULTS.md. Phase 2 of issue #56:
//
//   # local-review bench leaderboard
//
//   _Generated 2026-05-08T..._  ·  _Dataset: bench/dataset (10 cases)_  ·  _Mode: replay_
//
//   ## Overall
//
//   | LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |
//   | --- | --- | --- | --- | --- | --- | --- | --- |
//   | claude | 0.83 | 0.91 | 0.87 | 0.12 | — | 4.5s | 6.1s |
//   | ...
//
//   ## Per-language F1
//
//   | LLM | go (4) | python (3) | typescript (3) |
//   | --- | --- | --- | --- |
//   | claude | 0.89 | 0.71 | 0.82 |
//   | ...
//
//   ## Per-case detail
//
//   | Case | Lang | claude | codex | gemini |
//   | --- | --- | --- | --- | --- |
//   | go-nil-deref-1 | go | F1=1.00 | F1=0.50 | F1=0.40 |
//   | ...
//
// Sections are omitted when the underlying data isn't there: the
// per-language block stays out for single-language datasets, and the
// Consistency column collapses to "—" when nothing measured it. The
// caller decides where to write — typically `bench/RESULTS.md` via
// `--markdown`.
func WriteMarkdown(w io.Writer, rep Report) error {
	if err := writeMarkdownHeader(w, rep); err != nil {
		return err
	}
	if err := writeMarkdownOverall(w, rep); err != nil {
		return err
	}
	if hasLanguageSplits(rep) {
		if err := writeMarkdownLanguages(w, rep); err != nil {
			return err
		}
	}
	return writeMarkdownPerCase(w, rep)
}

func writeMarkdownHeader(w io.Writer, rep Report) error {
	_, err := fmt.Fprintf(w,
		"# local-review bench leaderboard\n\n"+
			"_Generated %s_ · _Dataset: %s (%d cases)_ · _Mode: %s_\n\n",
		rep.Generated.Format("2006-01-02 15:04 MST"),
		rep.Dataset, rep.CaseCount, rep.Mode,
	)
	return err
}

func writeMarkdownOverall(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Overall"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		cons := "—"
		if lr.Consistency > 0 {
			cons = fmt.Sprintf("%.2f", lr.Consistency)
		}
		if _, err := fmt.Fprintf(w, "| %s | %.2f | %.2f | %.2f | %.2f | %s | %s | %s |\n",
			lr.LLM, lr.Precision, lr.Recall, lr.F1, lr.NoiseRate, cons,
			fmtMs(lr.MedianMs), fmtMs(lr.P95Ms),
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func writeMarkdownLanguages(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Per-language F1"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	langs := collectLanguages(rep)
	caseCount := languageCaseCounts(rep)

	// Header row: "| LLM | go (4) | typescript (3) | ... |"
	header := []string{"LLM"}
	for _, l := range langs {
		header = append(header, fmt.Sprintf("%s (%d)", l, caseCount[l]))
	}
	if err := writeMDRow(w, header); err != nil {
		return err
	}
	if err := writeMDSeparator(w, len(header)); err != nil {
		return err
	}

	for _, lr := range rep.LLMReports {
		row := []string{lr.LLM}
		for _, l := range langs {
			f1 := languageF1(lr, l)
			if f1 < 0 {
				row = append(row, "—")
			} else {
				row = append(row, fmt.Sprintf("%.2f", f1))
			}
		}
		if err := writeMDRow(w, row); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// languageCaseCounts maps language → number of cases scored in that
// language by any LLM. We pull from any one LLM's Languages slice
// because the dataset case-list is identical across LLMs.
func languageCaseCounts(rep Report) map[string]int {
	counts := make(map[string]int)
	for _, lr := range rep.LLMReports {
		for _, ls := range lr.Languages {
			if counts[ls.Language] == 0 {
				counts[ls.Language] = ls.Cases
			}
		}
	}
	return counts
}

func writeMarkdownPerCase(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Per-case detail"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	caseIDs := collectCaseIDs(rep)
	sort.Strings(caseIDs)
	caseLang := caseLanguageMap(rep)
	scores := indexScores(rep)

	header := []string{"Case", "Lang"}
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
		row := []string{id, dashIfEmpty(caseLang[id])}
		for _, lr := range rep.LLMReports {
			cs := scores[id][lr.LLM]
			row = append(row, formatCaseCell(cs))
		}
		if err := writeMDRow(w, row); err != nil {
			return err
		}
	}
	return nil
}

// caseLanguageMap pulls the per-case language out of any LLM's
// CaseScore slice. All LLMs see the same dataset, so any LLM's view
// is sufficient.
func caseLanguageMap(rep Report) map[string]string {
	out := make(map[string]string)
	for _, lr := range rep.LLMReports {
		for _, cs := range lr.Cases {
			if _, ok := out[cs.CaseID]; ok {
				continue
			}
			out[cs.CaseID] = cs.Language
		}
	}
	return out
}

func formatCaseCell(cs *CaseScore) string {
	if cs == nil {
		return "—"
	}
	if cs.Error != "" {
		return "ERR"
	}
	return fmt.Sprintf("F1=%.2f", cs.F1())
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func writeMDRow(w io.Writer, cells []string) error {
	_, err := fmt.Fprintln(w, "| "+strings.Join(cells, " | ")+" |")
	return err
}

func writeMDSeparator(w io.Writer, n int) error {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "---"
	}
	return writeMDRow(w, parts)
}
