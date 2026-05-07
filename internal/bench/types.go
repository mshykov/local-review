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
// Matching is intentionally loose — see scorer.go for the exact rules —
// because LLM line numbers wander by ±a few lines on the same diff.
type ExpectedFinding struct {
	// File the bug lives in (matched by suffix, so "foo.go" matches
	// "src/foo.go" — LLMs vary on whether they emit relative or full
	// paths).
	File string `yaml:"file"`

	// Line number of the bug. Matching uses Window (default 3) on each
	// side; a produced finding within [Line-Window, Line+Window] counts.
	Line int `yaml:"line"`

	// Window in lines around Line that still counts as a match. Zero
	// means "use the global default" (3). Per-finding override exists
	// because some bugs have a clear single-line locus (nil deref) and
	// others span a small block (race window, error-handling section).
	Window int `yaml:"window,omitempty"`

	// Optional category hint: "security", "correctness", "performance",
	// "style". Recorded but NOT required for a match in v1 — the
	// markdown produced by LLM CLIs doesn't reliably preserve category
	// labels, and forcing a match would inflate false negatives.
	Category string `yaml:"category,omitempty"`

	// Optional severity hint: "critical", "major", "warning", "info",
	// "nit". Recorded but not required for a match in v1; LLMs grade
	// severity inconsistently across runs.
	Severity string `yaml:"severity,omitempty"`

	// Human-readable note about what the finding should call out. Shown
	// in reports next to "MISSED" lines so a maintainer reviewing a
	// regression can tell at a glance which bug the reviewer dropped.
	Note string `yaml:"note,omitempty"`
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

// CaseScore is the per-case scoring outcome.
type CaseScore struct {
	CaseID  string `json:"case_id"`
	LLM     string `json:"llm"`
	Version string `json:"version,omitempty"`

	// Counts that drive precision/recall.
	TruePositives  int `json:"true_positives"`
	FalsePositives int `json:"false_positives"`
	FalseNegatives int `json:"false_negatives"`

	// Total findings produced (TP + FP) for noise-rate calculation.
	Produced int `json:"produced"`

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

	// Wall-clock totals.
	TotalDurationMs int64 `json:"total_duration_ms"`
	MedianMs        int64 `json:"median_ms"`
	P95Ms           int64 `json:"p95_ms"`
}

// Report is the top-level bench output.
type Report struct {
	Dataset    string      `json:"dataset"`
	CaseCount  int         `json:"case_count"`
	Mode       string      `json:"mode"` // "cli", "replay", or "mixed"
	Generated  time.Time   `json:"generated"`
	LLMReports []LLMReport `json:"llms"`
}
