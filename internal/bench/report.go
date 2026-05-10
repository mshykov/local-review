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

	if hasUpliftMeasured(rep) {
		if err := writeUpliftBlock(w, rep); err != nil {
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

// hasUpliftMeasured returns true when at least one LLM has a
// non-nil Baseline aggregate OR at least one case recorded a
// BaselineError (--uplift was active even if every baseline pass
// errored). Used by both the text and markdown writers to gate
// the uplift block — single-pass benches don't see this section
// at all, keeping the default report terse, while a fully-failed
// baseline still surfaces (otherwise users see "no uplift section"
// and assume --uplift didn't run).
func hasUpliftMeasured(rep Report) bool {
	for _, lr := range rep.LLMReports {
		if lr.Baseline != nil {
			return true
		}
		for _, cs := range lr.Cases {
			if cs.BaselineError != "" {
				return true
			}
		}
	}
	return false
}

// countBaselineErrors returns the number of cases for an LLM where
// the --uplift baseline pass errored. Surfaced in the uplift block
// so a "0.91 vs 0.42" headline doesn't hide that 3/10 baseline
// passes failed and the comparison is over an unrepresentative
// subset.
func countBaselineErrors(lr LLMReport) int {
	n := 0
	for _, cs := range lr.Cases {
		if cs.BaselineError != "" {
			n++
		}
	}
	return n
}

// writeUpliftBlock renders the "Uplift over baseline" comparison:
// per-LLM treatment vs baseline scores plus the delta. The block
// is the headline answer to "is local-review better than running
// the raw LLM cold?" — appears between the per-language F1 grid
// (specific) and the per-case detail (debugging).
//
// Format mirrors writeOverallTable's spacing for visual consistency.
// The delta column uses signed format ("+0.32") so a regression
// (negative delta) is unambiguous at a glance.
func writeUpliftBlock(w io.Writer, rep Report) error {
	if _, err := fmt.Fprintln(w, "\nUplift over baseline (raw LLM, generic prompt):"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%-10s  %-13s  %-13s  %-13s  %-13s\n",
		"LLM", "F1 (Δ)", "Precision (Δ)", "Recall (Δ)", "Noise (Δ)"); err != nil {
		return err
	}
	for _, lr := range rep.LLMReports {
		errs := countBaselineErrors(lr)
		if !baselineHasNumericData(lr.Baseline) {
			// Either uplift wasn't run for this LLM, or every
			// baseline pass errored. Render a single status line
			// instead of numeric deltas — iter-3 self-review
			// flagged that printing "0.91 (+0.91)" against a
			// zero-baseline that nobody measured is a misleading
			// headline. The aggregate may still be present in
			// JSON for the "feature attempted" signal.
			label := "(not measured)"
			if errs > 0 {
				label = fmt.Sprintf("(baseline failed on %d case(s))", errs)
			}
			if _, err := fmt.Fprintf(w, "%-10s  %s\n", lr.LLM, label); err != nil {
				return err
			}
			continue
		}
		b := lr.Baseline
		if _, err := fmt.Fprintf(w, "%-10s  %s  %s  %s  %s\n",
			lr.LLM,
			fmtUpliftCell(lr.F1, b.F1),
			fmtUpliftCell(lr.Precision, b.Precision),
			fmtUpliftCell(lr.Recall, b.Recall),
			fmtUpliftCell(lr.NoiseRate, b.NoiseRate),
		); err != nil {
			return err
		}
		if errs > 0 {
			// Even with a populated aggregate we want users to
			// see "this comparison covers M of N cases" when the
			// baseline partially failed — the aggregate itself
			// micro-averages over only the survivors and would
			// otherwise misleadingly look like full coverage.
			if _, err := fmt.Fprintf(w, "%-10s  note: baseline failed on %d case(s); delta is over the surviving subset only\n", "", errs); err != nil {
				return err
			}
		}
	}
	return nil
}

// baselineHasNumericData returns true when the aggregate carries
// at least one case worth of measured numbers. False when nil
// (uplift not run) OR present-but-MeasuredCases==0 (uplift
// attempted, every baseline errored). Renderers gate the numeric
// delta cells on this so an attempted-but-fully-failed run never
// shows up as "treatment 0.91 (+0.91)" — that would imply the
// raw model scored 0 when in fact it was never scored at all.
func baselineHasNumericData(b *LLMBaselineAggregate) bool {
	return b != nil && b.MeasuredCases > 0
}

// fmtUpliftCell renders a single "treatment (Δsign)" cell —
// e.g. "0.91 (+0.32)" or "0.47 (-0.05)". Width-padded to fit the
// column header; signed delta makes the direction obvious at a
// glance (the sign matters for noise rate, where lower is
// better — a positive delta there is a regression).
func fmtUpliftCell(treatment, baseline float64) string {
	delta := treatment - baseline
	return fmt.Sprintf("%4.2f (%+5.2f)", treatment, delta)
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
// Consistency column appears in the text report; the markdown
// leaderboard always includes the Cons. column (rendering "—"
// for unmeasured LLMs).
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
