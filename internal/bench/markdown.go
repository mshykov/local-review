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
//	# local-review bench leaderboard
//
//	_Generated 2026-05-08T..._  ·  _Dataset: bench/dataset (10 cases)_  ·  _Mode: replay_
//
//	## Overall
//
//	| LLM | Precision | Recall | F1 | Noise | Cons. | Median | P95 |
//	| --- | --- | --- | --- | --- | --- | --- | --- |
//	| claude | 0.83 | 0.91 | 0.87 | 0.12 | — | 4.5s | 6.1s |
//	| ...
//
//	## Per-language F1
//
//	| LLM | go (4) | python (3) | typescript (3) |
//	| --- | --- | --- | --- |
//	| claude | 0.89 | 0.71 | 0.82 |
//	| ...
//
//	## Per-case detail
//
//	| Case | Lang | claude | codex | gemini |
//	| --- | --- | --- | --- | --- |
//	| go-nil-deref-1 | go | F1=1.00 | F1=0.50 | F1=0.40 |
//	| ...
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
	if hasUpliftMeasured(rep) {
		if err := writeMarkdownUplift(w, rep); err != nil {
			return err
		}
		if err := writeMarkdownOverhead(w, rep); err != nil {
			return err
		}
	}
	return writeMarkdownPerCase(w, rep)
}

// writeMarkdownOverhead is the markdown twin of writeOverheadBlock.
// Mirrors the "Overhead vs raw model" sub-table so a committed
// bench/RESULTS.md preserves the same negative-metrics view (time
// and tokens per case) as the text report. See writeOverheadBlock
// for the per-bucket gating rationale.
func writeMarkdownOverhead(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Overhead vs raw model (lower is better)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| LLM | Time/case (Δ) | Tokens/case (Δ) |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- |"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		tMean, tHave := treatmentMeanDurationMs(lr)
		bMean, bHave := baselineMeanDurationMs(lr.Baseline)
		timeCell := markdownOverheadDurationCell(tMean, bMean, tHave && bHave)

		tTok, tTokHave := treatmentMeanTokens(lr)
		bTok, bTokHave := baselineMeanTokens(lr.Baseline)
		tokenCell := markdownOverheadTokenCell(tTok, bTok, tTokHave && bTokHave)

		if _, err := fmt.Fprintf(w, "| %s | %s | %s |\n",
			lr.LLM, timeCell, tokenCell); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// markdownOverheadDurationCell renders one "treatment (Δ)" time
// cell without the text renderer's column padding. Returns "—"
// when the row wasn't measured.
func markdownOverheadDurationCell(treatment, baseline float64, measured bool) string {
	if !measured {
		return "—"
	}
	delta := treatment - baseline
	return fmt.Sprintf("%s (%+.2fs)", fmtMs(int64(treatment)), delta/1000.0)
}

// markdownOverheadTokenCell renders one "treatment (Δ)" token cell
// without column padding.
func markdownOverheadTokenCell(treatment, baseline float64, measured bool) string {
	if !measured {
		return "—"
	}
	delta := treatment - baseline
	return fmt.Sprintf("%s (%s)", fmtTokens(treatment), fmtTokensSigned(delta))
}

// writeMarkdownUplift mirrors writeUpliftBlock for the markdown
// leaderboard. Treatment vs baseline plus the delta as a separate
// "## Uplift over baseline" section so a committed RESULTS.md
// preserves the same headline answer ("is local-review better than
// the raw LLM?") as the text report.
func writeMarkdownUplift(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "## Uplift over baseline (raw LLM, generic prompt)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| LLM | F1 (Δ) | Precision (Δ) | Recall (Δ) | Noise (Δ) | Baseline errors |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		errs := countBaselineErrors(lr)
		errLabel := "0"
		if errs > 0 {
			errLabel = fmt.Sprintf("%d ⚠", errs)
		}
		if !baselineHasAnyNumericData(lr.Baseline) {
			// Same gating as the text renderer: a zero-coverage
			// aggregate must not produce numeric delta cells. See
			// baselineHasAnyNumericData for rationale.
			if _, err := fmt.Fprintf(w, "| %s | — | — | — | — | %s |\n", lr.LLM, errLabel); err != nil {
				return err
			}
			continue
		}
		b := lr.Baseline
		// Per-bucket gating — see writeUpliftBlock for rationale.
		// F1/Precision/Recall gate on non-clean coverage; Noise
		// gates on clean coverage.
		qualityMeasured := b.MeasuredNonCleanCases > 0
		noiseMeasured := b.MeasuredCleanCases > 0
		if _, err := fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n",
			lr.LLM,
			markdownUpliftCell(lr.F1, b.F1, qualityMeasured),
			markdownUpliftCell(lr.Precision, b.Precision, qualityMeasured),
			markdownUpliftCell(lr.Recall, b.Recall, qualityMeasured),
			markdownUpliftCell(lr.NoiseRate, b.NoiseRate, noiseMeasured),
			errLabel,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// markdownUpliftCell returns the standard "treatment (Δ)" cell
// when measured, "—" otherwise. Markdown twin of
// fmtUpliftCellOrDash; sized without the text renderer's column
// padding so the table cells stay tight.
func markdownUpliftCell(treatment, baseline float64, measured bool) string {
	if !measured {
		return "—"
	}
	return fmtUpliftCell(treatment, baseline)
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
		// Pointer-typed Consistency lets a measured-but-zero case
		// (every run produced totally different findings) render as
		// "0.00" instead of being collapsed to "—" alongside the
		// "not measured" case. The latter is single-run benches; we
		// still want to call the former out as a bad signal.
		cons := "—"
		if lr.Consistency != nil {
			cons = fmt.Sprintf("%.2f", *lr.Consistency)
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
