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

	// Repeat is the number of times each (case, LLM) pair is sampled
	// to compute Jaccard consistency. Zero or one means "no repeat";
	// the bench runs once and CaseScore.Jaccard stays nil (not
	// measured — absent from JSON output). Values > 1 are only
	// meaningful for SourceLive — replay fixtures are deterministic
	// and the Phase-2 consistency-runs check would always score 1.0,
	// so Run errors out for replay+Repeat>1 rather than producing a
	// meaningless number.
	//
	// The first run's findings still drive precision/recall/F1; the
	// extra runs only feed the Jaccard calculation. This keeps the
	// quality scores comparable to single-run benches and avoids
	// "did variance cause that regression?" ambiguity.
	Repeat int

	// Uplift, when true, runs each (case, LLM) pair TWICE: once
	// with prompts.BaselinePrompt (a minimal generic system prompt
	// that mirrors what a developer would type into Claude.app
	// without specialised tooling), and once with the full
	// local-review pipeline (language-specific pack via
	// prompts.Resolve). The treatment scores fill the primary
	// CaseScore fields; the baseline scores fill CaseScore.Baseline
	// (and the LLM-level rollup fills LLMReport.Baseline). The
	// leaderboard renders both plus the delta.
	//
	// Live-only by design: replay mode rejects --uplift outright.
	// The whole point of uplift is to measure real LLM behaviour
	// against the bare-prompt baseline; running cached fixtures
	// for both sides would just measure how well the fixtures
	// match each other, which is meaningless. Run rejects the
	// combination loudly rather than producing a misleading
	// number.
	Uplift bool
}

// Run scores each case against each LLM and returns an aggregated
// Report. Per-case errors are recorded on the CaseScore and don't
// abort the run — one flaky agent shouldn't take down the rest.
//
// Returns an error only on caller-mistake conditions (no LLMs, replay
// mode with no replay dir, --repeat > 1 in replay mode).
func Run(ctx context.Context, cases []Case, opts Options) (Report, error) {
	if len(opts.LLMs) == 0 {
		return Report{}, errors.New("no LLMs to bench (pass --only or authenticate at least one CLI)")
	}
	if opts.Source == SourceReplay && opts.ReplayDir == "" {
		return Report{}, errors.New("replay mode requires a fixtures directory")
	}
	if opts.Source == SourceReplay && opts.Repeat > 1 {
		return Report{}, errors.New("--repeat > 1 is meaningless in replay mode (fixtures are deterministic; consistency would always score 1.0); re-run live, or drop --repeat")
	}
	if opts.Source == SourceReplay && opts.Uplift {
		return Report{}, errors.New("--uplift is meaningless in replay mode (need live LLM calls to measure the baseline); re-run live, or drop --uplift")
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
//
// When opts.Repeat > 1, the (case, LLM) pair is sampled repeat times.
// The first sample drives precision/recall/F1 (so quality scores stay
// comparable to single-run benches), and the full run set feeds the
// Jaccard consistency calculation. If the first sample errors, the
// whole call short-circuits as an error — there's nothing to compare
// against.
//
// Duration is measured across the entire scoring pass, including
// the extra repeats. Codex flagged the prior shape (capture before
// sampleConsistency) as understating wall-clock for --repeat runs:
// with --repeat 5, only run 1's duration was reported, hiding 80%
// of real spend. Median/P95 in the per-LLM aggregate now reflect
// total per-case wall-clock, which is what a user comparing latency
// between runs actually wants.
func scoreOne(ctx context.Context, c Case, llm cli.LLM, opts Options) CaseScore {
	start := time.Now()
	mode := modeName(opts.Source)

	// Treatment-pass wall-clock captured separately from the full
	// scoreOne span so the "Overhead vs raw model" leaderboard
	// (v0.9.0) gets an apples-to-apples comparison against the
	// baseline pass's single-call duration. cs.DurationMs further
	// down still measures the full per-case spend (treatment +
	// repeats + baseline) for the Overall median/p95 view.
	treatmentStart := time.Now()
	output, usage, err := obtainReview(ctx, c, llm, opts)
	treatmentDur := time.Since(treatmentStart)

	cs := buildBaseScore(c, llm, mode, output, err)
	if err == nil {
		cs.InputTokens = usage.InputTokens
		cs.OutputTokens = usage.OutputTokens
		cs.TreatmentDurationMs = treatmentDur.Milliseconds()
	}

	// Phase-2 consistency: re-sample the same (case, LLM) up to
	// opts.Repeat times and record the Jaccard similarity. Each
	// extra sample is independent — if one errors we still report
	// Jaccard against whichever samples succeeded (down to a
	// minimum of 2 samples). Both the attempt count and the
	// successful-run count are recorded so a "Jaccard 1.0 but 3/5
	// runs failed" outcome stays interpretable instead of falsely
	// implying every attempt agreed — codex caught this credibility
	// hole in self-review. JSON consumers should treat any case
	// with attempts > run_count with skepticism.
	//
	// Skipped on treatment-error frames: there's no first run to
	// compare against, so consistency is undefined.
	if opts.Repeat > 1 && err == nil {
		produced := ParseFindings(output)
		attempts, count, jac := sampleConsistency(ctx, c, llm, opts, produced)
		cs.Attempts = attempts
		cs.RunCount = count
		if count >= 2 {
			cs.Jaccard = &jac
		}
	}

	// Phase-3 uplift: run the same case again with a minimal
	// generic system prompt (prompts.BaselinePrompt) and record
	// the resulting score. The bench then renders treatment vs
	// baseline so users see whether local-review's pack pipeline
	// actually adds value over a raw-LLM baseline. Live-only:
	// replay rejects --uplift up in Run().
	//
	// Baseline runs even when the treatment pass errored — the
	// uplift comparison should not be biased toward "treatment
	// happened to succeed here". Iter-4 self-review (codex)
	// flagged the prior shape (early-return on treatment error
	// before scoreBaseline) as silently shrinking the baseline
	// sample to the treatment-success subset, which inflates
	// apparent uplift exactly when local-review is flakiest.
	//
	// Baseline errors are recorded explicitly on cs.BaselineError
	// rather than silently dropped: a half-measured baseline
	// (treatment for all N cases, baseline for only M < N) would
	// inflate apparent uplift by comparing the full treatment
	// against an unrepresentative baseline subset. Renderer
	// surfaces the gap; strict mode treats it the same way it
	// treats treatment-side errors.
	if opts.Uplift {
		b, berr := scoreBaseline(ctx, c, llm, opts)
		if berr != nil {
			cs.BaselineError = berr.Error()
		} else if b != nil {
			cs.Baseline = b
		}
	}

	dur := time.Since(start)
	cs.Duration = dur
	cs.DurationMs = dur.Milliseconds()
	return cs
}

// buildBaseScore returns a CaseScore for one (case, LLM) pair.
// On treatment success it scores the produced findings; on
// treatment error it returns the error-frame shape (zero TP/FP/
// FN, Error set) but still carries the metadata fields so
// downstream rollups (Language, Mode, etc.) see the same key on
// every row regardless of treatment outcome. Pulled out of
// scoreOne so the consistency / uplift extension points stay
// readable instead of nested inside an err == nil branch.
func buildBaseScore(c Case, llm cli.LLM, mode, output string, err error) CaseScore {
	if err != nil {
		return CaseScore{
			CaseID:   c.ID,
			LLM:      llm.Name,
			Version:  llm.Version,
			Language: c.Language,
			// Carry Clean even on treatment errors so baseline-
			// only frames still classify correctly into the
			// noise vs. precision/recall buckets at aggregation
			// time. Without this, a treatment-error case on a
			// clean diff would land as "non-clean" in the
			// baseline rollup and skew the metrics.
			Clean: c.Clean || len(c.Expected) == 0,
			Error: err.Error(),
			Mode:  mode,
		}
	}
	produced := ParseFindings(output)
	cs := Score(c, produced)
	cs.LLM = llm.Name
	cs.Version = llm.Version
	cs.Language = c.Language
	cs.Mode = mode
	return cs
}

// sampleConsistency runs the same (case, LLM) opts.Repeat - 1
// additional times and returns (attempts, successfulRuns, jaccard)
// where jaccard is computed across all successful runs (including
// the caller-supplied first one).
//
// Errors on extra samples are tolerated: we tally attempts and
// successes separately and compute Jaccard over what landed. The
// gap (attempts > successfulRuns) is surfaced via CaseScore.Attempts
// so a high Jaccard built from few survivors doesn't get committed
// to a leaderboard as proof of stability.
//
// Returns (opts.Repeat, len(runs), 0) when no extra samples
// succeeded; the caller will not set cs.Jaccard in that case.
// Consumers should treat run_count <= 1 (or a nil Jaccard) as
// "consistency not measured."
func sampleConsistency(ctx context.Context, c Case, llm cli.LLM, opts Options, firstRun []ProducedFinding) (attempts, successful int, jac float64) {
	attempts = opts.Repeat
	runs := [][]ProducedFinding{firstRun}
	for i := 1; i < opts.Repeat; i++ {
		// Consistency cares about the produced finding sets only;
		// token usage from the extra repeats isn't summed into the
		// uplift aggregates (those measure the primary treatment
		// pass per case, mirroring the baseline pass's single call).
		out, _, err := obtainReview(ctx, c, llm, opts)
		if err != nil {
			continue
		}
		runs = append(runs, ParseFindings(out))
	}
	if len(runs) < 2 {
		return attempts, len(runs), 0
	}
	return attempts, len(runs), jaccard(runs)
}

// obtainReview returns the LLM's review markdown for one case, either
// by invoking the CLI or reading a fixture. The TokenUsage is zero in
// replay mode (fixtures don't carry token counts) and from any live
// call where the source CLI didn't expose structured token metadata;
// callers should treat the zero value as "unknown", not "no tokens
// used."
func obtainReview(ctx context.Context, c Case, llm cli.LLM, opts Options) (string, cli.TokenUsage, error) {
	switch opts.Source {
	case SourceReplay:
		out, err := readFixture(opts.ReplayDir, c.ID, llm.Name)
		return out, cli.TokenUsage{}, err
	case SourceLive:
		return runLive(ctx, c, llm, opts.Timeout)
	default:
		return "", cli.TokenUsage{}, fmt.Errorf("unknown bench source %d", opts.Source)
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
// markdown output plus the per-call TokenUsage. Mirrors what
// internal/multi/orchestrator.go does for a single agent, minus the
// on-disk save (the bench writes its own report; per-case markdown
// is already persisted in the dataset).
func runLive(ctx context.Context, c Case, llm cli.LLM, timeout time.Duration) (string, cli.TokenUsage, error) {
	pack, err := prompts.Get(c.Language)
	if err != nil {
		return "", cli.TokenUsage{}, fmt.Errorf("load prompt pack for language %q: %w", c.Language, err)
	}
	return runLiveWithPrompt(ctx, c, llm, timeout, pack)
}

// runLiveWithPrompt is the lower-level helper used by both the
// treatment path (runLive, which threads the language-specific
// pack) and the --uplift baseline path (which threads
// prompts.BaselinePrompt). Splitting on the system-prompt
// argument is enough to express "same case, different prompt" —
// no other code path differs.
func runLiveWithPrompt(ctx context.Context, c Case, llm cli.LLM, timeout time.Duration, systemPrompt string) (string, cli.TokenUsage, error) {
	invoker := cli.NewInvoker(llm)
	if invoker == nil {
		return "", cli.TokenUsage{}, fmt.Errorf("no invoker for llm %q", llm.Name)
	}
	timeout = resolveTimeout(timeout, llm)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// invoker.Review returns (output, tokenUsage, err); bench scoring
	// works against the markdown only, but the tokenUsage threads
	// upward into CaseScore.Input/OutputTokens and feeds the v0.9.0
	// "Overhead vs raw model" leaderboard (treatment vs --uplift
	// baseline token cost per case).
	return invoker.Review(cctx, systemPrompt, c.Diff)
}

// resolveTimeout picks the per-call wall-clock cap, in priority
// order: caller-supplied opts.Timeout (when non-zero), the LLM's
// configured TimeoutSec, then the 120-second built-in fallback.
// Centralised so the treatment and --uplift baseline paths can't
// drift in their idea of "how long is too long" — duplicate
// inline copies were flagged in self-review iter 2.
func resolveTimeout(provided time.Duration, llm cli.LLM) time.Duration {
	if provided > 0 {
		return provided
	}
	if llm.TimeoutSec > 0 {
		return time.Duration(llm.TimeoutSec) * time.Second
	}
	return 120 * time.Second
}

// scoreBaseline runs the (case, LLM) once with prompts.BaselinePrompt
// (a minimal generic system prompt) instead of the language-specific
// pack, scores the output, and returns the BaselineScore.
//
// Returns (nil, err) when the baseline LLM invocation fails. The
// caller records the error on CaseScore.BaselineError so the
// renderer can flag the gap and strict mode can fail the run —
// a silent skip would mean partial baseline coverage gets
// compared against full treatment coverage, inflating uplift.
func scoreBaseline(ctx context.Context, c Case, llm cli.LLM, opts Options) (*BaselineScore, error) {
	start := time.Now()
	out, usage, err := runLiveWithPrompt(ctx, c, llm, opts.Timeout, prompts.BaselinePrompt)
	dur := time.Since(start)
	if err != nil {
		return nil, err
	}
	produced := ParseFindings(out)
	cs := Score(c, produced)
	return &BaselineScore{
		TruePositives:  cs.TruePositives,
		FalsePositives: cs.FalsePositives,
		FalseNegatives: cs.FalseNegatives,
		Produced:       cs.Produced,
		DurationMs:     dur.Milliseconds(),
		InputTokens:    usage.InputTokens,
		OutputTokens:   usage.OutputTokens,
	}, nil
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
	fillLanguageAggregates(lr)
	fillConsistencyAggregate(lr)
	fillTimingAggregates(lr, durations)
	fillTreatmentTokenAggregate(lr)
	fillBaselineAggregate(lr)
}

// fillTreatmentTokenAggregate sums per-case treatment token counts
// into LLMReport.TotalInput/OutputTokens, the per-case treatment-
// pass duration into LLMReport.TotalTreatmentDurationMs, and records
// the number of non-error cases as MeasuredCases. The leaderboard
// uses MeasuredCases as the denominator when rendering "tokens per
// case" / "treatment time per case" in the overhead block —
// averaging over len(lr.Cases) would skew the mean downward whenever
// any case errored (its token / duration contributions are zero).
//
// Treatment duration is accumulated here (not in fillTimingAggregates)
// because it is a treatment-only number — fillTimingAggregates' input
// already lumps in baseline + repeat wall-clock for the Overall
// median/p95 view, which is the right denominator for "did the run
// get slower?" but the wrong one for "what does the tool cost on top
// of a raw-LLM call?". Keeping the two duration aggregates in
// separate functions makes the contract explicit.
func fillTreatmentTokenAggregate(lr *LLMReport) {
	for _, cs := range lr.Cases {
		if cs.Error != "" {
			continue
		}
		lr.MeasuredCases++
		lr.TotalInputTokens += cs.InputTokens
		lr.TotalOutputTokens += cs.OutputTokens
		lr.TotalTreatmentDurationMs += cs.TreatmentDurationMs
	}
}

// fillBaselineAggregate rolls up CaseScore.Baseline pointers into
// LLMReport.Baseline. Mirrors qualityScores' shape so the leaderboard
// can compute treatment-vs-baseline deltas using the same
// micro-averaging across non-clean cases.
//
// Cleanness is read from the explicit CaseScore.Clean field, not
// inferred from treatment-side TP/FN counts — see tallyCases for
// the rationale.
//
// Sets lr.Baseline whenever --uplift was attempted: a populated
// aggregate (any case has a non-nil Baseline) carries the real
// numbers; an attempted-but-fully-failed pass (every case has
// BaselineError, no Baseline) still sets a zero-valued aggregate
// so JSON consumers can distinguish "uplift not run" (nil) from
// "uplift attempted, every case errored" (present, all zeros +
// per-case BaselineError surfaces the failures). Iter-2 self-
// review flagged the prior nil-on-all-fail shape as
// indistinguishable from "feature not used."
func fillBaselineAggregate(lr *LLMReport) {
	var tp, fp, fn int
	cleanCases := 0
	cleanFindings := 0
	nonCleanScored := 0
	measuredAny := false
	attemptedAny := false
	var totalDur int64
	var totalIn, totalOut int
	for _, cs := range lr.Cases {
		if cs.BaselineError != "" {
			attemptedAny = true
		}
		if cs.Baseline == nil {
			continue
		}
		measuredAny = true
		attemptedAny = true
		b := cs.Baseline
		totalDur += b.DurationMs
		totalIn += b.InputTokens
		totalOut += b.OutputTokens
		if cs.Clean {
			cleanCases++
			cleanFindings += b.Produced
			continue
		}
		nonCleanScored++
		tp += b.TruePositives
		fp += b.FalsePositives
		fn += b.FalseNegatives
	}
	if !attemptedAny {
		return
	}
	agg := &LLMBaselineAggregate{
		TotalDurationMs:       totalDur,
		MeasuredNonCleanCases: nonCleanScored,
		MeasuredCleanCases:    cleanCases,
		TotalInputTokens:      totalIn,
		TotalOutputTokens:     totalOut,
	}
	if measuredAny && nonCleanScored > 0 {
		agg.Precision = ratio(tp, tp+fp)
		agg.Recall = ratio(tp, tp+fn)
		if agg.Precision+agg.Recall > 0 {
			agg.F1 = 2 * agg.Precision * agg.Recall / (agg.Precision + agg.Recall)
		}
	}
	if cleanCases > 0 {
		agg.NoiseRate = float64(cleanFindings) / float64(cleanCases)
	}
	lr.Baseline = agg
}

// fillLanguageAggregates groups CaseScore entries by their Language
// field and computes per-language precision / recall / F1 / noise.
// Skips the split entirely when the dataset has only one language
// (empty Languages slice tells the report writer "don't bother
// printing a per-language section").
func fillLanguageAggregates(lr *LLMReport) {
	byLang := make(map[string][]CaseScore)
	for _, cs := range lr.Cases {
		lang := cs.Language
		if lang == "" {
			lang = "default"
		}
		byLang[lang] = append(byLang[lang], cs)
	}
	if len(byLang) <= 1 {
		return
	}

	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	out := make([]LanguageScore, 0, len(langs))
	for _, lang := range langs {
		cases := byLang[lang]
		t := tallyCases(cases)
		// Skip languages where every case errored — "no data" is
		// less misleading than F1=0.00 ("missed everything"). The
		// languageF1 sentinel (-1) will render these as "—".
		if t.nonCleanScored == 0 && t.cleanCases == 0 {
			continue
		}
		p, r, f1 := qualityScores(t)
		ls := LanguageScore{
			Language:  lang,
			Cases:     len(cases),
			Precision: p,
			Recall:    r,
			F1:        f1,
		}
		if t.cleanCases > 0 {
			ls.NoiseRate = float64(t.cleanFindings) / float64(t.cleanCases)
		}
		out = append(out, ls)
	}
	lr.Languages = out
}

// fillConsistencyAggregate computes the mean Jaccard across cases
// where consistency was actually measured (RunCount >= 2). Cases
// that errored or were single-run don't contribute to the mean —
// otherwise a one-error case would silently drag the consistency
// number toward 0.
//
// Sets lr.Consistency only when at least one case was measured so
// the *float64 stays nil for single-run benches (the JSON output
// then renders as null, distinguishing "not measured" from
// "measured, zero overlap on every case" — that latter case
// produces a real 0.0).
func fillConsistencyAggregate(lr *LLMReport) {
	var sum float64
	measured := 0
	for _, cs := range lr.Cases {
		if cs.Jaccard == nil {
			continue
		}
		sum += *cs.Jaccard
		measured++
	}
	if measured > 0 {
		mean := sum / float64(measured)
		lr.Consistency = &mean
	}
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
//
// The clean signal is the explicit CaseScore.Clean field (set by
// Score from Case.Clean / len(Expected)==0), not derived from
// treatment-side TP/FN/Matched counts. Iter-2 self-review (codex)
// flagged the previous shape as coupling baseline aggregation to
// treatment scoring internals; the explicit field also keeps the
// classification correct on a noisy treatment run against a
// truly-clean case (FP > 0 alone would have inferred "non-clean"
// and routed the case into the precision bucket).
func tallyCases(cases []CaseScore) caseTally {
	var t caseTally
	for _, cs := range cases {
		if cs.Error != "" {
			continue
		}
		if cs.Clean {
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
