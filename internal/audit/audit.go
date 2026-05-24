// Package audit implements the `local-review audit` subcommand —
// the deep-analysis mode introduced in v0.10.0-c.
//
// Unlike the `review` subcommand which inspects a git diff, `audit`
// walks the whole committed source tree, groups files by directory,
// and runs each directory's source through the LLM with a topic-
// specific system prompt (security, tech-debt, …). The output is a
// markdown report listing findings per package — accumulated debt /
// vulnerabilities / smells that no individual diff would have surfaced.
//
// Design constraints (from the v0.10.0 planning discussion):
//   - Topic-driven, not open-ended ("find all bugs" produces shallow
//     LLM output; "find security gaps" produces actionable output).
//   - Chunked by directory so the LLM gets a coherent unit of scope,
//     and so a single 100k-LOC monorepo doesn't blow context windows.
//   - Single-LLM by default ("audit" cost is per-package × per-topic,
//     multiplying by 3 LLMs would put a real-codebase audit in $20+
//     territory). Multi-LLM is a later enhancement once we see real
//     usage patterns.
//   - Cost transparency: pre-flight `--dry-run` shows the plan without
//     invoking; the runner prints a per-chunk progress line.
package audit

import "time"

// Finding is one issue surfaced by the audit. Mirrors the review
// path's finding shape so consumers that already render review JSON
// can reuse most of the pipeline.
type Finding struct {
	// Path is repo-relative; the audit always uses the path the LLM
	// returned, falling back to the chunk path when the LLM elided
	// it. May be empty for findings that span the whole package.
	Path string `json:"path,omitempty"`

	// Line is the (start) line number the LLM cited (best-effort;
	// LLMs vary in line-accuracy across audit mode, where they're
	// reading more context than in diff mode). Zero = unlocated.
	Line int `json:"line,omitempty"`

	// LineEnd is the end line of a `LINE-RANGE` citation
	// (`file.go:12-18`). Zero when the LLM gave a single line
	// (the common case) or when the line was elided entirely.
	// Audit packs document the range shape; the v1 renderer
	// surfaces it inline as "file:start-end" when present.
	LineEnd int `json:"line_end,omitempty"`

	// Severity is one of "critical", "major", "warning", "info".
	// Audit packs deliberately skip "nit" — whole-codebase reading
	// produces enough signal that nits dilute the report.
	Severity string `json:"severity"`

	// Body is the finding text the LLM produced, lightly cleaned up
	// (trimmed, severity prefix stripped). Renderer formats it.
	Body string `json:"body"`
}

// PackageReport is the per-package output: one chunk, one LLM
// invocation, one set of findings. Empty Findings + Raw == ""
// means the LLM returned the "[clean] no findings in this package"
// sentinel; the renderer collapses these into a single tail line.
type PackageReport struct {
	// Package is the repo-relative directory path. Top-level files
	// (e.g. main.go in repo root) live under "." by convention.
	Package string `json:"package"`

	// Files lists the repo-relative paths included in this chunk.
	// Used by the renderer to show "audited N files in pkg/" lines
	// and by the runner to detect "package too big to chunk."
	Files []string `json:"files"`

	// Findings is the parsed list. Empty when the LLM returned
	// clean OR when the LLM errored (Error captures the failure).
	Findings []Finding `json:"findings,omitempty"`

	// Clean is true when the LLM explicitly produced a "[clean]"
	// sentinel. Distinguishes "audited and found nothing" from
	// "audited and the parse missed everything."
	Clean bool `json:"clean,omitempty"`

	// Raw is the LLM's untrimmed response. Kept on the report so a
	// reviewer with a parser failure can see what the LLM actually
	// said. Renderer doesn't surface it by default.
	Raw string `json:"raw,omitempty"`

	// Timing + status.
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`

	// InputTokens / OutputTokens carry the same semantics as the
	// review path's cli.TokenUsage (zero = unknown, not zero-spent).
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Report is the top-level audit output, suitable for JSON
// serialization and for the text/markdown renderers in report.go.
type Report struct {
	Topic     string          `json:"topic"`
	Generated time.Time       `json:"generated"`
	Root      string          `json:"root"`
	LLM       string          `json:"llm"`
	Version   string          `json:"version,omitempty"`
	Packages  []PackageReport `json:"packages"`

	// Aggregate counts, summed across packages. The renderer uses
	// these for the summary line; consumers can re-derive from
	// Packages but the counts on the top level cost nothing to
	// emit.
	TotalFindings        int            `json:"total_findings"`
	FindingsBySeverity   map[string]int `json:"findings_by_severity,omitempty"`
	PackagesWithFindings int            `json:"packages_with_findings"`
	PackagesClean        int            `json:"packages_clean"`
	PackagesErrored      int            `json:"packages_errored,omitempty"`

	// Total token usage across all packages.
	TotalInputTokens  int `json:"total_input_tokens,omitempty"`
	TotalOutputTokens int `json:"total_output_tokens,omitempty"`
}
