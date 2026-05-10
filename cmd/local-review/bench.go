package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/bench"
	"github.com/mshykov/local-review/internal/cli"
)

// benchFlags collects every flag accepted by the `bench` subcommand.
type benchFlags struct {
	dataset   string // path to the dataset root (default: bench/dataset)
	replayDir string // path to fixtures; non-empty → replay mode
	only      string // comma-separated llm names to restrict the run to
	jsonOut   bool   // emit JSON to stdout instead of text summary
	outFile   string // also write JSON to this file

	// strict, when true, makes any per-case error (missing fixture,
	// CLI invocation failure, etc.) a non-zero exit. Defaults to
	// true in replay mode (CI's intended gate — a missing fixture
	// must fail the workflow, not pass a green report on a dataset
	// that wasn't actually scored) and false in live mode (real
	// CLIs flake, a transient one-agent failure shouldn't kill the
	// bench and lose data on agents that did succeed).
	strict bool

	// repeat is the per-(case, LLM) sample count for the Phase-2
	// consistency metric (Jaccard similarity across runs). Default
	// 1 = no repeat. Values > 1 only make sense in live mode;
	// replay+repeat>1 errors out (fixtures are deterministic,
	// Jaccard would always be 1.0).
	repeat int

	// markdownPath, when non-empty, writes a leaderboard-style
	// markdown report (RESULTS.md shape) to that path. Independent
	// of --out (JSON) — common workflow is `--markdown bench/RESULTS.md
	// --out bench-results.json` to commit both.
	markdownPath string

	// uplift, when true, runs each (case, LLM) pair an additional
	// time with a minimal generic system prompt and records the
	// resulting score as the "baseline." The leaderboard then
	// shows treatment-vs-baseline deltas — the headline answer to
	// "is local-review actually better than running the raw LLM
	// cold?" Live mode only; replay rejects --uplift up at the
	// runner level.
	uplift bool
}

// benchCmd wires the `local-review bench` subcommand. Phase 1 + 2 of
// issue #56: load a labelled dataset, run each diff through the
// configured LLMs (or pre-recorded fixtures via --replay), score,
// print summary, optionally emit a markdown leaderboard.
func benchCmd() *cobra.Command {
	var bf benchFlags

	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run the review-quality benchmark suite",
		Long: `Run the local-review benchmark suite against a labelled dataset
of diffs. For each diff, every active LLM produces a review; findings
are scored against the labels (precision / recall / F1 + noise rate on
clean diffs).

Two run modes:

  Live (default):  invoke each LLM CLI for real. Slow, costs tokens,
                   requires CLI authentication.
  Replay:          --replay <fixtures-dir> reads pre-recorded review
                   markdown from <fixtures>/<case>/<llm>.md instead of
                   calling the CLI. Used by CI for deterministic,
                   no-cost runs.

Phase 2 + 3 additions:

  --repeat N        sample each (case, LLM) N times in live mode and
                    report Jaccard consistency across runs. Replay
                    rejects this (fixtures are deterministic).
  --markdown <path> write a leaderboard-style report (overall +
                    per-language + per-case tables) to <path>,
                    suitable for committing as bench/RESULTS.md.
  --uplift          run each (case, LLM) twice — once with a minimal
                    generic system prompt (baseline) and once with
                    the full local-review pipeline (treatment) — and
                    report treatment-vs-baseline deltas. Live only;
                    replay rejects this. Costs roughly 2× the tokens.
                    Answers "is local-review actually better than
                    running the raw LLM cold?".

Examples:

  local-review bench                                       # live, default dataset
  local-review bench --replay bench/fixtures               # deterministic replay
  local-review bench --only claude,gemini --json           # restrict + JSON
  local-review bench --out bench-results.json              # also save JSON to file
  local-review bench --repeat 5 --only claude              # consistency check
  local-review bench --uplift --only claude                # treatment vs baseline
  local-review bench --replay bench/fixtures \
       --markdown bench/RESULTS.md                         # update the leaderboard

The dataset format and starter cases live under bench/. See
bench/README.md for how to add a new case.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Effective strictness: explicit --strict wins; otherwise
			// default ON in replay mode, OFF in live mode.
			strict := bf.strict
			if !cmd.Flags().Changed("strict") {
				strict = bf.replayDir != ""
			}
			return runBench(cmd.Context(), bf, strict)
		},
	}

	cmd.Flags().StringVar(&bf.dataset, "dataset", "bench/dataset", "path to the dataset root")
	cmd.Flags().StringVar(&bf.replayDir, "replay", "", "fixtures directory; when set, reads <replay>/<case>/<llm>.md instead of invoking CLIs")
	cmd.Flags().StringVar(&bf.only, "only", "", "comma-separated agent names to bench (default: all installed in live mode, all canonical in replay)")
	cmd.Flags().BoolVar(&bf.jsonOut, "json", false, "emit JSON to stdout instead of the text summary")
	cmd.Flags().StringVar(&bf.outFile, "out", "", "also write JSON to this path (text summary still goes to stdout unless --json)")
	cmd.Flags().BoolVar(&bf.strict, "strict", false, "exit non-zero on any per-case error; default ON in --replay (CI gate), OFF in live mode")
	cmd.Flags().IntVar(&bf.repeat, "repeat", 1, "sample each (case, LLM) N times for Jaccard consistency (live mode only; replay rejects N>1)")
	cmd.Flags().StringVar(&bf.markdownPath, "markdown", "", "also write a leaderboard markdown file to this path (overall + per-language + per-case tables)")
	cmd.Flags().BoolVar(&bf.uplift, "uplift", false, "also run each (case, LLM) with a minimal generic system prompt and report treatment-vs-baseline deltas (live mode only; replay rejects --uplift). Costs roughly 2× the tokens; the answer is 'is local-review better than running the raw LLM cold?'")

	return cmd
}

// runBench dispatches a bench invocation: load config + dataset, pick
// the LLMs to run, score, render. strict makes per-case errors a
// non-zero exit; resolved by the caller from --strict + mode default.
//
// Decomposed into small steps so each is independently obvious — the
// pre-decomposition shape was a single function that tripped Sonar's
// cognitive-complexity budget (26 vs. 15 allowed) and was hard to
// read top-to-bottom.
func runBench(ctx context.Context, bf benchFlags, strict bool) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cases, llms, err := loadBenchInputs(bf)
	if err != nil {
		return err
	}

	rep, err := executeBench(ctx, bf, cases, llms)
	if err != nil {
		return err
	}

	if err := emitBenchReport(bf, rep); err != nil {
		return err
	}

	if strict {
		return checkStrictFailures(rep)
	}
	return nil
}

// loadBenchInputs resolves the dataset and the LLM set the bench will
// run against. Returns an actionable error when no LLMs were resolved
// (more useful than a silent zero-row report).
func loadBenchInputs(bf benchFlags) ([]bench.Case, []cli.LLM, error) {
	cases, err := bench.LoadDataset(bf.dataset)
	if err != nil {
		return nil, nil, err
	}
	llms, err := pickBenchLLMs(bf)
	if err != nil {
		return nil, nil, err
	}
	if len(llms) == 0 {
		return nil, nil, fmt.Errorf("no LLMs to bench (run `local-review doctor` for status, or pass --replay <dir> to use recorded fixtures)")
	}
	return cases, llms, nil
}

// executeBench wires the bench package together: pick source mode,
// run, stamp the dataset path back onto the report.
func executeBench(ctx context.Context, bf benchFlags, cases []bench.Case, llms []cli.LLM) (bench.Report, error) {
	source := bench.SourceLive
	if bf.replayDir != "" {
		source = bench.SourceReplay
	}
	rep, err := bench.Run(ctx, cases, bench.Options{
		LLMs:      llms,
		Source:    source,
		ReplayDir: bf.replayDir,
		Repeat:    bf.repeat,
		Uplift:    bf.uplift,
	})
	if err != nil {
		return bench.Report{}, err
	}
	rep.Dataset = bf.dataset
	return rep, nil
}

// emitBenchReport handles the four output sinks:
//   - --out <file>      JSON to disk
//   - --markdown <file> leaderboard markdown to disk
//   - --json            JSON to stdout
//   - default           text summary to stdout
//
// The two file sinks are independent and additive (a CI workflow
// typically wants both: bench-results.json for delta diffing,
// RESULTS.md for human review). Stdout still gets text or JSON.
func emitBenchReport(bf benchFlags, rep bench.Report) error {
	if bf.outFile != "" {
		if err := writeBenchJSONFile(bf.outFile, rep); err != nil {
			return fmt.Errorf("write --out file: %w", err)
		}
	}
	if bf.markdownPath != "" {
		if err := writeBenchMarkdownFile(bf.markdownPath, rep); err != nil {
			return fmt.Errorf("write --markdown file: %w", err)
		}
	}
	if bf.jsonOut {
		return bench.WriteJSON(os.Stdout, rep)
	}
	return bench.WriteText(os.Stdout, rep)
}

// checkStrictFailures collects any per-case errors and returns them
// as one summarised error. Used by --strict (default ON in --replay)
// to make CI fail on a missing fixture or a CLI invocation failure.
//
// Baseline-side failures (--uplift on, generic-prompt invocation
// errored while the treatment pass succeeded) are caught here too:
// without that, partial baseline coverage would slip past CI while
// leaving the leaderboard quietly comparing treatment-of-N against
// baseline-of-M-<-N, inflating apparent uplift.
func checkStrictFailures(rep bench.Report) error {
	var failures []string
	for _, lr := range rep.LLMReports {
		for _, cs := range lr.Cases {
			if cs.Error != "" {
				failures = append(failures, fmt.Sprintf("%s/%s: %s", lr.LLM, cs.CaseID, cs.Error))
			}
			if cs.BaselineError != "" {
				failures = append(failures, fmt.Sprintf("%s/%s baseline: %s", lr.LLM, cs.CaseID, cs.BaselineError))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("strict mode: %d case(s) errored: %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// pickBenchLLMs decides which agents to bench.
//
//   - Replay mode: every name in --only is accepted as a stub (Name
//     only) — the whole point of replay is "I don't have these CLIs
//     authenticated locally". Empty --only expands to the canonical
//     trio so the runner produces a row per fixture-bearing LLM.
//   - Live mode: reuse pickAgents() so the bench respects the same
//     readiness + config.enabled rules as `local-review review`.
//     Mirrors the v0.6 review-runner strictness: `--only` matching
//     no ready agents is an error, not a silent fall-through.
func pickBenchLLMs(bf benchFlags) ([]cli.LLM, error) {
	if bf.replayDir != "" {
		names := dedupeStrings(splitCSV(bf.only))
		if len(names) == 0 {
			names = []string{"claude", "codex", "gemini"}
		}
		out := make([]cli.LLM, 0, len(names))
		for _, n := range names {
			out = append(out, cli.LLM{Name: n})
		}
		return out, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	sf := &sharedFlags{only: bf.only}
	active, _ := pickAgents(cfg, sf)
	if len(active) == 0 && bf.only != "" {
		return nil, fmt.Errorf("--only %q matched no ready LLMs (run `local-review doctor` to see what's authenticated; refusing to silently bench a different set than the one named)", bf.only)
	}
	return active, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// dedupeStrings keeps the first occurrence of each non-empty string
// while preserving input order.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// writeBenchJSONFile writes the report as JSON to path, creating
// parent directories as needed. Used by --out.
//
// Close error is checked explicitly (not just deferred-and-dropped):
// on a full-disk or other late-write I/O failure, Write may return
// success while the buffered tail bytes only fail at Close. Without
// this check a corrupt/truncated bench-results.json would look
// indistinguishable from a clean run — exactly the failure mode
// the bench is supposed to surface, not hide.
func writeBenchJSONFile(path string, rep bench.Report) (retErr error) {
	return writeBenchToFile(path, rep, bench.WriteJSON)
}

// writeBenchMarkdownFile writes the leaderboard markdown report to
// path, creating parent directories as needed. Used by --markdown.
// Same Close-error discipline as writeBenchJSONFile so a partial
// write on a full disk fails the bench instead of silently shipping
// a truncated leaderboard.
func writeBenchMarkdownFile(path string, rep bench.Report) error {
	return writeBenchToFile(path, rep, bench.WriteMarkdown)
}

// writeBenchToFile is the shared driver for the two file sinks. The
// emitter callback decides the wire format; this function owns the
// directory-creation, open, deferred-close-with-error-check pattern.
func writeBenchToFile(path string, rep bench.Report, emit func(io.Writer, bench.Report) error) (retErr error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	return emit(f, rep)
}
