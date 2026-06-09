package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/audit"
	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/prompts"
)

// auditFlags collects every flag accepted by the `audit` subcommand.
type auditFlags struct {
	// topic is the audit-pack id (security / tech-debt / …).
	// Required — no default, because the choice IS the audit.
	topic string

	// include / exclude are path-prefix filters passed through to
	// the walker. Comma-separated. Empty include = walk every
	// auditable tracked file.
	include string
	exclude string

	// out, when non-empty, writes the markdown report to that
	// path instead of (or in addition to) stdout. When the
	// extension is .json, JSON shape is written instead.
	out string

	// dryRun prints the audit plan (chunk count, file count per
	// chunk, total bytes) without invoking the LLM. Lets a user
	// preview the cost / scope before paying tokens.
	dryRun bool

	// maxBytesPerChunk soft-caps each chunk. Zero falls back to
	// the audit package's internal default (96 KiB) which is the
	// realistic balance between LLM context-window pressure and
	// "one chunk = one package" cohesion. Packages over the cap
	// auto-split into `pkg [part N/M]` sub-chunks (see
	// internal/audit/walker.go splitChunk).
	maxBytesPerChunk int

	// with names the single agent to run the audit through —
	// matches any name that appears in `local-review doctor`'s
	// ready list, i.e. a CLI agent (`claude`, `codex`, …) or a
	// provider agent (any free-form id from cfg.llms with a
	// configured base_url, e.g. `qwen`, `local-fast`). When
	// empty, falls back to the first authenticated agent — same
	// shape as v0.13 and earlier. Single-valued in v1; multi-LLM
	// audit would multiply per-chunk cost × N and isn't on the
	// roadmap yet.
	//
	// Composes with --only: --only restricts the active set,
	// --with picks one entry from it. `--only codex --with claude`
	// fails the same way as `--with claude` against an
	// unauthenticated claude.
	with string

	// parallel caps concurrent per-chunk LLM calls (v0.15.1+).
	// Default 1 = strict sequential (the v0.10-v0.15.0 behaviour).
	// Set >1 against backends that serve concurrent requests
	// (Ollama with OLLAMA_NUM_PARALLEL configured, vLLM, etc.) to
	// fan out N chunks at a time — wallclock drops roughly N× on
	// a 37-chunk job. Cloud LLMs (claude/codex/copilot) typically
	// have per-tier rate limits; leave at 1 there to avoid 429s.
	// See internal/audit.Options.Parallelism for the constraints.
	parallel int
}

// auditCmd wires the `local-review audit` subcommand. v0.10.0-c:
// the first user-facing top-level command since v0.8's `bench`,
// and the first one that doesn't operate on a diff. Documented
// under the "review" group in --help because it's part of the
// same review-quality story; bench stays under "other" because
// it's contributor tooling.
func auditCmd(sf *sharedFlags) *cobra.Command {
	var af auditFlags

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Deep analysis of the codebase (security / tech-debt / …)",
		Long: `Walk the committed source tree and surface accumulated issues that
no diff-based review would find: pre-existing security gaps, dead
code, duplicated logic, leaky abstractions. Topic-driven and
opt-in — pick one focus per run via --topic.

Available topics:

  security     OWASP-aligned sweep for vulnerabilities, hardcoded
               secrets, missing authorization, weak crypto, etc.
  tech-debt    Dead code, duplicated logic, leaky abstractions,
               inconsistent error handling.

Examples:

  local-review audit --topic security
  local-review audit --topic tech-debt --out audit-tech-debt.md
  local-review audit --topic security --include internal/auth
  local-review audit --topic security --dry-run        # preview without invoking

Single-LLM by design in v1 — audit cost is per-package × per-topic,
and multi-LLM would multiply it without obvious quality return.
Picks the first authenticated LLM by default; override with --with
(e.g. --with claude, --with qwen) to pin a specific CLI or provider
agent. --only still works as the upstream allow-list filter.

Audit runs against the whole committed source tree (git ls-files),
so a working-tree-only change won't appear in findings until it's
committed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(cmd.Context(), sf, af)
		},
	}

	cmd.Flags().StringVar(&af.topic, "topic", "", "audit pack id (security / tech-debt); required")
	cmd.Flags().StringVar(&af.include, "include", "", "comma-separated path prefixes to include (default: all auditable tracked files)")
	cmd.Flags().StringVar(&af.exclude, "exclude", "", "comma-separated path prefixes to exclude")
	cmd.Flags().StringVar(&af.out, "out", "", "write report to this file (.md = markdown, .json = JSON)")
	// JSON output reuses the root-level persistent --json flag
	// (sharedFlags.jsonOut) — it's defined on the root command so
	// every review-shape subcommand can opt in. A separate
	// --audit-json flag would be inconsistent with the rest of
	// the CLI (Copilot caught this on PR #73).
	cmd.Flags().BoolVar(&af.dryRun, "dry-run", false, "print the audit plan (chunks + sizes) without invoking the LLM")
	cmd.Flags().IntVar(&af.maxBytesPerChunk, "max-bytes-per-chunk", 0, "per-chunk input cap; packages over the cap auto-split into `pkg [part N/M]` sub-chunks (0 = default 96 KiB; negative values are rejected)")
	// The placeholder backticks set cobra's type-name token — must
	// be a single word, so `agent` (no spaces) rather than the
	// example command. Anything else here would render as
	// `--with local-review doctor` in --help (cobra parses the
	// first backtick-quoted run as the placeholder).
	cmd.Flags().StringVar(&af.with, "with", "", "pin the audit to a specific `agent` (CLI or provider name from `local-review doctor`, e.g. claude / qwen); single-valued")
	cmd.Flags().IntVar(&af.parallel, "parallel", 1, "concurrent per-chunk LLM calls. 1 = sequential (default; safe everywhere). >1 = fan out (good for local Ollama with OLLAMA_NUM_PARALLEL set; cloud LLMs may rate-limit, keep at 1)")

	return cmd
}

func runAudit(ctx context.Context, sf *sharedFlags, af auditFlags) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if af.topic == "" {
		// AvailableAuditTopics failure means the embed is broken;
		// the user needs to know that, not see "(available: )"
		// with an empty parenthetical. CLAUDE.md rule 4.
		topics, listErr := prompts.AvailableAuditTopics()
		if listErr != nil {
			return fmt.Errorf("--topic is required (also failed to list available topics: %w)", listErr)
		}
		return fmt.Errorf("--topic is required (available: %s)", strings.Join(topics, ", "))
	}

	// Validate topic early — surfaces unknown-topic errors before
	// we walk the codebase. The runner would catch it later but
	// the user shouldn't pay file-read latency for a typo.
	if _, err := prompts.GetAuditPack(af.topic); err != nil {
		return err
	}

	chunks, err := audit.Walk(audit.WalkOptions{
		Include:          splitCSV(af.include),
		Exclude:          splitCSV(af.exclude),
		MaxBytesPerChunk: af.maxBytesPerChunk,
		Warn:             os.Stderr,
	})
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no auditable files found (include filters too narrow, or repo has no tracked source?)")
	}

	// Validate --parallel BEFORE the --dry-run early-return so an
	// invalid value (e.g. `--parallel 0`) gets the same error in
	// both modes. Pre-fix v0.15.1 self-review caught that dry-run
	// silently accepted bad values while the real run rejected them
	// — confusing for automation pipelines that ran dry-run first.
	if af.parallel < 1 {
		return fmt.Errorf("--parallel must be >= 1 (got %d); use 1 for sequential, >1 to fan out chunks", af.parallel)
	}

	if af.dryRun {
		return writeAuditPlan(os.Stdout, af.topic, chunks)
	}

	llm, err := pickAuditLLM(sf, af)
	if err != nil {
		return err
	}

	rep, err := audit.Run(ctx, chunks, audit.Options{
		Topic:       af.topic,
		LLM:         llm,
		Progress:    os.Stderr,
		Parallelism: af.parallel,
	})
	if err != nil {
		return err
	}

	// Apply the --min-severity / --max-findings filters (flag wins, else
	// the matching review.* config key). These were advertised on `audit`
	// but inert before v0.16.
	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		return cfgErr
	}
	minSev := sf.minSeverity
	if minSev == "" {
		minSev = cfg.Review.MinSeverity
	}
	if minSev != "" {
		switch strings.ToLower(minSev) {
		case "nit", "info", "warning", "major", "critical":
		default:
			return fmt.Errorf("--min-severity %q is invalid (use nit|info|warning|major|critical)", minSev)
		}
	}
	maxF := sf.maxFindings
	if maxF == 0 {
		maxF = cfg.Review.MaxFindings
	}
	rep, hidden := rep.Filtered(minSev, maxF)
	if hidden > 0 {
		word := "findings"
		if hidden == 1 {
			word = "finding"
		}
		fmt.Fprintf(os.Stderr, "Note: %d %s hidden by --min-severity/--max-findings (lower the floor or raise the cap to see them).\n", hidden, word)
	}

	rep.Root = "." // stamp the conceptual working-tree root onto the report
	return emitAuditReport(sf, af, rep)
}

// pickAuditLLM picks ONE authenticated LLM for the audit run.
// Reuses the existing review-path agent selection so the same
// `--only` / config rules apply, then narrows to one entry
// based on `--with` (when set) or first-match (default). Audit
// is single-LLM in v1.
//
// Returns an actionable error when no LLM is authenticated, or
// when `--with` names an agent that isn't in the active set; in
// either case the message points the user at `doctor` so the
// fix is one command away.
func pickAuditLLM(sf *sharedFlags, af auditFlags) (cli.LLM, error) {
	cfg, err := loadConfig()
	if err != nil {
		return cli.LLM{}, fmt.Errorf("load config: %w", err)
	}
	active, _, _ := pickAgents(cfg, sf)
	return selectAuditLLM(active, af.with)
}

// selectAuditLLM is the pure-function core of pickAuditLLM —
// extracted so the resolution rules (--with exact match, default
// to first authenticated agent) can be exercised without spinning
// up a config + filesystem. Match is exact by `LLM.Name` so a
// typo fails closed with an actionable error rather than silently
// fanning out to the wrong agent.
func selectAuditLLM(active []cli.LLM, with string) (cli.LLM, error) {
	if len(active) == 0 {
		return cli.LLM{}, fmt.Errorf("audit needs at least one authenticated LLM (run `local-review doctor` for status)")
	}
	if with == "" {
		// First authenticated agent wins. The review path's
		// auto-merge ordering already prefers claude when
		// present, so this matches "use claude when available."
		return active[0], nil
	}
	for _, llm := range active {
		if llm.Name == with {
			return llm, nil
		}
	}
	// Build the candidate list inline so the user can copy a name
	// directly out of the error message instead of re-running
	// doctor to learn what's available right now.
	names := make([]string, 0, len(active))
	for _, llm := range active {
		names = append(names, llm.Name)
	}
	return cli.LLM{}, fmt.Errorf("--with %q is not in the ready set %v (run `local-review doctor` to see what's authenticated)", with, names)
}

// emitAuditReport dispatches to the right writer based on flags.
// Output sinks are independent and additive (a workflow can `--out
// audit.md` while also wanting JSON on stdout), same shape as
// bench's emitter. JSON on stdout is gated by the root-level
// --json flag (sharedFlags.jsonOut).
func emitAuditReport(sf *sharedFlags, af auditFlags, rep audit.Report) error {
	if af.out != "" {
		if err := writeAuditFile(af.out, rep); err != nil {
			return fmt.Errorf("write --out file: %w", err)
		}
	}
	if sf.jsonOut {
		return audit.WriteJSON(os.Stdout, rep)
	}
	return audit.WriteText(os.Stdout, rep)
}

// writeAuditFile picks JSON vs markdown based on the extension —
// .json → JSON, anything else → markdown. Matches the implicit
// convention from bench's --markdown / --out separation but here
// it's one --out flag that switches on extension, since audit's
// canonical output IS the markdown report.
//
// File creation + mkdir-parents + close-error handling delegated to
// writeFileWithDirs (see cmd/local-review/iohelpers.go); this
// function owns only the extension → emitter routing.
func writeAuditFile(path string, rep audit.Report) error {
	return writeFileWithDirs(path, func(w io.Writer) error {
		if strings.HasSuffix(strings.ToLower(path), ".json") {
			return audit.WriteJSON(w, rep)
		}
		return audit.WriteMarkdown(w, rep)
	})
}

// writeAuditPlan prints the dry-run preview: one line per chunk
// (package + file count + size) plus a total. Lets the user
// eyeball cost before paying tokens; especially useful on first
// audit of an unfamiliar codebase.
//
// Uses audit.FormatBytes for byte sizing so the dry-run preview
// renders the same units the runner's per-chunk progress lines
// use during a real run — single source of truth for the
// formatting, no drift between preview and run.
func writeAuditPlan(w io.Writer, topic string, chunks []audit.Chunk) error {
	totalFiles := 0
	totalBytes := 0
	for _, c := range chunks {
		totalFiles += len(c.Files)
		totalBytes += c.SizeBytes
	}
	if _, err := fmt.Fprintf(w, "Audit plan (topic=%s, dry-run):\n  %d chunks · %d files · %s total\n\n",
		topic, len(chunks), totalFiles, audit.FormatBytes(totalBytes)); err != nil {
		return err
	}
	for _, c := range chunks {
		suffix := ""
		if len(c.Files) != 1 {
			suffix = "s"
		}
		if _, err := fmt.Fprintf(w, "  %-40s  %d file%s  %s\n",
			c.Package, len(c.Files), suffix, audit.FormatBytes(c.SizeBytes)); err != nil {
			return err
		}
	}
	return nil
}
