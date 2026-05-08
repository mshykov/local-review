package bench

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/prompts"
)

// Source picks where review markdown for a (case, llm) pair comes
// from. Either a real CLI invocation or a pre-recorded fixture file.
type Source int

const (
	// SourceLive runs the LLM CLI against each case's diff. Slow
	// (seconds per case) and requires authentication.
	SourceLive Source = iota
	// SourceReplay reads a fixture from <replay-dir>/<case>/<llm>.md.
	// Used by CI and tests for deterministic, no-cost runs.
	SourceReplay
)

// Options configure one Run.
type Options struct {
	// LLMs is the set of agents to score. The runner does NOT detect
	// or filter — caller passes whatever it wants run. Empty = error.
	LLMs []cli.LLM

	// Source selects live-CLI vs replay-fixture mode.
	Source Source

	// ReplayDir is the root of the fixture tree. Required when
	// Source==SourceReplay; ignored otherwise.
	ReplayDir string

	// Timeout per case per LLM. Zero falls back to llm.TimeoutSec, then
	// to 120 seconds.
	Timeout time.Duration
}

// Run scores each case against each LLM and returns an aggregated
// Report. Per-case errors are recorded on the CaseScore and don't
// abort the run — one flaky agent shouldn't take down the rest.
//
// Returns an error only on caller-mistake conditions (no LLMs, replay
// mode with no replay dir).
func Run(ctx context.Context, cases []Case, opts Options) (Report, error) {
	if len(opts.LLMs) == 0 {
		return Report{}, errors.New("no LLMs to bench (pass --only or authenticate at least one CLI)")
	}
	if opts.Source == SourceReplay && opts.ReplayDir == "" {
		return Report{}, errors.New("replay mode requires a fixtures directory")
	}

	rep := Report{
		CaseCount: len(cases),
		Mode:      modeName(opts.Source),
		Generated: time.Now().UTC(),
	}

	for _, llm := range opts.LLMs {
		lr := LLMReport{LLM: llm.Name, Version: llm.Version}
		var durations []time.Duration

		for _, c := range cases {
			cs := scoreOne(ctx, c, llm, opts)
			lr.Cases = append(lr.Cases, cs)
			if cs.Error == "" {
				durations = append(durations, cs.Duration)
			}
		}

		fillAggregates(&lr, durations)
		rep.LLMReports = append(rep.LLMReports, lr)
	}

	sort.Slice(rep.LLMReports, func(i, j int) bool { return rep.LLMReports[i].LLM < rep.LLMReports[j].LLM })
	return rep, nil
}

// scoreOne runs a single (case, LLM) pair. Always returns a CaseScore
// — on error the score has Error set and zero TP/FP/FN.
func scoreOne(ctx context.Context, c Case, llm cli.LLM, opts Options) CaseScore {
	start := time.Now()
	mode := modeName(opts.Source)

	output, err := obtainReview(ctx, c, llm, opts)
	dur := time.Since(start)

	if err != nil {
		return CaseScore{
			CaseID:     c.ID,
			LLM:        llm.Name,
			Version:    llm.Version,
			Duration:   dur,
			DurationMs: dur.Milliseconds(),
			Error:      err.Error(),
			Mode:       mode,
		}
	}

	produced := ParseFindings(output)
	cs := Score(c, produced)
	cs.LLM = llm.Name
	cs.Version = llm.Version
	cs.Duration = dur
	cs.DurationMs = dur.Milliseconds()
	cs.Mode = mode
	return cs
}

// obtainReview returns the LLM's review markdown for one case, either
// by invoking the CLI or reading a fixture.
func obtainReview(ctx context.Context, c Case, llm cli.LLM, opts Options) (string, error) {
	switch opts.Source {
	case SourceReplay:
		return readFixture(opts.ReplayDir, c.ID, llm.Name)
	case SourceLive:
		return runLive(ctx, c, llm, opts.Timeout)
	default:
		return "", fmt.Errorf("unknown bench source %d", opts.Source)
	}
}

// readFixture reads a pre-recorded review from
// <replayDir>/<caseID>/<llmName>.md. Missing fixtures are an error
// rather than a silent skip — caller almost always wants to know
// "you forgot to record a fixture for the new case".
//
// Both caseID and llmName are validated against safeIdentifierRE
// before path construction. Without that guard a `--only "../etc"`
// or a malicious case directory name would let the bench read
// arbitrary files outside replayDir. Identifiers are required to be
// alphanumeric plus dash/underscore/dot — covers everything our
// real cases and LLM names use, refuses path separators and `..`.
func readFixture(replayDir, caseID, llmName string) (string, error) {
	if err := validateIdentifier("case id", caseID); err != nil {
		return "", err
	}
	if err := validateIdentifier("llm name", llmName); err != nil {
		return "", err
	}
	path := filepath.Join(replayDir, caseID, llmName+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read fixture %s: %w", path, err)
	}
	return string(b), nil
}

// safeIdentifierRE bounds what we accept as a case id or llm name
// for fixture lookups. Allows: A-Z, a-z, 0-9, dash, underscore, dot.
// Refuses: path separators, leading dot (rejects "."/".."), spaces,
// shell metacharacters. Conservative on purpose — every real case
// id and llm name in this project fits the restricted set.
var safeIdentifierRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

func validateIdentifier(kind, v string) error {
	if !safeIdentifierRE.MatchString(v) {
		return fmt.Errorf("invalid %s %q: must match [A-Za-z0-9._-] and not start with a dot", kind, v)
	}
	return nil
}

// runLive invokes the LLM CLI with the case's diff and returns the
// markdown output. Mirrors what internal/multi/orchestrator.go does
// for a single agent, minus the on-disk save (the bench writes its
// own report; per-case markdown is already persisted in the dataset).
func runLive(ctx context.Context, c Case, llm cli.LLM, timeout time.Duration) (string, error) {
	invoker := cli.NewInvoker(llm)
	if invoker == nil {
		return "", fmt.Errorf("no invoker for llm %q", llm.Name)
	}

	pack, err := prompts.Get(c.Language)
	if err != nil {
		return "", fmt.Errorf("load prompt pack for language %q: %w", c.Language, err)
	}

	if timeout == 0 {
		if llm.TimeoutSec > 0 {
			timeout = time.Duration(llm.TimeoutSec) * time.Second
		} else {
			timeout = 120 * time.Second
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// invoker.Review returns (output, tokenUsage, err) — bench
	// scoring works against the markdown only; token totals are
	// already tracked per-run in the live review path and the
	// bench-aggregate timing/cost view is Phase 2 ("Cost / latency
	// benchmark" in #56).
	out, _, err := invoker.Review(cctx, pack, c.Diff)
	return out, err
}

// fillAggregates computes precision/recall/F1/noise/timing over a
// freshly-populated LLMReport. Aggregation rules:
//
//   - Precision/recall/F1 are micro-averaged across non-clean cases:
//     sum TP, FP, FN over the dataset, then compute. This weights big
//     cases more than small ones, matching what users actually care
//     about ("did we catch most of the bugs across all diffs?").
//   - When no non-clean case scored successfully (every fixture
//     errored, or the dataset has only clean cases), all three drop
//     to 0. The previous shape returned Recall=1.0 in this case —
//     matching CaseScore.Recall's "no expected findings = perfect
//     recall" rule but actively misleading at the dataset level: a
//     run where every reviewer crashed would proudly report 100 %
//     recall and hide the regression.
//   - Noise rate is the mean number of findings produced per clean
//     case. Zero clean cases → 0.
//   - Median + p95 use only successful runs; an LLM that errors on
//     half the cases shouldn't have its timing skewed by the other
//     half's zero-duration error frames.
func fillAggregates(lr *LLMReport, durations []time.Duration) {
	tally := tallyCases(lr.Cases)
	lr.Precision, lr.Recall, lr.F1 = qualityScores(tally)
	if tally.cleanCases > 0 {
		lr.NoiseRate = float64(tally.cleanFindings) / float64(tally.cleanCases)
	}
	fillTimingAggregates(lr, durations)
}

// caseTally collects the running sums fillAggregates needs. Lives as
// its own type so the per-bucket logic doesn't have to share lexical
// scope with the unrelated timing pass.
type caseTally struct {
	tp, fp, fn     int
	cleanCases     int
	cleanFindings  int
	nonCleanScored int
}

// tallyCases bins each scored case into clean vs. non-clean and
// accumulates TP/FP/FN over the non-clean ones. Clean cases by
// definition have zero expected findings, so they don't contribute
// to precision/recall — only to the noise-rate denominator.
func tallyCases(cases []CaseScore) caseTally {
	var t caseTally
	for _, cs := range cases {
		if cs.Error != "" {
			continue
		}
		// Distinguish clean cases by "zero expected findings AND zero
		// matched/missed". A non-clean case where every expected was
		// found has FN==0 too, but it still has TP>0 — the union check
		// keeps the buckets disjoint.
		isClean := cs.TruePositives == 0 && cs.FalseNegatives == 0 && len(cs.Matched) == 0 && len(cs.Missed) == 0
		if isClean {
			t.cleanCases++
			t.cleanFindings += cs.Produced
			continue
		}
		t.nonCleanScored++
		t.tp += cs.TruePositives
		t.fp += cs.FalsePositives
		t.fn += cs.FalseNegatives
	}
	return t
}

// qualityScores returns (precision, recall, F1). When no non-clean
// case scored successfully — every fixture errored, or the dataset
// has only clean cases — all three drop to 0 instead of the
// CaseScore-level "Recall=1 when no expected findings" rule, which
// at the dataset level would let a reviewer that crashed on every
// real case proudly report 100 % recall.
func qualityScores(t caseTally) (precision, recall, f1 float64) {
	if t.nonCleanScored == 0 {
		return 0, 0, 0
	}
	precision = ratio(t.tp, t.tp+t.fp)
	recall = ratio(t.tp, t.tp+t.fn)
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return precision, recall, f1
}

// fillTimingAggregates computes total / median / p95 wall-clock from
// the durations of successful runs. The slice is sorted in place;
// caller's view is mutated, which is fine because the only caller
// drops the slice immediately after.
func fillTimingAggregates(lr *LLMReport, durations []time.Duration) {
	if len(durations) == 0 {
		return
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	lr.TotalDurationMs = total.Milliseconds()
	lr.MedianMs = durations[len(durations)/2].Milliseconds()
	lr.P95Ms = durations[p95Index(len(durations))].Milliseconds()
}

// p95Index returns the slice index for the 95th-percentile sample
// using nearest-rank: idx = ⌈0.95·n⌉ - 1, clamped to [0, n-1]. The
// previous form `(n*95)/100` was a floor — for n=20 it picked index
// 19 (the maximum), one off from the documented "second-highest of
// 20" intent. Off-by-one only bites when 0.95·n is an integer
// (n=20, 40, 60, …); on the small Ns the bench actually runs
// (≤4 cases per LLM) both forms collapse to the max, so the
// user-visible numbers don't change today. Fixed for forward
// compatibility as the dataset grows.
func p95Index(n int) int {
	idx := int(math.Ceil(0.95*float64(n))) - 1
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func modeName(s Source) string {
	if s == SourceReplay {
		return "replay"
	}
	return "cli"
}
