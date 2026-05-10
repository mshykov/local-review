// Package bench is the local-review benchmark harness: load a labelled
// dataset of diffs, run each diff through one or more LLMs (or pre-
// recorded fixtures via --replay), and score the resulting findings
// against the labels.
//
// The goal is a reproducible signal — precision, recall, F1, noise —
// for prompt + model changes, so the project doesn't have to guess
// whether a release moved review quality forward or backward.
package bench

import "time"

// Case is one labelled diff in the dataset. Loaded from
// bench/dataset/<id>/case.yaml plus a sibling diff.patch file.
type Case struct {
	// Stable identifier; matches the directory name on disk. Used as the
	// fixture lookup key in --replay mode.
	ID string `yaml:"id"`

	// Free-form one-line title for human-readable reports.
	Title string `yaml:"title"`

	// Optional longer description of what the diff does and why the
	// expected findings are correct.
	Description string `yaml:"description,omitempty"`

	// Language id (matches internal/lang ids: "go", "typescript",
	// "python", "rust", "default", ...). Drives prompt-pack selection.
	// Empty falls back to "default".
	Language string `yaml:"language,omitempty"`

	// Clean=true marks a diff that should produce zero findings. Used
	// for the noise-rate metric (issue #56, "Noise / false-positive
	// benchmark"). When true, Expected SHOULD be empty; if it isn't,
	// loader fails fast.
	Clean bool `yaml:"clean,omitempty"`

	// Expected findings the reviewer is supposed to catch. Empty for
	// clean cases.
	Expected []ExpectedFinding `yaml:"expected,omitempty"`

	// DiffPath is the resolved on-disk path of the diff body
	// (bench/dataset/<id>/diff.patch). Populated by Load*; not part of
	// the YAML schema.
	DiffPath string `yaml:"-"`

	// Diff is the raw text fed to the reviewer LLM. Populated by Load*.
	Diff string `yaml:"-"`
}

// ExpectedFinding describes one bug or issue the reviewer should catch.
// Matching is intentionally loose — see score.go for the exact rules —
// because LLM line numbers wander by ±a few lines on the same diff.
//
// Both yaml and json tags are set: yaml is read from case.yaml,
// json is emitted in the bench Report (CaseScore.Missed,
// MatchPair.Expected). Without explicit json tags the JSON output
// would carry CamelCase field names while the rest of the report
// uses snake_case, breaking schema consistency for downstream
// consumers.
type ExpectedFinding struct {
	// File the bug lives in (matched by suffix, so "foo.go" matches
	// "src/foo.go" — LLMs vary on whether they emit relative or full
	// paths).
	File string `yaml:"file" json:"file"`

	// Line number of the bug. Matching uses Window (default 3) on each
	// side; a produced finding within [Line-Window, Line+Window] counts.
	Line int `yaml:"line" json:"line"`

	// Window in lines around Line that still counts as a match. Zero
	// means "use the global default" (3). Per-finding override exists
	// because some bugs have a clear single-line locus (nil deref) and
	// others span a small block (race window, error-handling section).
	Window int `yaml:"window,omitempty" json:"window,omitempty"`

	// Optional category hint: "security", "correctness", "performance",
	// "style". Recorded but NOT required for a match in v1 — the
	// markdown produced by LLM CLIs doesn't reliably preserve category
	// labels, and forcing a match would inflate false negatives.
	Category string `yaml:"category,omitempty" json:"category,omitempty"`

	// Optional severity hint: "critical", "major", "warning", "info",
	// "nit". Recorded but not required for a match in v1; LLMs grade
	// severity inconsistently across runs.
	Severity string `yaml:"severity,omitempty" json:"severity,omitempty"`

	// Human-readable note about what the finding should call out. Shown
	// in reports next to "MISSED" lines so a maintainer reviewing a
	// regression can tell at a glance which bug the reviewer dropped.
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
}

// ProducedFinding is one finding extracted from the LLM's review
// markdown. The bench parser scans for "path/to/file.ext:LINE" tokens
// and groups them under the most recent "## <Severity>" heading.
type ProducedFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity,omitempty"` // best-effort, from the closest preceding heading
	Snippet  string `json:"snippet,omitempty"`  // short context line for human reports
}

// BaselineScore is the score from the bench's --uplift baseline
// pass: the SAME case run through the SAME LLM, but with a
// minimal generic system prompt instead of local-review's
// language-specific pack + multi-LLM merge pipeline. Gives the
// "vs raw model" comparison the harness's primary numbers don't
// answer on their own.
//
// Only populated when --uplift was active; nil/zero otherwise.
type BaselineScore struct {
	TruePositives  int   `json:"true_positives"`
	FalsePositives int   `json:"false_positives"`
	FalseNegatives int   `json:"false_negatives"`
	Produced       int   `json:"produced"`
	DurationMs     int64 `json:"duration_ms"`
}

// Precision = TP / (TP + FP). Returns 0 when no findings produced.
func (b BaselineScore) Precision() float64 {
	if b.TruePositives+b.FalsePositives == 0 {
		return 0
	}
	return float64(b.TruePositives) / float64(b.TruePositives+b.FalsePositives)
}

// Recall = TP / (TP + FN). Returns 1 when no expected findings
// (clean case), matching the CaseScore.Recall convention.
func (b BaselineScore) Recall() float64 {
	if b.TruePositives+b.FalseNegatives == 0 {
		return 1
	}
	return float64(b.TruePositives) / float64(b.TruePositives+b.FalseNegatives)
}

// F1 = 2*P*R / (P+R). Returns 0 when both are zero.
func (b BaselineScore) F1() float64 {
	p, r := b.Precision(), b.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// CaseScore is the per-case scoring outcome.
type CaseScore struct {
	CaseID  string `json:"case_id"`
	LLM     string `json:"llm"`
	Version string `json:"version,omitempty"`

	// Language carries the case's language id ("go", "typescript", …)
	// so per-language aggregation in the LLMReport doesn't have to
	// re-walk the dataset to look it up.
	Language string `json:"language,omitempty"`

	// Counts that drive precision/recall.
	TruePositives  int `json:"true_positives"`
	FalsePositives int `json:"false_positives"`
	FalseNegatives int `json:"false_negatives"`

	// Total findings produced (TP + FP) for noise-rate calculation.
	Produced int `json:"produced"`

	// Clean mirrors Case.Clean ("this diff is supposed to produce
	// zero findings"). Carried on the score so aggregate code paths
	// — both the treatment side (tallyCases) and the --uplift
	// baseline side (fillBaselineAggregate) — can route a case into
	// the noise-rate bucket vs. the precision/recall bucket from a
	// single explicit signal instead of inferring it from
	// treatment-side TP/FN counts. The earlier heuristic conflated
	// "case has no expected findings" with "treatment found zero
	// matches", which would misclassify a noisy treatment run on a
	// truly-clean case if the score fields ever drifted out of sync.
	Clean bool `json:"clean,omitempty"`

	// Match details for human reports.
	Matched []MatchPair       `json:"matched,omitempty"`
	Missed  []ExpectedFinding `json:"missed,omitempty"`
	Extra   []ProducedFinding `json:"extra,omitempty"`

	// Timing + status. Duration is serialised in milliseconds rather
	// than nanoseconds for cross-language consumer ergonomics — the
	// JSON shape mirrors metadata.json's *_ms fields.
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`

	// Mode is "cli" or "replay" — recorded in JSON output so consumers
	// can tell live runs from cached ones.
	Mode string `json:"mode"`

	// Phase-2 consistency metric.
	//
	// Attempts is the number of times the runner *tried* to sample
	// this (case, LLM) pair (== opts.Repeat for repeated benches,
	// 0 for single-run). RunCount is the number that actually
	// returned output (Attempts minus the failures in
	// sampleConsistency). When Attempts > RunCount, some repeats
	// errored — Jaccard is computed only on the survivors, which
	// would otherwise misleadingly report e.g. "1.0" for a 5-repeat
	// case where 3 attempts crashed and the remaining 2 happened to
	// agree. Surfacing the gap lets consumers treat such cases with
	// skepticism instead of committing inflated stability scores to
	// the leaderboard.
	//
	// Jaccard is the |∩| / |∪| of finding (file, line) tuples
	// across the surviving runs — 1.0 means every run produced the
	// same set, 0.0 means no overlap.
	//
	// Jaccard is pointer-typed so a measured-but-zero value (every
	// run produced a completely different set of findings) renders
	// as 0.0 in JSON instead of being silently dropped by omitempty.
	// JSON consumers should treat Jaccard != nil as the "consistency
	// was measured" signal; RunCount >= 2 confirms the sample count.
	// All three fields are absent in JSON for single-run benches.
	Attempts int      `json:"attempts,omitempty"`
	RunCount int      `json:"run_count,omitempty"`
	Jaccard  *float64 `json:"jaccard,omitempty"`

	// Baseline carries the --uplift baseline score (same case,
	// same LLM, generic system prompt) so the report can render
	// "treatment vs baseline" deltas. Pointer-typed: nil means
	// "no baseline measured" (no --uplift, or the baseline pass
	// errored). The CaseScore's primary fields hold the TREATMENT
	// (full local-review) result.
	Baseline *BaselineScore `json:"baseline,omitempty"`

	// BaselineError is populated when --uplift was active but the
	// baseline pass for this case errored. Without this field, a
	// silent baseline failure would compute the uplift over only
	// the cases that succeeded — comparing treatment-of-all-N
	// against baseline-of-the-M-that-survived inflates apparent
	// uplift. The renderer surfaces the count; strict mode treats
	// it the same way it treats treatment-side errors.
	BaselineError string `json:"baseline_error,omitempty"`

	// Duration is the in-memory timing field used by the runner. Not
	// serialised; DurationMs is what's emitted to JSON.
	Duration time.Duration `json:"-"`
}

// MatchPair links a produced finding to the expected one it satisfied.
type MatchPair struct {
	Expected ExpectedFinding `json:"expected"`
	Produced ProducedFinding `json:"produced"`
}

// Precision = TP / (TP + FP). Returns 0 when no findings were produced.
func (s CaseScore) Precision() float64 {
	if s.TruePositives+s.FalsePositives == 0 {
		return 0
	}
	return float64(s.TruePositives) / float64(s.TruePositives+s.FalsePositives)
}

// Recall = TP / (TP + FN). Returns 1 when there were no expected
// findings (clean cases) — semantically: "the reviewer caught all of
// the (zero) bugs we expected." Noise on clean cases is captured
// separately in CaseReport.NoiseRate.
func (s CaseScore) Recall() float64 {
	if s.TruePositives+s.FalseNegatives == 0 {
		return 1
	}
	return float64(s.TruePositives) / float64(s.TruePositives+s.FalseNegatives)
}

// F1 = 2*P*R / (P+R). Returns 0 when both are zero.
func (s CaseScore) F1() float64 {
	p, r := s.Precision(), s.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// LanguageScore is the per-(LLM, language) aggregate. Phase 2 added
// per-language splits so prompt-pack changes can be evaluated against
// the language they target without being averaged out by the rest of
// the dataset (e.g. tightening the Go pack shouldn't show up as a
// regression on TypeScript cases).
type LanguageScore struct {
	Language  string  `json:"language"`
	Cases     int     `json:"cases"`     // total cases scored in this language (incl. clean)
	Precision float64 `json:"precision"` // micro-averaged across non-clean cases of this language
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	NoiseRate float64 `json:"noise_rate"` // mean findings per clean case of this language
}

// LLMReport aggregates per-case scores for one LLM across the dataset.
type LLMReport struct {
	LLM     string      `json:"llm"`
	Version string      `json:"version,omitempty"`
	Cases   []CaseScore `json:"cases"`

	// Aggregates across non-clean cases.
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`

	// Aggregate noise rate across clean cases: total spurious findings /
	// number of clean cases. Reported in "findings per clean diff" so
	// the number is interpretable without knowing the dataset size.
	NoiseRate float64 `json:"noise_rate"`

	// Per-language aggregates, sorted by Language for deterministic
	// output. Empty when the dataset has cases of only one language.
	Languages []LanguageScore `json:"languages,omitempty"`

	// Mean Jaccard similarity across cases when --repeat > 1 was used.
	// Pointer-typed so JSON consumers can distinguish "not measured"
	// (single-run bench → null) from "measured, zero overlap on every
	// case" (multi-run bench, model is totally inconsistent → 0.0).
	// Codex flagged the prior float-with-omitempty shape: a real 0.0
	// would silently disappear from output, hiding the worst case.
	Consistency *float64 `json:"consistency"`

	// Wall-clock totals.
	TotalDurationMs int64 `json:"total_duration_ms"`
	MedianMs        int64 `json:"median_ms"`
	P95Ms           int64 `json:"p95_ms"`

	// Baseline is the per-LLM aggregate from the --uplift baseline
	// pass — same cases, same LLM, generic system prompt instead
	// of local-review's pack pipeline. Pointer-typed so JSON
	// consumers can distinguish "no baseline measured" (nil) from
	// "baseline measured, every case errored" (the inner zeros).
	Baseline *LLMBaselineAggregate `json:"baseline,omitempty"`
}

// LLMBaselineAggregate is the per-LLM rollup of BaselineScore
// values, mirroring the same primary fields LLMReport carries for
// the treatment side. Used by the leaderboard to compute and show
// "baseline → treatment" deltas.
type LLMBaselineAggregate struct {
	Precision       float64 `json:"precision"`
	Recall          float64 `json:"recall"`
	F1              float64 `json:"f1"`
	NoiseRate       float64 `json:"noise_rate"`
	TotalDurationMs int64   `json:"total_duration_ms"`
}

// Report is the top-level bench output.
type Report struct {
	Dataset    string      `json:"dataset"`
	CaseCount  int         `json:"case_count"`
	Mode       string      `json:"mode"` // "cli", "replay", or "mixed"
	Generated  time.Time   `json:"generated"`
	LLMReports []LLMReport `json:"llms"`
}
