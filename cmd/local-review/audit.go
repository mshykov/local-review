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
	// the audit package's internal default (256 KiB) which is the
	// realistic balance between LLM context-window pressure and
	// "one chunk = one package" cohesion.
	maxBytesPerChunk int
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
Picks the first authenticated LLM by default; override with --only.

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
	cmd.Flags().IntVar(&af.maxBytesPerChunk, "max-bytes-per-chunk", 0, "soft cap on per-chunk input size (0 = default 256 KiB)")

	return cmd
}

func runAudit(ctx context.Context, sf *sharedFlags, af auditFlags) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if af.topic == "" {
		topics, _ := prompts.AvailableAuditTopics()
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

	if af.dryRun {
		return writeAuditPlan(os.Stdout, af.topic, chunks)
	}

	llm, err := pickAuditLLM(sf)
	if err != nil {
		return err
	}

	rep, err := audit.Run(ctx, chunks, audit.Options{
		Topic:    af.topic,
		LLM:      llm,
		Progress: os.Stderr,
	})
	if err != nil {
		return err
	}
	rep.Root = "." // stamp the conceptual working-tree root onto the report
	return emitAuditReport(sf, af, rep)
}

// pickAuditLLM picks ONE authenticated LLM for the audit run.
// Reuses the existing review-path agent selection so the same
// `--only` / config rules apply, but takes only the first match
// — audit is single-LLM in v1.
//
// Returns an actionable error when no LLM is authenticated; the
// user sees a doctor hint, same as the review path.
func pickAuditLLM(sf *sharedFlags) (cli.LLM, error) {
	cfg, err := loadConfig()
	if err != nil {
		return cli.LLM{}, fmt.Errorf("load config: %w", err)
	}
	active, _ := pickAgents(cfg, sf)
	if len(active) == 0 {
		return cli.LLM{}, fmt.Errorf("audit needs at least one authenticated LLM (run `local-review doctor` for status)")
	}
	// Single-LLM in v1: first authenticated agent wins. The
	// review path's auto-merge ordering already prefers claude
	// when present, so this matches "use claude when available."
	return active[0], nil
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
func writeAuditFile(path string, rep audit.Report) (retErr error) {
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
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return audit.WriteJSON(f, rep)
	}
	return audit.WriteMarkdown(f, rep)
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
