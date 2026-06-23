package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/agentselect"
	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/lang"
	"github.com/mshykov/local-review/internal/multi"
	"github.com/mshykov/local-review/internal/prompts"
	"github.com/mshykov/local-review/internal/review"
)

// errBlockingFindings signals that the review surfaced major/critical
// findings — pre-commit hooks need exit code 2. main() translates this
// to os.Exit(2) AFTER cobra returns, so all deferred cleanup still runs.
var errBlockingFindings = errors.New("blocking findings present")

// pickAgents returns the LLMs to run for this invocation. Wraps
// agentselect.Select with real CLI + provider detection + classify()
// calls; tests drive agentselect.Select directly with synthetic input.
//
// CLI agents are detected from the hardcoded supported list (claude /
// gemini / codex / copilot / antigravity). Provider agents are
// detected from cfg.LLMs entries that carry a BaseURL — that's the
// kind discriminator. Both kinds end up in the same []LLM slice and
// flow through identical selection / ready-filter logic.
func pickAgents(cfg config.Config, sf *sharedFlags) (active []cli.LLM, configDisabled, sunsetDropped []string) {
	// Honor cfg.LLMs[*].CLIPath when set — corporate / nix-store installs
	// at non-standard paths can override the default binary name.
	overrides := make(map[string]string, len(cfg.LLMs))
	for name, c := range cfg.LLMs {
		if c.CLIPath != "" {
			overrides[name] = c.CLIPath
		}
	}
	detected := cli.DetectAllWithOverrides(overrides)
	// Drop any CLI agent whose name is actually a provider entry (base_url
	// set) before appending the provider twins — see agentselect.DropCLITwins.
	detected = agentselect.DropCLITwins(detected, cfg)
	detected = append(detected, cli.DetectProviders(context.Background(), agentselect.ProviderSpecsFromConfig(cfg))...)

	ready := make(map[string]bool, len(detected))
	for _, llm := range detected {
		// Mirror doctor: honor cfg.LLMs[*].APIKeyEnv so a user with
		// a key under a non-canonical env var gets ✓ ready instead
		// of being silently filtered out.
		var customEnvVar string
		if c, ok := cfg.LLMs[llm.Name]; ok {
			customEnvVar = c.APIKeyEnv
		}
		status, _ := classify(llm, customEnvVar)
		ready[llm.Name] = status == statusReady
	}
	return agentselect.Select(detected, ready, cfg, sf.only, time.Now().UTC())
}

// runMultiLLMReview executes the parallel multi-LLM flow: print the
// agent roster, extract the diff, fan out reviews, merge findings, save
// to disk, and print the merged report to stdout.
func runMultiLLMReview(ctx context.Context, cfg config.Config, sf *sharedFlags, active []cli.LLM, configDisabled, sunsetDropped []string, mode git.Mode, ref string) error {
	if err := cfg.Validate(); err != nil {
		// `--only` is an explicit allow-list that overrides config-level
		// enable/disable for agent selection (see agentselect.Select). So
		// an "all LLMs disabled" config is benign here — the user named the
		// agents to run. This is what makes the two-pass workflow work
		// with ONE config (disable all → Ollama fallback for the default
		// run; `--only claude,codex,...` → those cloud agents). We only
		// swallow THIS specific error; every other Validate failure
		// (bad merge.preferred_llm, etc.) still aborts.
		// Use agentselect.ParseOnlyList (not a bare sf.only != "") so a
		// whitespace-only --only value is treated as "unset" — matching how
		// agentselect.Select decides whether --only is in effect.
		onlySet := len(agentselect.ParseOnlyList(sf.only)) > 0
		if !(onlySet && errors.Is(err, config.ErrAllLLMsDisabled)) {
			return fmt.Errorf("invalid config: %w", err)
		}
	}
	warnIgnoredFlags(sf)
	if err := validateMergeWith(sf, active); err != nil {
		return err
	}

	// Resolve commit + branch up front so the opening roster line can
	// show "Reviewing <branch> (<sha>) with N LLMs..." — printing the
	// roster before resolution would force a generic header and the
	// user would have to scroll past N pages of findings to learn what
	// they actually reviewed.
	commit, branch, err := resolveCommitBranch(mode, ref)
	if err != nil {
		return err
	}
	printAgentRoster(active, configDisabled, sunsetDropped, cfg, branch, commit)

	diffs, err := git.Extract(ctx, mode, ref)
	if err != nil {
		return fmt.Errorf("extract diff: %w", err)
	}
	// Apply review.include/review.exclude before fan-out. The single-
	// LLM fallback already does this in internal/review/review.go; the
	// multi-LLM path skipped it pre-v0.5.x, so users with tuned glob
	// configs (e.g. exclude: ["**/generated/**"]) silently saw the LLM
	// review their auto-generated files.
	diffs = review.FilterDiffs(diffs, cfg.Review.IncludeGlobs, cfg.Review.ExcludeGlobs)
	if len(diffs) == 0 {
		fmt.Println("No changes to review.")
		return nil
	}
	diffStr := formatDiffForLLM(diffs)

	// Pick the language pack the same way the single-LLM path does
	// (review.go:43-50). Pre-v0.6.x the multi-LLM path skipped this
	// entirely — every agent ran with a generic 4-bullet prompt while
	// the README claimed language-specific packs were applied. Now
	// each agent gets the same Go/TS/Python/Rust pack the single-LLM
	// path uses, with a markdown-output override appended (see
	// multiLLMOutputOverride in cli/invoker.go) so the merger can
	// consolidate prose across reviewers.
	systemPrompt, err := selectPromptPack(cfg, diffs)
	if err != nil {
		return err
	}

	// Preflight: drop agents whose context window can't fit the
	// (prompt + diff) payload. Pre-v0.7 every agent saw the diff
	// regardless of size; oversized prompts surfaced as model-
	// specific failures (claude SIGKILL, codex 4xx, gemini quietly
	// surviving via its larger window) — confusing and expensive.
	// Catching this here means: predictable up-front skip with an
	// actionable "use a smaller scope" hint, no tokens spent on a
	// call that would 4xx.
	active, skipped, promptDiffTokens := cli.PreflightFilter(active, systemPrompt, diffStr)
	if len(skipped) > 0 {
		fmt.Fprint(os.Stderr, cli.SkipSummary(skipped))
		fmt.Fprintln(os.Stderr)
	}
	if len(active) == 0 {
		// Every agent's context was too small. Bailing here saves
		// the user a 2-minute fan-out where each agent fails
		// individually with a vague stderr.
		return fmt.Errorf("prompt+diff is too large for every active agent: ~%d tokens estimated; try a smaller scope (`local-review commit HEAD` or `local-review staged`)", promptDiffTokens)
	}

	// Pre-flight readiness probe (v0.10.1). Issues a tiny "reply OK"
	// call to each remaining active LLM with a ~10s per-LLM timeout,
	// renders a ✓/✗ block immediately, then drops the ✗ agents from
	// the active set before the real fan-out.
	//
	// Why this exists: v0.10.0's first-customer dogfood surfaced
	// gemini's "exhausted capacity on this model" error AFTER
	// ~4 minutes — the real-review timeout window is large enough
	// to hide a doomed LLM for that long. The probe collapses that
	// signal to seconds and lets the run proceed with the surviving
	// agents instead of waiting on the doomed one. PreflightFilter
	// above does a *static* context-window check (cheap, no LLM
	// call); this is the *dynamic* auth+capacity probe (one tiny
	// call per LLM).
	//
	// --no-preflight bypasses this entirely for callers who don't
	// want the extra ~10s + ~1k tokens per LLM. Reserved escape
	// hatch — not the recommended path.
	if !sf.noPreflight {
		// Resolve the per-LLM probe timeout: explicit --preflight-
		// timeout wins; 0/unset falls back to cli.DefaultProbeTimeout
		// (10s). The runner — not Probe — owns this resolution so
		// the readiness-block render can show the actual configured
		// value, not the package default.
		probeTimeout := sf.preflightTimeout
		if probeTimeout <= 0 {
			probeTimeout = cli.DefaultProbeTimeout
		}
		active = runPreflightProbe(ctx, active, probeTimeout, sf.strictProbe)
		// Short-circuit on user interrupt / parent context cancel.
		// Pre-fix the runner's only check was len(active) == 0,
		// which would surface "every active LLM failed pre-flight"
		// after a Ctrl+C — accurate only in the narrow technical
		// sense; misleading about the actual cause. Propagate
		// ctx.Err() directly so the user sees the right reason
		// (and main()'s signal-handler exit path gets the right
		// exit code).
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(active) == 0 {
			// Every authenticated LLM failed the probe. The
			// readiness block above already showed the per-LLM
			// reason; this final message points the user at
			// `doctor` for a structured diagnostic instead of
			// asking them to re-grep the rendered block.
			return fmt.Errorf("every active LLM failed the pre-flight readiness probe (run `local-review doctor` for status, or `--no-preflight` to skip the probe)")
		}
	}

	startTime := time.Now()
	storage := multi.NewStorage(cfg.Storage.BasePath)
	orch := multi.NewOrchestrator(active, storage)

	resultsCh, err := orch.RunParallel(ctx, systemPrompt, diffStr, commit, branch)
	if err != nil {
		return fmt.Errorf("run reviews: %w", err)
	}

	// Stream per-agent completion lines as each agent finishes. The
	// channel closes after all agents report, so the loop also serves
	// as the synchronisation point before merge. Emission order =
	// completion order; we dropped the [N/M] numeric prefix so the
	// non-roster order doesn't read as a bug. Lines look like:
	//   claude ✓ (51.5s) · 12.3k in / 4.5k out
	//   codex ✗ timeout — try `local-review commit HEAD` for ...
	results := make([]multi.ReviewResult, 0, len(active))
	anyFailed := false
	for r := range resultsCh {
		results = append(results, r)
		if r.Error != nil {
			anyFailed = true
			// r.Error is the invoker's ClassifyExit output — already
			// has the actionable hint inline. No "review failed:"
			// prefix or "(output: )" wrapping.
			fmt.Printf("%s ✗ %s%s\n", r.LLM, r.Error, formatTokenSuffix(r.Tokens))
		} else {
			fmt.Printf("%s ✓ (%.1fs)%s\n", r.LLM, r.Duration.Seconds(), formatTokenSuffix(r.Tokens))
		}
	}
	fmt.Println()

	// If the user interrupted during the fan-out — the long phase where
	// Ctrl+C is most likely — surface cancellation directly. Otherwise every
	// invoker returns a context error and the downstream gate reports "all N
	// LLM reviews failed", which misdiagnoses a user interrupt as an agent
	// failure. Mirrors the post-probe handler above.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Sort results back to roster order before any downstream use.
	// Display lines above already printed in completion order (the
	// streaming UX); everything from here on (BuildMergeInput,
	// buildMetadata, selectMergeLLM) is order-sensitive and must be
	// deterministic across runs on identical input. Pre-fix, two
	// identical `local-review review` runs could produce different
	// merge prompts (reviewer #1 was "claude" in one run, "codex" in
	// the next, depending on which finished first) — a regression
	// from v0.6.6 that erodes trust without surfacing as an error.
	results = sortByRoster(results, active)

	// Surface the on-disk path so users know where to find raw
	// per-LLM output — especially when one agent failed and they
	// want to debug from saved partial state. Storage uses
	// SanitizeBranch internally; we mirror that here so the path
	// we print actually exists.
	storageDir := filepath.Join(cfg.Storage.BasePath, git.SanitizeBranchName(branch))
	if anyFailed {
		fmt.Fprintf(os.Stderr, "Per-LLM reviews saved to → %s/\n\n", storageDir)
	} else {
		// On a clean run print to stdout (it's informational, not a
		// warning) and quietly so it doesn't compete with the merged
		// findings the user is about to read.
		fmt.Printf("Per-LLM reviews → %s/\n\n", storageDir)
	}

	// Single-pass summary of the result set. Both views (Error == nil
	// "Successful" and HasMergeableOutput "WithOutput") are derived
	// once here and threaded through everything downstream, so the
	// two counts can't drift across call sites the way they did pre-
	// consolidation (audit/tech-debt.md flagged runner.go:156 as a
	// `major` finding for exactly that pattern — six call sites
	// observing the result set independently meant any one updating
	// the other was a regression risk).
	gate := multi.DecideGate(results)
	rmode := classifyRunModeFromGate(gate) // computed once here; reused in the report-path block below to avoid a second traversal
	metadata := buildMetadata(commit, branch, results, startTime)

	// Short-circuit on "nothing for the merger to consume." We branch
	// on WithOutput (what the merger actually sees via BuildMergeInput),
	// not Successful (Error == nil), because two cases historically
	// slipped past a Successful-only check:
	//
	//   1. SaveReview-failed-with-output: Error is set but Output is
	//      populated. Successful drops to 0 even though the merger
	//      could still consolidate the in-memory output. Pre-fix we
	//      aborted with "all N reviews failed" — wrong, the reviews
	//      ran, only persistence failed.
	//   2. CLI-exited-zero-with-empty-output: Error is nil but Output
	//      is "". Successful stays positive but BuildMergeInput drops
	//      the empty entry, so the merger ran on 0 reviews and
	//      classifyRunMode fell through to runModeMerge — both giving
	//      the user a "Merged review" framing for what was actually
	//      nothing.
	//
	// HasMergeable() is the gate predicate; ClassifyZero() picks the
	// right error message when the gate trips. The error taxonomy is
	// owned by the GateDecision type, not duplicated inline here —
	// see internal/multi/orchestrator.go ZeroMergeableReason.
	if !gate.HasMergeable() {
		metadata.Merge.Status = "skipped"
		if _, err := storage.SaveMetadata(branch, commit, metadata); err != nil {
			// Best-effort: per-LLM markdown saves either succeeded or
			// would already have surfaced their own errors via the
			// streaming completion lines. Surface the metadata failure
			// to stderr so the user knows provenance is missing, but
			// don't fail the run — they came here for the gate exit
			// code, not for metadata.json.
			fmt.Fprintf(os.Stderr, "Warning: failed to save metadata.json (review markdown still on disk): %v\n", err)
		}
		switch gate.ClassifyZero() {
		case multi.ZeroMergeableAllFailed:
			return fmt.Errorf("all %d LLM reviews failed", gate.Total)
		case multi.ZeroMergeableAllEmpty:
			return fmt.Errorf("all %d LLM reviews returned empty output (no findings to merge)", gate.Total)
		default: // ZeroMergeableMixed
			// Mixed: some agents crashed, others exited zero with blank
			// output. Pre-fix this case printed "all returned empty
			// output" which misled users into debugging the wrong
			// problem (an empty-output bug vs a crash).
			return fmt.Errorf("no LLM produced output: %d failed, %d returned empty (nothing to merge)", gate.Failed(), gate.Successful)
		}
	}

	mergedPath, mergedContent, mergeTokens := mergeAndPrint(ctx, cfg, sf, active, results, gate, storage, commit, branch, metadata)
	if _, err := storage.SaveMetadata(branch, commit, metadata); err != nil {
		// Same posture as the no-mergeable-output branch above: review
		// markdown files are already on disk, the gate's exit code is
		// the value the user came for, so we don't fail the run on a
		// metadata.json save error. Print a clear stderr warning so
		// the user knows provenance + token totals are missing for
		// this run and can investigate (full disk, read-only fs,
		// permission denied).
		fmt.Fprintf(os.Stderr, "Warning: failed to save metadata.json (review markdown still on disk): %v\n", err)
	}

	fmt.Println()
	// "produced output" mirrors the classifier (CountWithOutput) and
	// the merger's actual consumption criterion. Pre-fix this line
	// said "succeeded" using CountSuccessful (Error == nil), which
	// drifted from the rest of the surface — a SaveReview-failed-
	// with-output run would print "0/3 succeeded" while the merger
	// happily consolidated all 3 outputs and the gate fired correctly.
	//
	// Token total aggregates per-LLM review tokens + merge-step
	// tokens. Omitted entirely when every agent reported zero (CLI
	// version too old to surface usage) — printing "0 tokens" would
	// mislead users into thinking the call was free.
	totalTokens := aggregateTokens(results, mergeTokens)
	if totalTokens > 0 {
		fmt.Printf("✓ %d/%d LLMs produced output · total %s · ~%s tokens\n", gate.WithOutput, gate.Total, time.Since(startTime).Round(time.Second), humanTokens(totalTokens))
	} else {
		fmt.Printf("✓ %d/%d LLMs produced output · total %s\n", gate.WithOutput, gate.Total, time.Since(startTime).Round(time.Second))
	}
	if mergedPath != "" {
		// Report-path label uses `rmode` (computed above from the same
		// gate) so we don't traverse `results` again. Degraded/solo
		// runs use distinct labels so users can tell single-source
		// from consensus.
		switch rmode {
		case runModeDegraded:
			fmt.Printf("Single-LLM report (%d of %d agents produced no output): %s\n", gate.Total-gate.WithOutput, gate.Total, mergedPath)
		case runModeSolo:
			fmt.Printf("Report: %s\n", mergedPath)
		default:
			fmt.Printf("Merged report: %s\n", mergedPath)
		}
	}

	// Two independent signals trip the gate (any one is enough). False
	// positives are preferred over false negatives — over-blocking is
	// a re-run, under-blocking is a shipped bug.
	//
	//  1. The merged markdown — what the merger LLM concluded after
	//     seeing the (possibly-truncated) per-LLM reviews.
	//  2. Each per-LLM review's full Output — defends against the
	//     8 KB merger-input truncation hiding blocking findings past
	//     the cut, AND against merger failures (merger.Merge error,
	//     SaveMerged error, mergeLLM unavailable, whitespace-only
	//     output) where signal #1 is unavailable. The on-disk per-LLM
	//     file has always had the full output; this just scans it
	//     before we decide.
	//
	// Compute the per-LLM signal first so a merge-step failure with
	// blocking per-LLM findings still trips the gate. Pre-fix the
	// empty-merged-content guard short-circuited before this scan
	// could run, so a merger timeout or rate-limit collapsed an
	// exit-2 into exit 1 — and the documented pre-commit hook treats
	// tool failures (exit 1) as "let the commit through." Returning
	// the sentinel (rather than os.Exit) lets cobra and main()
	// unwind defers.
	// The decision (and its ordering invariant) lives in the pure helper
	// decideExitGate so it can be unit-tested without git / probe /
	// orchestrator plumbing — see TestDecideExitGate. The I/O (the
	// per-LLM-only warning and the merge-unavailable error message) stays
	// here.
	gateOut := decideExitGate(mergedContent, results)
	if gateOut.mergeUnavailable {
		// Merge step produced nothing (mergeLLM unavailable, merger
		// error, save failed, whitespace-only) AND no per-LLM review
		// flagged a blocking finding. Per-LLM reviews are saved on disk.
		return fmt.Errorf("merge step produced no output; per-LLM reviews are saved under %s and showed no blocking findings, but the merged report is unavailable", cfg.Storage.BasePath)
	}
	if gateOut.block {
		if strings.TrimSpace(mergedContent) == "" {
			fmt.Fprintln(os.Stderr, "Warning: merge step produced no output, but per-LLM reviews flagged blocking findings — gate firing on the per-LLM signal.")
		}
		return errBlockingFindings
	}
	return nil
}

// gateOutcome is the review exit-gate decision, computed independently of
// any I/O so the ordering invariant is unit-testable. See decideExitGate.
type gateOutcome struct {
	// block is true when at least one blocking signal fired — the caller
	// returns errBlockingFindings (exit 2).
	block bool
	// mergeUnavailable is true when the merged report is empty/whitespace
	// AND no per-LLM review flagged a blocking finding: the run can't
	// produce a report and isn't blocking, so the caller returns a
	// tool-failure error (exit 1), not the gate sentinel.
	mergeUnavailable bool
}

// decideExitGate computes the review exit gate from the merged report and
// the per-LLM results. The per-LLM blocking scan is computed FIRST and
// UNCONDITIONALLY, so an empty merged report (merger timeout, rate-limit,
// SaveMerged failure) still trips the gate when any per-LLM review flagged
// a Critical/Major finding. That ordering is the invariant that stops a
// merge-step failure from collapsing exit-2 (blocked) into exit-1 (which
// pre-commit hooks treat as "let the commit through"). Pure — no I/O — so
// TestDecideExitGate can exercise all four cases directly.
func decideExitGate(mergedContent string, results []multi.ReviewResult) gateOutcome {
	perLLMBlocking := anyPerLLMHasBlocking(results)
	if strings.TrimSpace(mergedContent) == "" {
		return gateOutcome{block: perLLMBlocking, mergeUnavailable: !perLLMBlocking}
	}
	return gateOutcome{block: review.IsBlockingMarkdown(mergedContent) || perLLMBlocking}
}

// anyPerLLMHasBlocking runs review.IsBlockingMarkdown against each per-LLM
// Output, BEFORE the 8 KB truncation that BuildMergeInput applies. If any
// reviewer raised a Critical / Major finding (or BLOCK MERGE / REQUEST
// CHANGES verdict), the gate fires even if the truncated merger input
// dropped it.
func anyPerLLMHasBlocking(results []multi.ReviewResult) bool {
	for _, r := range results {
		if r.Output == "" {
			continue
		}
		if review.IsBlockingMarkdown(r.Output) {
			return true
		}
	}
	return false
}

// warnIgnoredFlags emits stderr notes when v0-only flags slip into a
// multi-LLM run. Better to be noisy than to silently produce reports
// that don't reflect what the user asked for.
func warnIgnoredFlags(sf *sharedFlags) {
	if sf.jsonOut {
		fmt.Fprintln(os.Stderr, "Warning: --json is ignored on the review path (the merged report is markdown); it still applies to audit and bench.")
	}
	if sf.minSeverity != "" {
		fmt.Fprintln(os.Stderr, "Warning: --min-severity is ignored on the review path (filtering happens inside the merge prompt); it applies to audit.")
	}
	if sf.maxFindings != 0 {
		fmt.Fprintln(os.Stderr, "Warning: --max-findings is ignored on the review path (trimming happens inside the merge prompt); it applies to audit.")
	}
}

// validateMergeWith fails fast on a typo'd --merge-with so the user
// doesn't silently get the auto-fallback agent and assume their flag
// took effect.
func validateMergeWith(sf *sharedFlags, active []cli.LLM) error {
	if sf.mergeWith == "" || sf.mergeWith == "auto" {
		return nil
	}
	for _, llm := range active {
		if llm.Name == sf.mergeWith {
			return nil
		}
	}
	names := make([]string, len(active))
	for i, l := range active {
		names[i] = l.Name
	}
	return fmt.Errorf("--merge-with %q not in active set %v", sf.mergeWith, names)
}

// printAgentRoster prints "Reviewing <branch> (<short-sha>) with N agents"
// plus one line per agent showing model name and CLI version, plus a
// discoverability hint when an authed agent is disabled in config.
//
// Including branch and commit on the first line tells the user *what*
// they're reviewing without scrolling — important when the same shell
// is jumping between checkouts and `local-review review` is the first
// thing they see after a `git switch`.
func printAgentRoster(active []cli.LLM, configDisabled, sunsetDropped []string, cfg config.Config, branch, commit string) {
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	if branch != "" && short != "" {
		fmt.Printf("Reviewing %s (%s) with %d LLM%s...\n", branch, short, len(active), pluralS(len(active)))
	} else {
		fmt.Printf("Running review with %d LLM%s...\n", len(active), pluralS(len(active)))
	}
	for _, llm := range active {
		model := modelFor(llm.Name, cfg)
		// Surface the per-agent timeout on the roster line so users
		// know up-front "if this agent doesn't return within Ns,
		// we'll mark it failed." Pre-v0.7 the timeout was invisible
		// until the failure line displayed it as part of the hint;
		// having it visible at run-start lets users notice "wait, my
		// timeout is still 120s — that's why claude on a big diff
		// keeps failing" before they spend tokens on the run.
		timeout := llm.TimeoutSec
		if timeout == 0 {
			timeout = cli.DefaultTimeoutSec
		}
		if model != "" {
			// agent_<model> reads as a single identifier (think
			// docker image names) so users see "what model is
			// running" at a glance.
			fmt.Printf("  • %s_%s (CLI v%s) | timeout: %ds\n", llm.Name, model, llm.Version, timeout)
		} else {
			// No model pinned → the invoker doesn't pass --model and
			// the vendor CLI picks its own current default. We can't
			// know which model that is without probing the CLI (no
			// portable way), so we say so explicitly and tell the
			// user how to take control. Pre-fix this said "model: CLI
			// default" which the user reported as a non-answer.
			fmt.Printf("  • %s (CLI v%s) | timeout: %ds — using vendor's default model; pin via `llms.%s.model:`\n", llm.Name, llm.Version, timeout, llm.Name)
		}
	}
	if len(configDisabled) > 0 {
		fmt.Printf("  (skipped: %s — disabled in config; pass `--only %s` to run anyway)\n",
			strings.Join(configDisabled, ", "), strings.Join(configDisabled, ","))
	}
	for _, name := range sunsetDropped {
		fmt.Printf("  (skipped: %s — past manufacturer sunset %s; set `llms.%s.force_after_sunset: true` to override)\n",
			name, cli.AgentSunsetDate(name).Format("2006-01-02"), name)
	}
	fmt.Println()
}

// runPreflightProbe issues a tiny `Reply OK` call to each LLM with a
// short per-LLM timeout, renders a ✓/✗ readiness block to the user,
// and returns the subset of LLMs that passed. Empty return ⇒ every
// LLM failed; the caller handles the all-failed error message.
//
// Rendering posture: prints to stdout (not stderr) because the
// readiness block is informational and sequenced before the real
// review's progress lines — the user reads them top-to-bottom as a
// single narrative. stderr would interleave on terminals that line-
// buffer the two streams differently.
//
// Glyph choice: the success glyph mirrors the per-LLM completion
// line later in the run (`✓` is the same character cli.ClassifyExit
// avoids and the merged-report footer prints), so a user scanning
// terminal output for "did all the LLMs work?" sees the same
// visual at both moments. The `✗` glyph is similarly consistent
// with the per-LLM failure line.
//
// Goroutine + timeout posture: ProbeAll fans out internally with
// per-LLM deadlines bounded by `timeout`. The caller resolves
// the actual value (--preflight-timeout flag overrides
// cli.DefaultProbeTimeout); runPreflightProbe just receives it
// pre-resolved so the readiness-block render can show the
// CONFIGURED value, not a hard-coded default that would lie when
// the user passed --preflight-timeout. Empirically the default
// 10s is generous for a healthy CLI's startup + minimal response;
// shortening it would catch slow but recoverable CLIs as
// false-positive timeouts.
func runPreflightProbe(ctx context.Context, active []cli.LLM, timeout time.Duration, strict bool) []cli.LLM {
	fmt.Println("Pre-flight (probing auth + capacity):")
	probeStart := time.Now()
	results := cli.ProbeAll(ctx, active, timeout, strict)
	probeDuration := time.Since(probeStart)

	// Build a quick name → index map so we can filter `active`
	// down to the ready subset without two slice walks. Roster
	// order is preserved because ProbeAll returns in roster
	// order and we iterate the same slice.
	readyByName := make(map[string]bool, len(results))
	for _, r := range results {
		// Width-pad the agent name to the longest in the
		// roster so the glyphs line up in a column — read-at-
		// a-glance UX, same as the per-LLM completion lines.
		// printf's %-Ns padding does this in one format call.
		name := r.LLM
		line := formatProbeLine(r, timeout)
		fmt.Println(line)
		if r.Status == cli.ProbeReady {
			readyByName[name] = true
		}
	}
	fmt.Printf("Probed %d LLM%s in %s.\n\n", len(active), pluralS(len(active)), probeDuration.Round(10*time.Millisecond))

	// Filter active → ready subset, preserving roster order.
	ready := make([]cli.LLM, 0, len(active))
	for _, l := range active {
		if readyByName[l.Name] {
			ready = append(ready, l)
		}
	}
	return ready
}

// formatProbeLine renders one row of the pre-flight readiness
// block as a single string, ready for fmt.Println. Extracted from
// the render loop so the rendering rules — especially the v0.10.8
// "no diagnostic captured" hint and the v0.10.6 vendor-message
// surfacing — can be unit-tested without needing to capture stdout.
//
// The padding width (8 chars on the agent name) matches the
// per-LLM completion lines later in the run, so the readiness
// block and the completion summary line up visually as a column.
// All four ProbeStatus cases are exhaustive — unknown statuses
// would render the empty default, which is harmless but tests
// pin the exhaustiveness to catch a future enum addition that
// forgets to update this function.
func formatProbeLine(r cli.ProbeResult, timeout time.Duration) string {
	name := r.LLM
	switch r.Status {
	case cli.ProbeReady:
		return fmt.Sprintf("  %-8s ✓ (%s)", name, r.Duration.Round(10*time.Millisecond))
	case cli.ProbeTimeout:
		// v0.10.6: vendor message present → surface it (the
		// "timeout after Ns — <vendor>" shape produced by
		// Probe via probeTimeoutErr).
		// v0.10.8: vendor message absent → append the
		// "no diagnostic captured" hint so the user knows the
		// difference between "vendor told us X" and "we got
		// nothing." The hint also points at the most common
		// fix (raise --preflight-timeout) so cold-start cases
		// don't require digging through `doctor` output first.
		if r.Err != nil && strings.Contains(r.Err.Error(), "timeout after") {
			return fmt.Sprintf("  %-8s ✗ %s", name, singleLine(r.Err.Error()))
		}
		return fmt.Sprintf("  %-8s ✗ timeout after %s (no diagnostic captured — run `local-review doctor`, or raise --preflight-timeout)", name, timeout)
	case cli.ProbeCanceled:
		// Distinct glyph (⊘) so the user reads "I pressed
		// Ctrl+C" rather than "vendor timed out" at a glance.
		// The runner short-circuits on ctx.Err() right after
		// the render loop, so this branch is mostly for the
		// user's eyes mid-cancel.
		return fmt.Sprintf("  %-8s ⊘ canceled", name)
	case cli.ProbeError:
		// Surface the vendor's own error message (single line
		// — multi-line stderr tails belong in the real-review
		// path, not the readiness block).
		return fmt.Sprintf("  %-8s ✗ %s", name, singleLine(r.Err.Error()))
	default:
		// Unknown future ProbeStatus: render the agent with
		// no glyph so the line still appears, surfacing the
		// integer status via String() for diagnosis.
		return fmt.Sprintf("  %-8s %s", name, r.Status)
	}
}

// singleLine collapses any internal newline / carriage-return
// runs to single spaces, then trims. Used by the readiness block
// to keep vendor error messages on one line — a multi-line stderr
// tail in the readiness column would break the ✓/✗ alignment.
func singleLine(s string) string {
	// Replace any \r\n / \n / \r with a space, then collapse
	// runs of whitespace.
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(spaceCollapse.ReplaceAllString(s, " "))
}

// Reused by singleLine. Compiled once at init so the readiness-
// block render loop doesn't pay a regex-compile per LLM.
var spaceCollapse = regexp.MustCompile(`\s+`)

func modelFor(name string, cfg config.Config) string {
	if c, ok := cfg.LLMs[name]; ok {
		return c.Model
	}
	return ""
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// aggregateTokens returns the sum of input+output tokens across all
// per-LLM reviews plus the merge step's own usage. Used by the
// closing-line "~Nk tokens" summary. Returns 0 when nothing was
// reported — the closing-line caller checks for this and omits the
// "tokens" suffix entirely so users don't see "0 tokens" and assume
// the call was free.
func aggregateTokens(results []multi.ReviewResult, mergeTokens cli.TokenUsage) int {
	total := mergeTokens.Total()
	for _, r := range results {
		total += r.Tokens.Total()
	}
	return total
}

// humanTokens formats a token count for the per-LLM and closing
// summary lines. Three bands:
//   - Below 1000:   raw integer (e.g. "456") because at small scales
//     "0.5k" hides meaningful precision.
//   - 1k to 99,999: "k" values truncated to one decimal, dropping
//     ".0" for whole thousands (e.g. "1k", "1.2k",
//     "12.3k", "15k", "99.9k") because cost-sensitive
//     users need tens-of-tokens resolution here, where
//     the gap between 4.5k and 5.0k is real money on a
//     paid tier.
//   - 100k and up:  rounded "k" (e.g. "120k") because at six figures
//     the decimal is just noise.
//
// Truncation (not rounding) in the middle band is deliberate so
// 99,999 renders as "99.9k" rather than "100.0k" — band-crossing
// would both look wrong (the band cap is supposed to be 99.9k)
// and overstate usage by ~1 token at the boundary. We'd rather
// under-report by <100 tokens than tip over.
//
// CodeRabbit's auto-fix proposed a simpler "always divide by 1000,
// drop .0 for whole thousands" form. Rejected: that path renders
// 999 as "1.0k" (rounding hides the sub-1k case) and 156000 as
// "156.0k" (the trailing .0 is noise at six figures). The three-band
// shape covers the same docs example (12300 → "12.3k") without
// either regression.
func humanTokens(n int) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 100_000 {
		// Integer math truncates toward zero — no rounding-up across
		// the band boundary. tenths = floor(n/100); whole = tenths/10;
		// frac = tenths%10 ∈ [0,9].
		tenths := n / 100
		whole := tenths / 10
		frac := tenths % 10
		if frac == 0 {
			return fmt.Sprintf("%dk", whole)
		}
		return fmt.Sprintf("%d.%dk", whole, frac)
	}
	rounded := (n + 500) / 1000
	return fmt.Sprintf("%dk", rounded)
}

// formatTokenSuffix returns " · 12.3k in / 4.5k out" for the per-LLM
// completion line, or " · 12.3k total" when only the combined total
// is known (codex's legacy stdout shape pre-v0.128 doesn't split
// input vs output — formatting as "X in / 0 out" would mislead
// users into thinking the model returned nothing). Returns "" when
// usage wasn't reported at all so we don't print "0 in / 0 out".
func formatTokenSuffix(u cli.TokenUsage) string {
	if u.IsZero() {
		return ""
	}
	if u.TotalOnly {
		return fmt.Sprintf(" · %s total", humanTokens(u.Total()))
	}
	return fmt.Sprintf(" · %s in / %s out", humanTokens(u.InputTokens), humanTokens(u.OutputTokens))
}

// resolveCommitBranch resolves the commit hash and branch name for the
// current invocation. In detached-HEAD environments (CI checkouts,
// `git checkout <tag>`, bisect) git returns "HEAD" as the branch name
// — we fall back to a commit-derived synthetic branch ("detached-<sha>")
// instead of failing, so multi-LLM still works there. The previous
// behavior errored out, regressing the v0 single-LLM path that worked
// fine in detached HEAD.
func resolveCommitBranch(mode git.Mode, ref string) (string, string, error) {
	commit := git.CurrentCommit()
	branch := git.CurrentBranch()

	if mode == git.ModeCommit && ref != "" {
		// Always resolve, even for full 40-char SHAs — `git rev-parse
		// --short` normalizes everything (branch name, tag, short hash,
		// full hash) to the same canonical short form, so the same
		// commit can't end up under two different storage keys when
		// the user invokes `local-review commit abc1234` once and
		// `local-review commit <full-40-char-sha>` another time.
		resolved := git.ResolveRef(ref)
		if resolved == "" {
			return "", "", fmt.Errorf("failed to resolve ref '%s' to commit hash", ref)
		}
		commit = resolved
	}

	if commit == "HEAD" {
		return "", "", fmt.Errorf("failed to get current commit (git rev-parse failed)")
	}
	if s := git.SanitizeCommit(commit); s == "" || len(s) < 6 {
		return "", "", fmt.Errorf("invalid commit hash '%s' (sanitized to '%s')", commit, s)
	}

	// Detached HEAD ('HEAD') or git failure ('unknown'). Don't refuse
	// to run — synthesize a stable per-commit name so storage stays
	// organized and reviews from different detached commits don't
	// collide under one "HEAD" or "unknown" directory.
	branch = syntheticDetachedBranch(branch, commit)

	return commit, branch, nil
}

// syntheticDetachedBranch returns a per-commit fallback name when git
// reports a detached state ("HEAD") or a hard failure ("unknown").
// Real branch names pass through unchanged.
func syntheticDetachedBranch(branch, commit string) string {
	if branch != "HEAD" && branch != "unknown" {
		return branch
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	return "detached-" + short
}

// runMode classifies a multi-LLM review by how many agents produced
// output the merger will actually consume. The framing ("Merged review"
// vs "Single-LLM result") and the degraded-run warning hinge on this
// distinction — see classifyRunMode for the rules.
type runMode int

const (
	runModeMerge    runMode = iota // ≥2 outputs; a real cross-model merge
	runModeDegraded                // exactly 1 output out of ≥2 agents; no consensus
	runModeSolo                    // exactly 1 output out of 1 agent; user chose --only, expected
)

// classifyRunModeFromGate picks the framing for the merge step based
// on how many agents produced output the merger will actually see —
// derived from a GateDecision so we don't traverse the result set a
// second time after DecideGate already walked it once. Counts non-
// blank Output (gate.WithOutput, matching BuildMergeInput's filter
// via HasMergeableOutput), not Error == nil:
// a run where the LLM succeeded but SaveReview failed has Error != nil
// yet Output != "", and the merger will still consume that review, so
// the framing should reflect that. Pre-consolidation classifyRunMode
// derived `mergeable` itself via a fresh CountWithOutput call — the
// extra walk wasn't a correctness bug but did make the dual-metric
// pattern that audit/tech-debt.md flagged on runner.go:156 harder to
// audit (each call site computed counts itself, each had to be kept
// consistent independently).
//
// The merger still runs in every case (it's what produces the structured
// Recommendation line the gate reads), but the user-facing language is
// different so nobody mistakes a single-LLM fallback for cross-model
// consensus. Zero-output runs never reach here — the caller short-
// circuits with an "all LLMs failed" error.
func classifyRunModeFromGate(g multi.GateDecision) runMode {
	switch {
	case g.WithOutput == 1 && g.Total > 1:
		return runModeDegraded
	case g.WithOutput == 1 && g.Total == 1:
		return runModeSolo
	default:
		return runModeMerge
	}
}

// mergeAndPrint runs the merge LLM, saves the merged report, and prints
// it to stdout so users see findings without `cat`-ing a file. Returns
// the saved path and the merged content; both are "" on skip/failure.
// The content is returned so the caller can run the blocking-finding
// gate without re-reading from disk.
//
// Takes the caller's GateDecision rather than recomputing — pre-
// consolidation this function called classifyRunMode(results) and
// CountWithOutput(results) independently, each walking the slice
// again. Same metrics, same intent, two extra walks per invocation
// and (more importantly) two extra independent observers of the
// result set the dual-metric-drift bugs were about.
func mergeAndPrint(ctx context.Context, cfg config.Config, sf *sharedFlags, active []cli.LLM, results []multi.ReviewResult, gate multi.GateDecision, storage *multi.ReviewStorage, commit, branch string, metadata *multi.Metadata) (mergedPath, mergedContent string, mergeTokens cli.TokenUsage) {
	mode := classifyRunModeFromGate(gate)

	switch mode {
	case runModeDegraded:
		fmt.Fprintf(os.Stderr, "⚠ Only %d of %d LLMs produced output — review is single-source, no cross-model consensus.\n", gate.WithOutput, gate.Total)
		fmt.Println("Reformatting single review...")
	case runModeSolo:
		fmt.Println("Formatting review...")
	default:
		fmt.Println("Merging reviews...")
	}

	preferred := cfg.Merge.PreferredLLM
	if sf.mergeWith != "" {
		preferred = sf.mergeWith
	}
	mergeLLM := selectMergeLLM(results, active, preferred)
	if mergeLLM == nil {
		fmt.Println("Warning: no LLM available for merging (skipping merge)")
		metadata.Merge.Status = "skipped"
		return "", "", cli.TokenUsage{}
	}
	switch mode {
	case runModeDegraded:
		fmt.Printf("Using %s to reformat the surviving review...\n", mergeLLM.Name)
	case runModeSolo:
		fmt.Printf("Using %s to format the review...\n", mergeLLM.Name)
	default:
		fmt.Printf("Using %s for merge...\n", mergeLLM.Name)
	}

	merger, err := multi.NewMerger(*mergeLLM)
	if err != nil {
		fmt.Printf("Warning: failed to create merger: %v\n", err)
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		return "", "", cli.TokenUsage{}
	}

	mergeInput := multi.BuildMergeInput(results, cfg.Merge.ConsensusThreshold)
	// pickAgents → agentselect (applyConfig) already enforces non-zero
	// TimeoutSec on every active LLM. Belt-and-suspenders: keep the explicit
	// fallback so a future caller that bypasses pickAgents can't silently end up
	// with `time.Duration(0)` = no timeout = a hung merge LLM hanging
	// the whole review.
	mergeTimeout := time.Duration(mergeLLM.TimeoutSec) * time.Second
	// Negative timeouts (e.g., a `timeout_seconds: -1` typo) would otherwise
	// produce an already-expired context that cancels the merge instantly.
	if mergeLLM.TimeoutSec <= 0 {
		mergeTimeout = time.Duration(cli.DefaultTimeoutSec) * time.Second
	}
	mergeCtx, cancel := context.WithTimeout(ctx, mergeTimeout)
	defer cancel()

	mergeStart := time.Now()
	merged, mergeTokens, err := merger.Merge(mergeCtx, mergeInput)
	mergeDuration := time.Since(mergeStart)

	if err != nil {
		fmt.Printf("Warning: merge failed: %v\n", err)
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		return "", "", cli.TokenUsage{}
	}

	savedPath, err := storage.SaveMerged(branch, commit, merged)
	if err != nil {
		// SaveMerged failed (read-only mount, full disk, permission
		// denied on .local-review/) — but the merger DID produce
		// content, and the gate runs against that content. Pre-fix
		// we returned ("", "") and the caller bailed with "merge
		// step produced no output; gate did not run", which collapsed
		// to a tool error (exit 1) — pre-commit hooks treat exit 1
		// as "let the commit through" and miss the blocking finding
		// the merger had already identified.
		//
		// The right move is: warn loud about the persistence failure,
		// print the findings inline so the user can see them, and
		// return the merged content with an empty path. The caller
		// runs the gate on the content; the closing-line "Merged
		// report: <path>" branch already guards on non-empty path,
		// so it silently omits.
		fmt.Fprintf(os.Stderr, "Warning: failed to save merged review: %v\n", err)
		fmt.Fprintln(os.Stderr, "         findings shown below; the gate will still run, but the report is not persisted.")
		fmt.Fprintln(os.Stderr, "         re-run after fixing the storage issue if you want a saved copy.")
		metadata.Merge.LLM = mergeLLM.Name
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		metadata.Merge.DurationMs = mergeDuration.Milliseconds()
		metadata.Merge.InputTokens = mergeTokens.InputTokens
		metadata.Merge.OutputTokens = mergeTokens.OutputTokens
		metadata.Merge.TotalOnlyTokens = mergeTokens.TotalOnly
		fmt.Println("─── Findings (not persisted) ───")
		fmt.Println(merged)
		fmt.Println("─── End ───")
		return "", merged, mergeTokens
	}

	metadata.Merge.LLM = mergeLLM.Name
	metadata.Merge.Status = "success"
	metadata.Merge.DurationMs = mergeDuration.Milliseconds()
	metadata.Merge.InputTokens = mergeTokens.InputTokens
	metadata.Merge.OutputTokens = mergeTokens.OutputTokens
	metadata.Merge.TotalOnlyTokens = mergeTokens.TotalOnly

	switch mode {
	case runModeDegraded:
		fmt.Printf("✓ Reformatted (%.1fs) — single-LLM, no merge\n\n", mergeDuration.Seconds())
	case runModeSolo:
		fmt.Printf("✓ Formatted review (%.1fs)\n\n", mergeDuration.Seconds())
	default:
		fmt.Printf("✓ Merged review (%.1fs)\n\n", mergeDuration.Seconds())
	}

	// Print the merged review inline so users see findings without cat.
	fmt.Println("─── Findings ───")
	fmt.Println(merged)
	fmt.Println("─── End ───")

	return savedPath, merged, mergeTokens
}

// --- helpers extracted from the deleted multi.go --------------------

func formatDiffForLLM(diffs []git.Diff) string {
	var b strings.Builder
	for _, d := range diffs {
		fmt.Fprintf(&b, "## File: %s\n", d.Path)
		for _, h := range d.Hunks {
			b.WriteString(h.Header)
			b.WriteString("\n")
			b.WriteString(h.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func buildMetadata(commit, branch string, results []multi.ReviewResult, startTime time.Time) *multi.Metadata {
	meta := &multi.Metadata{
		Commit:    commit,
		Branch:    branch,
		Timestamp: startTime,
		Reviews:   make([]multi.ReviewMeta, len(results)),
	}
	for i, r := range results {
		status := "success"
		errMsg := ""
		if r.Error != nil {
			status = "failed"
			errMsg = r.Error.Error()
		}
		meta.Reviews[i] = multi.ReviewMeta{
			LLM:             r.LLM,
			Version:         r.Version,
			Mode:            r.Mode,
			Status:          status,
			DurationMs:      r.Duration.Milliseconds(),
			OutputFile:      r.FilePath,
			Error:           errMsg,
			InputTokens:     r.Tokens.InputTokens,
			OutputTokens:    r.Tokens.OutputTokens,
			TotalOnlyTokens: r.Tokens.TotalOnly,
		}
	}
	return meta
}

// sortByRoster returns results re-ordered to match the configured
// roster order in `available`. Used after the streaming channel
// drains to restore determinism for downstream consumers
// (BuildMergeInput, buildMetadata, selectMergeLLM): identical runs
// must produce identical merge prompts and metadata files
// regardless of which agent happened to finish first.
//
// Stable: agents present in `results` but absent from `available`
// (defensive — shouldn't happen in practice since active drives
// both) keep their relative completion-order position at the end.
func sortByRoster(results []multi.ReviewResult, available []cli.LLM) []multi.ReviewResult {
	rank := make(map[string]int, len(available))
	for i, llm := range available {
		rank[llm.Name] = i
	}
	sorted := make([]multi.ReviewResult, len(results))
	copy(sorted, results)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, oki := rank[sorted[i].LLM]
		rj, okj := rank[sorted[j].LLM]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		default:
			return false
		}
	})
	return sorted
}

// selectMergeLLM picks which agent merges findings. Priority:
// caller-preferred → auto (claude > codex > copilot > gemini) → first
// eligible.
//
// Order rationale: claude and codex are the proven merge workhorses
// (since v0.5). copilot ranks next — it produces clean mergeable
// output (unlike antigravity) and is a current, supported agent.
// gemini is LAST because it's deprecated (Google stops serving it
// 2026-06-18); a default run shouldn't lean on a tool that's about to
// go away, even though it's the nominally "free" option. (This is
// only the auto fallback — `--merge-with`/`merge.preferred_llm`
// override it, and it merely picks the merger, not the reviewer set.)
//
// antigravity is intentionally absent from the auto chain: it never
// enters the review fan-out (cli.IsReviewCapable == false) so it can't
// produce mergeable output, and the merge prompt would hit the same
// agentic `--print` failure that excluded it as a reviewer.
//
// Eligibility is "produced mergeable output" (matching CountWithOutput),
// not "Error == nil". Pre-fix a SaveReview-failed-with-output run
// (Error != nil, Output != "") was excluded from merger candidates, so
// when *all* saves failed selectMergeLLM returned nil and the gate
// skipped — a tool error instead of exit 2, which pre-commit hooks
// silently ignore. The merge step itself only needs the LLM's CLI to
// work for the merge prompt; whether an earlier per-LLM SaveReview
// happened to succeed is unrelated.
func selectMergeLLM(results []multi.ReviewResult, available []cli.LLM, preferred string) *cli.LLM {
	eligible := make(map[string]cli.LLM)
	for _, llm := range available {
		for _, r := range results {
			if r.LLM == llm.Name && multi.HasMergeableOutput(r) {
				eligible[llm.Name] = llm
				break
			}
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	if preferred != "" && preferred != "auto" {
		if llm, ok := eligible[preferred]; ok {
			return &llm
		}
	}
	for _, name := range []string{"claude", "codex", "copilot", "gemini"} {
		if llm, ok := eligible[name]; ok {
			return &llm
		}
	}
	// Final fallback: pick the first eligible agent in *roster* order,
	// not results order. With v0.6.7 streaming, `results` is in
	// completion order — iterating it for the fallback would make
	// merge-LLM selection timing-dependent for custom-named agents
	// (where the auto claude>codex>copilot>gemini chain doesn't match).
	// Roster order (`available`) is deterministic across runs.
	for _, llm := range available {
		if l, ok := eligible[llm.Name]; ok {
			return &l
		}
	}
	return nil
}

// selectPromptPack picks the language pack for this multi-LLM run.
// Mirrors the single-LLM logic in internal/review/review.go: an
// explicit review.prompt_pack in config wins, otherwise auto-detect
// from the dominant language across the diff paths. The returned
// string is the resolved pack content (embedded body plus any
// cfg.Prompts.PackDir override and cfg.Prompts.Prepend/Append, per
// issue #55); the markdown-output override is added by each invoker
// in internal/cli/invoker.go.
func selectPromptPack(cfg config.Config, diffs []git.Diff) (string, error) {
	packID := cfg.Review.PromptPack
	if packID == "" {
		paths := make([]string, len(diffs))
		for i, d := range diffs {
			paths[i] = d.Path
		}
		packID = lang.Dominant(paths)
	}
	pack, err := prompts.Resolve(packID, prompts.ResolveOptions{
		PackDir: cfg.Prompts.PackDir,
		Prepend: cfg.Prompts.Prepend,
		Append:  cfg.Prompts.Append,
	})
	if err != nil {
		return "", fmt.Errorf("load prompt pack %q: %w", packID, err)
	}
	return pack.Content, nil
}
