// SWE-bench-lite adapter for the local-review bench harness.
//
// Loads a curated subset of SWE-bench-lite tasks (or synthetic
// SWE-bench-shaped examples), runs the existing multi-LLM review
// path against each task's bug-introducing diff, and scores via
// case-insensitive keyword match between the LLM's findings and
// the task's expected_keywords. Output is rendered as a "SWE-bench
// catch rate" section in `bench/RESULTS.md`.
//
// v1 scope (v0.10.0):
//   - Binary caught / missed scoring (no partial credit tier).
//   - Substring match on the raw LLM review markdown.
//   - Live mode + replay mode (same shape as the existing bench).
//   - No Ollama row yet (planned for v0.11.0 alongside auto-fix).
//
// See bench/swe-bench-lite/README.md for the on-disk format and
// the user-facing methodology.
package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/cli"

	"gopkg.in/yaml.v3"
)

// SWEBenchCase is one task in the SWE-bench-lite-flavoured dataset.
// Loaded from bench/swe-bench-lite/<id>/case.yaml plus the sibling
// diff.patch file. Mirrors the layout of bench.Case but uses keyword-
// match scoring instead of (file, line) tuple matching.
type SWEBenchCase struct {
	// Stable identifier; matches the directory name on disk and the
	// fixture lookup key in --replay mode.
	ID string `yaml:"id"`

	// Optional upstream reference — e.g. the original
	// princeton-nlp/SWE-bench-lite task id. Recorded for
	// traceability; not used by the scorer.
	Source string `yaml:"source,omitempty"`

	// Language id ("python", "javascript", …). Drives prompt-pack
	// selection just like bench.Case. SWE-bench-lite is Python-only
	// upstream, but the format accepts any language id so curated
	// tasks for other stacks can land in the same dataset later.
	Language string `yaml:"language,omitempty"`

	// Free-form one-line title for human-readable reports.
	Title string `yaml:"title"`

	// Optional longer description.
	Description string `yaml:"description,omitempty"`

	// ExpectedKeywords is the substring-match dictionary the scorer
	// uses to decide "caught" vs "missed." A finding that contains
	// ANY of these phrases (case-insensitive) counts the task as
	// caught. At least one keyword is required; the loader rejects
	// tasks without any.
	ExpectedKeywords []string `yaml:"expected_keywords"`

	// ExpectedFiles is reserved for future partial-credit scoring
	// (LLM-as-judge tier planned for a later release). The v1 keyword
	// scorer doesn't consult it; the field is parsed and surfaced in
	// JSON output for tooling that wants to render the expected
	// location alongside the catch / miss verdict.
	ExpectedFiles []string `yaml:"expected_files,omitempty"`

	// DiffPath is the resolved on-disk path of the diff body. Populated
	// by LoadSWEBenchDataset; not part of the YAML schema.
	DiffPath string `yaml:"-"`

	// Diff is the raw diff text fed to the reviewer LLM. Populated by
	// LoadSWEBenchDataset.
	Diff string `yaml:"-"`
}

// SWEBenchScore is the per-(task, LLM) outcome.
type SWEBenchScore struct {
	CaseID  string `json:"case_id"`
	LLM     string `json:"llm"`
	Version string `json:"version,omitempty"`
	Mode    string `json:"mode"` // "cli" or "replay"

	// Caught is true when the LLM's review markdown contained at
	// least one of the task's expected_keywords (case-insensitive
	// substring). v1 is binary; no partial-credit tier.
	Caught bool `json:"caught"`

	// MatchedKeywords lists which keywords actually fired. Helpful
	// for triaging false-positive matches (e.g., a keyword too
	// generic) and for showing readers WHY the task was scored
	// caught.
	MatchedKeywords []string `json:"matched_keywords,omitempty"`

	// Timing + status.
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`

	// Duration is the in-memory timing field used by the runner.
	// Not serialised; DurationMs is what's emitted to JSON.
	Duration time.Duration `json:"-"`
}

// SWEBenchLLMReport aggregates per-task scores for one LLM across
// the SWE-bench dataset. Catch rate is the headline number.
type SWEBenchLLMReport struct {
	LLM     string          `json:"llm"`
	Version string          `json:"version,omitempty"`
	Cases   []SWEBenchScore `json:"cases"`

	// Tasks counts the cases scored (including errors); CaughtCount
	// counts the subset where Caught == true. CatchRate is
	// CaughtCount / Tasks, or 0 when Tasks == 0.
	Tasks       int     `json:"tasks"`
	CaughtCount int     `json:"caught"`
	CatchRate   float64 `json:"catch_rate"`

	// Errors counts cases where the LLM invocation itself failed
	// (timeout, exit non-zero, fixture missing). Error frames count
	// against CatchRate's denominator — a reviewer that crashes
	// catches no bugs — but Errors is surfaced separately so readers
	// can tell "reviewer ran but missed" from "reviewer never ran."
	Errors int `json:"errors,omitempty"`
}

// SWEBenchReport is the top-level SWE-bench output. Parallel to
// bench.Report but for the keyword-match scoring path.
type SWEBenchReport struct {
	Dataset    string              `json:"dataset"`
	CaseCount  int                 `json:"case_count"`
	Mode       string              `json:"mode"`
	Generated  time.Time           `json:"generated"`
	LLMReports []SWEBenchLLMReport `json:"llms"`
}

// LoadSWEBenchDataset walks rootDir for `<task-id>/case.yaml` +
// `<task-id>/diff.patch` pairs and returns the parsed cases sorted
// by id. Behaviour mirrors LoadDataset's contract — both for caller
// ergonomics and so the bench harness has one consistent
// dataset-loading model across modes:
//
//   - A task directory missing either file is skipped silently
//     ("partial entry"). The dir-walk produced its name once, then
//     curation never finished — not fatal.
//   - Any OTHER read error on those files (permission denied,
//     filesystem corruption) fails the whole load. Hiding I/O
//     failures behind a silent skip would let a partially-readable
//     dataset masquerade as a fully-loaded one.
//   - Duplicate case ids fail loudly. A duplicate id collides on
//     fixture lookup (<replay>/<id>/<llm>.md) and on per-task
//     indexing in the report — both produce silently-wrong
//     numbers, so refuse before they happen.
//   - Empty result (no tasks loaded) fails loudly. A wrong
//     `--swe-bench-dataset` path or an entire directory of
//     partial entries would otherwise yield a zero-row report
//     that looks like a clean reviewer wipe.
//
// Per-case validation lives in loadSWEBenchCase: trimmed id+title
// required, at least one non-empty expected_keyword required.
func LoadSWEBenchDataset(rootDir string) ([]SWEBenchCase, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("read swe-bench dataset root %s: %w", rootDir, err)
	}
	var cases []SWEBenchCase
	idToDir := make(map[string]string)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(rootDir, e.Name())
		c, err := loadSWEBenchCase(dir)
		if err != nil {
			return nil, fmt.Errorf("load swe-bench case %s: %w", e.Name(), err)
		}
		if c == nil {
			continue // partial entry; skipped
		}
		if prev, dup := idToDir[c.ID]; dup {
			return nil, fmt.Errorf("duplicate swe-bench case id %q in %q and %q (each case directory must have a unique id)", c.ID, prev, dir)
		}
		idToDir[c.ID] = dir
		cases = append(cases, *c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no swe-bench cases found under %q (each case is a subdirectory with case.yaml + diff.patch)", rootDir)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

// loadSWEBenchCase parses one task directory.
//
// Returns (nil, nil) when EITHER case.yaml OR diff.patch is missing
// from the directory (os.ErrNotExist) — that's a partial / half-
// curated entry and the LoadSWEBenchDataset contract is to skip
// those silently rather than fail the whole bench.
//
// Returns (nil, err) on any other read error (permission denied,
// I/O failure, etc.) — those are real dataset corruption and
// would silently shrink the load to whatever did read cleanly if
// swallowed. Same fail-loud invariant CLAUDE.md rule 4 calls out.
//
// Returns (nil, err) on malformed YAML, missing required fields
// (id, title — both TrimSpace-checked), or an empty / all-
// whitespace expected_keywords list (an empty list would silently
// mark every task missed and obscure the curation gap).
func loadSWEBenchCase(dir string) (*SWEBenchCase, error) {
	yamlPath := filepath.Join(dir, "case.yaml")
	diffPath := filepath.Join(dir, "diff.patch")
	yb, yerr := os.ReadFile(yamlPath)
	db, derr := os.ReadFile(diffPath)
	if yerr != nil || derr != nil {
		// Partial entry: skip ONLY when the file is missing
		// (os.ErrNotExist). Any other error — permission denied,
		// filesystem hiccup — is real corruption and must surface,
		// or the bench would silently report on a partial load.
		if errors.Is(yerr, os.ErrNotExist) || errors.Is(derr, os.ErrNotExist) {
			return nil, nil
		}
		if yerr != nil {
			return nil, fmt.Errorf("read case.yaml: %w", yerr)
		}
		return nil, fmt.Errorf("read diff.patch: %w", derr)
	}
	var c SWEBenchCase
	if err := yaml.Unmarshal(yb, &c); err != nil {
		return nil, fmt.Errorf("parse case.yaml: %w", err)
	}
	// TrimSpace before required-field checks so a YAML value of
	// `id: "   "` doesn't sneak past validation and produce a
	// task whose fixture lookup, dedup, and reporting indexing
	// all silently break in different ways. CLAUDE.md rule 4
	// ("Fail loud, fail closed — use TrimSpace for emptiness
	// checks").
	c.ID = strings.TrimSpace(c.ID)
	c.Title = strings.TrimSpace(c.Title)
	if c.ID == "" {
		return nil, fmt.Errorf("case.yaml missing required field: id")
	}
	if c.Title == "" {
		return nil, fmt.Errorf("case.yaml missing required field: title")
	}
	// Trim and validate keywords: empty list (or list of only-empties)
	// would silently mark every task missed; loud failure is right.
	trimmed := make([]string, 0, len(c.ExpectedKeywords))
	for _, k := range c.ExpectedKeywords {
		k = strings.TrimSpace(k)
		if k != "" {
			trimmed = append(trimmed, k)
		}
	}
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("case.yaml requires at least one non-empty expected_keywords entry")
	}
	c.ExpectedKeywords = trimmed
	c.DiffPath = diffPath
	c.Diff = string(db)
	return &c, nil
}

// ScoreSWE returns the per-task verdict by case-insensitive
// substring matching the LLM's raw review markdown against each of
// the task's expected_keywords. Caught == true when at least one
// keyword fires; MatchedKeywords records which ones did, in the
// order they appear in case.yaml (deterministic for review diffs).
//
// Pure function — no I/O, no LLM calls — so it can be exercised
// from tests with synthetic LLM output.
func ScoreSWE(c SWEBenchCase, llmOutput string) SWEBenchScore {
	lower := strings.ToLower(llmOutput)
	var matched []string
	for _, k := range c.ExpectedKeywords {
		if strings.Contains(lower, strings.ToLower(k)) {
			matched = append(matched, k)
		}
	}
	return SWEBenchScore{
		CaseID:          c.ID,
		Caught:          len(matched) > 0,
		MatchedKeywords: matched,
	}
}

// RunSWE scores each (task, LLM) pair and returns an aggregated
// SWEBenchReport. Per-case errors (LLM crash, missing fixture)
// land on the SWEBenchScore.Error field and count toward
// SWEBenchLLMReport.Errors. They count against the catch rate's
// denominator deliberately — a reviewer that crashes catches no
// bugs, and silently dropping error frames would inflate the
// surviving subset's apparent catch rate.
//
// Mirrors the existing bench.Run() control flow: per-LLM loop, per-
// case obtainReview call, fillAggregate at the end. Live mode hits
// the real LLM CLIs; replay mode reads `<replay>/<task>/<llm>.md`
// fixtures — same path-traversal guard as the bench replay loader.
func RunSWE(ctx context.Context, cases []SWEBenchCase, opts Options) (SWEBenchReport, error) {
	if len(opts.LLMs) == 0 {
		return SWEBenchReport{}, fmt.Errorf("no LLMs to bench (pass --only or authenticate at least one CLI)")
	}
	if opts.Source == SourceReplay && opts.ReplayDir == "" {
		return SWEBenchReport{}, fmt.Errorf("replay mode requires a fixtures directory")
	}
	// SWE-bench mode rejects --uplift and --repeat for v1: neither
	// concept is meaningful here. --uplift compares treatment vs
	// baseline prompts but SWE-bench scoring asks one binary
	// question; baseline catches don't combine with treatment
	// catches in a way that means anything. --repeat measures
	// consistency on the same case; an LLM that catches the bug
	// once and misses it once is a stability concern worth
	// surfacing, but the catch-rate aggregate would have to choose
	// "caught if any run caught" or "caught if all runs caught"
	// and either choice is contentious enough to defer.
	if opts.Uplift {
		return SWEBenchReport{}, fmt.Errorf("--uplift is not meaningful in --swe-bench mode (binary catch/miss scoring); drop --uplift")
	}
	if opts.Repeat > 1 {
		return SWEBenchReport{}, fmt.Errorf("--repeat > 1 is not yet supported in --swe-bench mode (the consistency aggregation semantics for binary catch scoring — 'caught-if-any-run-caught' vs 'caught-if-all-runs-caught' — are contentious enough to defer until we see real usage); drop --repeat or run separate single-pass invocations")
	}

	rep := SWEBenchReport{
		CaseCount: len(cases),
		Mode:      modeName(opts.Source),
		Generated: time.Now().UTC(),
	}

	for _, llm := range opts.LLMs {
		lr := SWEBenchLLMReport{LLM: llm.Name, Version: llm.Version}
		for _, c := range cases {
			cs := scoreOneSWE(ctx, c, llm, opts)
			lr.Cases = append(lr.Cases, cs)
		}
		fillSWEAggregates(&lr)
		rep.LLMReports = append(rep.LLMReports, lr)
	}
	sort.Slice(rep.LLMReports, func(i, j int) bool { return rep.LLMReports[i].LLM < rep.LLMReports[j].LLM })
	return rep, nil
}

// scoreOneSWE invokes the LLM for one task and applies ScoreSWE.
// On error, returns an error-frame score (Caught=false, Error set,
// no MatchedKeywords) so the LLM-level aggregate counts the case
// against the denominator without inventing a phantom catch.
func scoreOneSWE(ctx context.Context, c SWEBenchCase, llm cli.LLM, opts Options) SWEBenchScore {
	start := time.Now()
	mode := modeName(opts.Source)

	output, _, err := obtainSWEReview(ctx, c, llm, opts)
	dur := time.Since(start)

	if err != nil {
		return SWEBenchScore{
			CaseID:     c.ID,
			LLM:        llm.Name,
			Version:    llm.Version,
			Mode:       mode,
			Caught:     false,
			Error:      err.Error(),
			Duration:   dur,
			DurationMs: dur.Milliseconds(),
		}
	}
	s := ScoreSWE(c, output)
	s.LLM = llm.Name
	s.Version = llm.Version
	s.Mode = mode
	s.Duration = dur
	s.DurationMs = dur.Milliseconds()
	return s
}

// obtainSWEReview returns the LLM's review markdown for one
// SWE-bench task. Adapter onto the existing live/replay
// infrastructure: bench-internal Case construction lets us reuse
// runLive and readFixture verbatim.
func obtainSWEReview(ctx context.Context, c SWEBenchCase, llm cli.LLM, opts Options) (string, cli.TokenUsage, error) {
	// Bridge the SWE-bench case into the runLive contract by
	// constructing a minimal bench.Case with the same diff +
	// language fields. The treatment prompt pack selection looks
	// only at Language and Diff, so the bridge is faithful.
	bridged := Case{
		ID:       c.ID,
		Language: c.Language,
		Diff:     c.Diff,
		DiffPath: c.DiffPath,
	}
	switch opts.Source {
	case SourceReplay:
		out, err := readFixture(opts.ReplayDir, c.ID, llm.Name)
		return out, cli.TokenUsage{}, err
	case SourceLive:
		return runLive(ctx, bridged, llm, opts.Timeout)
	default:
		return "", cli.TokenUsage{}, fmt.Errorf("unknown bench source %d", opts.Source)
	}
}

// fillSWEAggregates computes Tasks, CaughtCount, CatchRate, and
// Errors on lr from lr.Cases. Catch rate uses Tasks (NOT Tasks-Errors)
// as the denominator deliberately: a reviewer that crashes catches
// no bugs, and silently shrinking the denominator to the surviving
// subset would inflate the apparent catch rate exactly when
// reviewers are flakiest.
func fillSWEAggregates(lr *SWEBenchLLMReport) {
	lr.Tasks = len(lr.Cases)
	for _, cs := range lr.Cases {
		if cs.Error != "" {
			lr.Errors++
			continue
		}
		if cs.Caught {
			lr.CaughtCount++
		}
	}
	if lr.Tasks > 0 {
		lr.CatchRate = float64(lr.CaughtCount) / float64(lr.Tasks)
	}
}
