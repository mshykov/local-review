package bench

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
func readFixture(replayDir, caseID, llmName string) (string, error) {
	path := filepath.Join(replayDir, caseID, llmName+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read fixture %s: %w", path, err)
	}
	return string(b), nil
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
	return invoker.Review(cctx, pack, c.Diff)
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
	var tp, fp, fn int
	cleanCases := 0
	cleanFindings := 0
	nonCleanScored := 0

	for _, cs := range lr.Cases {
		if cs.Error != "" {
			continue
		}
		// Distinguish clean cases by "zero expected findings AND zero
		// matched/missed". A non-clean case where every expected was
		// found has FN==0 too, but it still has TP>0 — the union check
		// keeps the buckets disjoint.
		isClean := cs.TruePositives == 0 && cs.FalseNegatives == 0 && len(cs.Matched) == 0 && len(cs.Missed) == 0
		if isClean {
			cleanCases++
			cleanFindings += cs.Produced
			continue
		}
		nonCleanScored++
		tp += cs.TruePositives
		fp += cs.FalsePositives
		fn += cs.FalseNegatives
	}

	if nonCleanScored == 0 {
		// Leave Precision/Recall/F1 at their zero values. A consumer
		// can detect "no measurement" by checking that nonCleanScored
		// is reflected in the per-case array (every entry has Error
		// set, or the dataset truly has no non-clean cases).
		lr.Precision = 0
		lr.Recall = 0
		lr.F1 = 0
	} else {
		lr.Precision = ratio(tp, tp+fp)
		lr.Recall = ratio(tp, tp+fn)
		if lr.Precision+lr.Recall == 0 {
			lr.F1 = 0
		} else {
			lr.F1 = 2 * lr.Precision * lr.Recall / (lr.Precision + lr.Recall)
		}
	}
	if cleanCases > 0 {
		lr.NoiseRate = float64(cleanFindings) / float64(cleanCases)
	}

	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		var total time.Duration
		for _, d := range durations {
			total += d
		}
		lr.TotalDurationMs = total.Milliseconds()
		lr.MedianMs = durations[len(durations)/2].Milliseconds()
		// Nearest-rank p95: idx = ⌈0.95·n⌉ - 1 (then clamp). The
		// previous form `(n*95)/100` was a floor — for n=20 it picked
		// index 19 (the maximum), one off from the documented
		// "second-highest of 20" intent. Off-by-one only bites when
		// 0.95·n is an integer (n=20, 40, 60, …); on the small Ns the
		// bench actually runs (≤4 cases per LLM) both forms collapse
		// to the max, so the user-visible numbers don't change today.
		// Fixed for forward compatibility as the dataset grows.
		idx := int(math.Ceil(0.95*float64(len(durations)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(durations) {
			idx = len(durations) - 1
		}
		lr.P95Ms = durations[idx].Milliseconds()
	}
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
