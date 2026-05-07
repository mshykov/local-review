package main

import (
	"context"
	"fmt"
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
}

// benchCmd wires the `local-review bench` subcommand. Phase 1 of issue
// #56: load a labelled dataset, run each diff through the configured
// LLMs (or pre-recorded fixtures via --replay), score, print summary.
func benchCmd() *cobra.Command {
	var bf benchFlags

	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run the review-quality benchmark suite (Phase 1)",
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

Examples:

  local-review bench                              # live, default dataset
  local-review bench --replay bench/fixtures      # deterministic replay
  local-review bench --only claude,gemini --json  # restrict + JSON
  local-review bench --out bench-results.json     # also save JSON to file

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

	return cmd
}

// runBench dispatches a bench invocation: load config + dataset, pick
// the LLMs to run, score, render. strict makes per-case errors a
// non-zero exit; resolved by the caller from --strict + mode default.
func runBench(ctx context.Context, bf benchFlags, strict bool) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cases, err := bench.LoadDataset(bf.dataset)
	if err != nil {
		return err
	}

	llms, err := pickBenchLLMs(bf)
	if err != nil {
		return err
	}
	if len(llms) == 0 {
		return fmt.Errorf("no LLMs to bench (run `local-review doctor` for status, or pass --replay <dir> to use recorded fixtures)")
	}

	source := bench.SourceLive
	if bf.replayDir != "" {
		source = bench.SourceReplay
	}

	rep, err := bench.Run(ctx, cases, bench.Options{
		LLMs:      llms,
		Source:    source,
		ReplayDir: bf.replayDir,
	})
	if err != nil {
		return err
	}
	rep.Dataset = bf.dataset

	if bf.outFile != "" {
		if err := writeBenchJSONFile(bf.outFile, rep); err != nil {
			return fmt.Errorf("write --out file: %w", err)
		}
	}

	if bf.jsonOut {
		if err := bench.WriteJSON(os.Stdout, rep); err != nil {
			return err
		}
	} else {
		if err := bench.WriteText(os.Stdout, rep); err != nil {
			return err
		}
	}

	if strict {
		var failures []string
		for _, lr := range rep.LLMReports {
			for _, cs := range lr.Cases {
				if cs.Error != "" {
					failures = append(failures, fmt.Sprintf("%s/%s: %s", lr.LLM, cs.CaseID, cs.Error))
				}
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("strict mode: %d case(s) errored: %s", len(failures), strings.Join(failures, "; "))
		}
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
		names := splitCSV(bf.only)
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

// writeBenchJSONFile writes the report as JSON to path, creating
// parent directories as needed. Used by --out.
func writeBenchJSONFile(path string, rep bench.Report) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return bench.WriteJSON(f, rep)
}
