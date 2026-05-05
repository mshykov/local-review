package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/lang"
	"github.com/mshykov/local-review/internal/multi"
	"github.com/mshykov/local-review/internal/output"
	"github.com/mshykov/local-review/internal/prompts"
	"github.com/mshykov/local-review/internal/review"
)

// errBlockingFindings signals that the review surfaced major/critical
// findings — pre-commit hooks need exit code 2. main() translates this
// to os.Exit(2) AFTER cobra returns, so all deferred cleanup still runs.
var errBlockingFindings = errors.New("blocking findings present")

// pickAgents returns the LLMs to run for this invocation. Wraps
// selectAgents with a real cli.DetectAll() + classify() call; tests
// drive selectAgents directly with synthetic input.
func pickAgents(cfg config.Config, sf *sharedFlags) (active []cli.LLM, configDisabled []string) {
	// Honor cfg.LLMs[*].CLIPath when set — corporate / nix-store installs
	// at non-standard paths can override the default binary name.
	overrides := make(map[string]string, len(cfg.LLMs))
	for name, c := range cfg.LLMs {
		if c.CLIPath != "" {
			overrides[name] = c.CLIPath
		}
	}
	detected := cli.DetectAllWithOverrides(overrides)
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
	return selectAgents(detected, ready, cfg, sf)
}

// selectAgents picks which detected LLMs run, plus the names of any
// that were authed-but-disabled-in-config so the caller can show a
// discoverability hint. Decision tree, top-down:
//
//  1. If --only is set, that wins absolutely (overrides config disable).
//  2. An LLM is "active" only if its readiness map says so (caller
//     supplies; in production this comes from doctor's classify).
//  3. If config explicitly sets enabled:false, skip — but report it
//     separately so we can tell the user about the override path.
func selectAgents(detected []cli.LLM, ready map[string]bool, cfg config.Config, sf *sharedFlags) (active []cli.LLM, configDisabled []string) {
	if want := parseOnlyList(sf.only); len(want) > 0 {
		for _, llm := range detected {
			if !want[llm.Name] {
				continue
			}
			if !ready[llm.Name] {
				continue
			}
			active = append(active, applyConfig(llm, cfg))
		}
		return active, nil
	}

	for _, llm := range detected {
		if !ready[llm.Name] {
			continue
		}
		if c, ok := cfg.LLMs[llm.Name]; ok && c.Enabled != nil && !*c.Enabled {
			configDisabled = append(configDisabled, llm.Name)
			continue
		}
		active = append(active, applyConfig(llm, cfg))
	}
	return active, configDisabled
}

// parseOnlyList splits a comma-separated --only value into a set.
// Trims whitespace per element and drops empty entries so callers don't
// need a separate guard against `--only ""` or `--only " ,, "`.
func parseOnlyList(s string) map[string]bool {
	out := make(map[string]bool)
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = true
	}
	return out
}

// applyConfig threads per-agent config (model, timeout) onto the
// detected LLM struct so it reaches the invoker — without this the
// Detector returns name+path+version only and per-agent --*-model /
// timeout config is silently dropped on the floor.
//
// Renamed from withTimeout to reflect the broader scope; the function
// now owns "everything from cfg.LLMs[llm.Name] that the invoker needs".
func applyConfig(llm cli.LLM, cfg config.Config) cli.LLM {
	if c, ok := cfg.LLMs[llm.Name]; ok {
		if c.TimeoutSec > 0 {
			llm.TimeoutSec = c.TimeoutSec
		}
		if c.Model != "" {
			llm.Model = c.Model
		}
		// APIKey is already resolved from c.APIKeyEnv by
		// config.resolveAPIKeys() during Load(), so the value is
		// either the user's custom-env-var key or empty (in which
		// case the CLI's own auth flow takes over).
		if c.APIKey != "" {
			llm.APIKey = c.APIKey
		}
	}
	if llm.TimeoutSec == 0 {
		llm.TimeoutSec = 120
	}
	return llm
}

// runMultiLLMReview executes the parallel multi-LLM flow: print the
// agent roster, extract the diff, fan out reviews, merge findings, save
// to disk, and print the merged report to stdout.
func runMultiLLMReview(ctx context.Context, cfg config.Config, sf *sharedFlags, active []cli.LLM, configDisabled []string, mode git.Mode, ref string) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	warnIgnoredFlags(sf)
	if err := validateMergeWith(sf, active); err != nil {
		return err
	}
	printAgentRoster(active, configDisabled, cfg)

	diffs, err := git.Extract(mode, ref)
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

	commit, branch, err := resolveCommitBranch(mode, ref)
	if err != nil {
		return err
	}

	startTime := time.Now()
	storage := multi.NewStorage(cfg.Storage.BasePath)
	orch := multi.NewOrchestrator(active, storage)

	results, err := orch.RunParallel(ctx, systemPrompt, diffStr, commit, branch)
	if err != nil {
		return fmt.Errorf("run reviews: %w", err)
	}

	for i, r := range results {
		if r.Error != nil {
			fmt.Printf("[%d/%d] %s ✗ (%v)\n", i+1, len(results), r.LLM, r.Error)
		} else {
			fmt.Printf("[%d/%d] %s ✓ (%.1fs)\n", i+1, len(results), r.LLM, r.Duration.Seconds())
		}
	}
	fmt.Println()

	successCount := multi.CountSuccessful(results)
	metadata := buildMetadata(commit, branch, results, startTime)

	if successCount == 0 {
		metadata.Merge.Status = "skipped"
		_, _ = storage.SaveMetadata(branch, commit, metadata)
		return fmt.Errorf("all %d LLM reviews failed", len(results))
	}

	mergedPath, mergedContent := mergeAndPrint(ctx, cfg, sf, active, results, storage, commit, branch, metadata)
	if _, err := storage.SaveMetadata(branch, commit, metadata); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save metadata: %v\n", err)
	}

	fmt.Println()
	fmt.Printf("✓ %d/%d LLMs succeeded\n", successCount, len(results))
	if mergedPath != "" {
		fmt.Printf("Merged report: %s\n", mergedPath)
	}

	// Per-LLM reviews succeeded but the merge step didn't produce
	// content (mergeLLM unavailable, merger error, save failed, or the
	// merger returned only whitespace). Without a merged report the
	// blocking-finding gate can't run, and returning nil would silently
	// exit 0 — a pre-commit hook would treat the commit as clean even
	// though no gate ever fired. TrimSpace (not just == "") catches the
	// "merger returned `\n`" variant that codex flagged as bypassing the
	// v0.5.1 fix.
	if strings.TrimSpace(mergedContent) == "" {
		return fmt.Errorf("merge step produced no output; per-LLM reviews are saved under %s but the blocking-finding gate did not run", cfg.Storage.BasePath)
	}

	// Two independent signals trip the gate (any one is enough). False
	// positives are preferred over false negatives — over-blocking is
	// a re-run, under-blocking is a shipped bug.
	//
	//  1. The merged markdown — what the merger LLM concluded after
	//     seeing the (possibly-truncated) per-LLM reviews.
	//  2. Each per-LLM review's full Output — defends against the
	//     8 KB merger-input truncation hiding blocking findings past
	//     the cut. The merger only sees the first 8 KB of each
	//     reviewer; without this independent check, a verbose
	//     reviewer that puts a critical finding past byte 8000 would
	//     produce a false-clean exit. The on-disk per-LLM file has
	//     always had the full output; this just scans it before we
	//     decide.
	//
	// Returning the sentinel (rather than os.Exit) lets cobra and
	// main() unwind defers.
	if mergedHasBlocking(mergedContent) || anyPerLLMHasBlocking(results) {
		return errBlockingFindings
	}
	return nil
}

// anyPerLLMHasBlocking runs the same heuristic mergedHasBlocking uses
// against each per-LLM Output, BEFORE the 8 KB truncation that
// BuildMergeInput applies. If any reviewer raised a Critical / Major
// finding (or BLOCK MERGE / REQUEST CHANGES verdict), the gate fires
// even if the truncated merger input dropped it.
func anyPerLLMHasBlocking(results []multi.ReviewResult) bool {
	for _, r := range results {
		if r.Output == "" {
			continue
		}
		if mergedHasBlocking(r.Output) {
			return true
		}
	}
	return false
}

// mergedHasBlocking returns true when the merged markdown report
// indicates blocking findings. We use two independent signals so that
// LLM drift on one shape doesn't silently disable the gate:
//
//  1. The Recommendation line in the Summary block. The merge prompt
//     pins this to "BLOCK MERGE" / "REQUEST CHANGES" / "APPROVE" — both
//     of the first two count as blocking. Strongest signal because
//     it's an explicit decision the merger has already made.
//  2. Any non-placeholder content under a Critical / Major section
//     heading (with a few common heading variants). Backstop for the
//     case where the LLM forgets the Recommendation line but still
//     enumerates findings.
//
// Either signal independently trips the gate. False positives are
// preferred to false negatives — this is a security gate, the cost
// of over-blocking is a re-run, the cost of under-blocking is a
// shipped bug.
func mergedHasBlocking(markdown string) bool {
	if markdown == "" {
		return false
	}
	if recommendationIsBlocking(markdown) {
		return true
	}
	for _, name := range []string{
		"Critical Issues", "Critical issues", "CRITICAL ISSUES", "Critical",
		"Major Issues", "Major issues", "MAJOR ISSUES", "Major",
	} {
		if sectionHasContent(markdown, name) {
			return true
		}
	}
	return false
}

// recommendationRE matches the "**Recommendation**: <verdict>" line
// the merge prompt emits in the Summary block. Pre-compiled at
// package level so anyPerLLMHasBlocking + mergedHasBlocking don't
// pay regexp.MustCompile on every per-LLM output (one call per
// reviewer, but adds up in tests and on big PRs).
var recommendationRE = regexp.MustCompile(`(?im)^\s*-?\s*\**Recommendation\**\s*:\s*(.+?)\s*$`)

// recommendationIsBlocking parses the "**Recommendation**: <verdict>"
// line the merge prompt emits in the Summary block. Returns true when
// the verdict is BLOCK MERGE or REQUEST CHANGES (case-insensitive).
// APPROVE / unrecognized verdicts return false — the section-content
// backstop in mergedHasBlocking still runs.
func recommendationIsBlocking(markdown string) bool {
	m := recommendationRE.FindStringSubmatch(markdown)
	if m == nil {
		return false
	}
	verdict := strings.ToUpper(strings.Trim(m[1], "* `"))
	return strings.Contains(verdict, "BLOCK MERGE") || strings.Contains(verdict, "REQUEST CHANGES")
}

// sectionHasContent returns true when a "## <name>" heading has any
// real content before the next "## " heading. We skip blank lines, the
// italicized section descriptions the merge prompt template prescribes
// (`*(...)*`), and a small set of common "no findings" placeholder
// shapes the LLM sometimes uses (`*(None)*`, `*None.*`, `_None_`, bare
// `None.`).
//
// This is a security gate — false negatives let blocking findings
// through silently — so we lean toward false positives. If the LLM
// emits findings as bullets, prose, numbered lists, or tables, they
// all count.
func sectionHasContent(markdown, name string) bool {
	re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(name) + `\s*$`)
	loc := re.FindStringIndex(markdown)
	if loc == nil {
		return false
	}
	body := markdown[loc[1]:]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next]
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isNonePlaceholder(line) {
			continue
		}
		return true
	}
	return false
}

// isNonePlaceholder recognizes the empty-section markers the merge
// prompt template emits or that LLMs commonly substitute. Kept narrow
// on purpose — too lenient and it swallows real one-line findings.
func isNonePlaceholder(line string) bool {
	// `*(...)*` — italic parenthetical (section description or *(None)*)
	if strings.HasPrefix(line, "*(") && strings.HasSuffix(line, ")*") {
		return true
	}
	// Bare italic/underscored "None"/"None." with no surrounding content.
	switch strings.ToLower(line) {
	case "*none*", "*none.*", "_none_", "_none._", "none", "none.":
		return true
	}
	return false
}

// warnIgnoredFlags emits stderr notes when v0-only flags slip into a
// multi-LLM run. Better to be noisy than to silently produce reports
// that don't reflect what the user asked for.
func warnIgnoredFlags(sf *sharedFlags) {
	if sf.jsonOut {
		fmt.Fprintln(os.Stderr, "Warning: --json is only honored in single-LLM fallback (the merged report is markdown).")
	}
	if sf.minSeverity != "" {
		fmt.Fprintln(os.Stderr, "Warning: --min-severity is only honored in single-LLM fallback; multi-LLM filtering happens inside the merge prompt.")
	}
	if sf.maxFindings != 0 {
		fmt.Fprintln(os.Stderr, "Warning: --max-findings is only honored in single-LLM fallback; multi-LLM trims inside the merge prompt.")
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

// printAgentRoster prints "Running review with N agents" plus one line
// per agent showing model name and CLI version, plus a discoverability
// hint when an authed agent is disabled in config.
func printAgentRoster(active []cli.LLM, configDisabled []string, cfg config.Config) {
	fmt.Printf("Running review with %d LLM%s...\n", len(active), pluralS(len(active)))
	for _, llm := range active {
		model := modelFor(llm.Name, cfg)
		if model != "" {
			fmt.Printf("  • %s %s (CLI v%s)\n", llm.Name, model, llm.Version)
		} else {
			fmt.Printf("  • %s (CLI v%s)\n", llm.Name, llm.Version)
		}
	}
	if len(configDisabled) > 0 {
		fmt.Printf("  (skipped: %s — disabled in config; pass `--only %s` to run anyway)\n",
			strings.Join(configDisabled, ", "), strings.Join(configDisabled, ","))
	}
	fmt.Println()
}

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

// mergeAndPrint runs the merge LLM, saves the merged report, and prints
// it to stdout so users see findings without `cat`-ing a file. Returns
// the saved path and the merged content; both are "" on skip/failure.
// The content is returned so the caller can run the blocking-finding
// gate without re-reading from disk.
func mergeAndPrint(ctx context.Context, cfg config.Config, sf *sharedFlags, active []cli.LLM, results []multi.ReviewResult, storage *multi.ReviewStorage, commit, branch string, metadata *multi.Metadata) (string, string) {
	fmt.Println("Merging reviews...")

	preferred := cfg.Merge.PreferredLLM
	if sf.mergeWith != "" {
		preferred = sf.mergeWith
	}
	mergeLLM := selectMergeLLM(results, active, preferred)
	if mergeLLM == nil {
		fmt.Println("Warning: no LLM available for merging (skipping merge)")
		metadata.Merge.Status = "skipped"
		return "", ""
	}
	fmt.Printf("Using %s for merge...\n", mergeLLM.Name)

	merger, err := multi.NewMerger(*mergeLLM)
	if err != nil {
		fmt.Printf("Warning: failed to create merger: %v\n", err)
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		return "", ""
	}

	mergeInput := multi.BuildMergeInput(results, cfg.Merge.ConsensusThreshold)
	// pickAgents → applyConfig already enforces non-zero TimeoutSec on
	// every active LLM. Belt-and-suspenders: keep the explicit fallback
	// so a future caller that bypasses pickAgents can't silently end up
	// with `time.Duration(0)` = no timeout = a hung merge LLM hanging
	// the whole review.
	mergeTimeout := time.Duration(mergeLLM.TimeoutSec) * time.Second
	// Negative timeouts (e.g., a `timeout_sec: -1` typo) would otherwise
	// produce an already-expired context that cancels the merge instantly.
	if mergeLLM.TimeoutSec <= 0 {
		mergeTimeout = 120 * time.Second
	}
	mergeCtx, cancel := context.WithTimeout(ctx, mergeTimeout)
	defer cancel()

	mergeStart := time.Now()
	merged, err := merger.Merge(mergeCtx, mergeInput)
	mergeDuration := time.Since(mergeStart)

	if err != nil {
		fmt.Printf("Warning: merge failed: %v\n", err)
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		return "", ""
	}

	mergedPath, err := storage.SaveMerged(branch, commit, merged)
	if err != nil {
		fmt.Printf("Warning: failed to save merged review: %v\n", err)
		metadata.Merge.Status = "failed"
		metadata.Merge.Error = err.Error()
		return "", ""
	}

	metadata.Merge.LLM = mergeLLM.Name
	metadata.Merge.Status = "success"
	metadata.Merge.DurationMs = mergeDuration.Milliseconds()

	fmt.Printf("✓ Merged review (%.1fs)\n\n", mergeDuration.Seconds())

	// Print the merged review inline so users see findings without cat.
	fmt.Println("─── Findings ───")
	fmt.Println(merged)
	fmt.Println("─── End ───")

	return mergedPath, merged
}

// runSingleLLMFallback is the v0 path: hit the configured provider's
// chat-completions endpoint with a single review pass. Used when no LLM
// CLI is active.
func runSingleLLMFallback(ctx context.Context, cfg config.Config, sf *sharedFlags, mode git.Mode, ref string) error {
	r := review.New(cfg)
	rep, err := r.Run(ctx, mode, ref)
	if err != nil {
		return err
	}

	if sf.jsonOut {
		if err := output.WriteJSON(os.Stdout, rep); err != nil {
			return err
		}
	} else {
		if err := output.WriteText(os.Stdout, rep); err != nil {
			return err
		}
	}

	if hasBlocking(rep) {
		return errBlockingFindings
	}
	return nil
}

func hasBlocking(r review.Report) bool {
	for _, f := range r.Findings {
		if f.Severity >= review.SeverityMajor {
			return true
		}
	}
	return false
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
			LLM:        r.LLM,
			Version:    r.Version,
			Mode:       r.Mode,
			Status:     status,
			DurationMs: r.Duration.Milliseconds(),
			OutputFile: r.FilePath,
			Error:      errMsg,
		}
	}
	return meta
}

// selectMergeLLM picks which agent merges findings. Priority:
// caller-preferred → auto (claude > codex > gemini) → first successful.
func selectMergeLLM(results []multi.ReviewResult, available []cli.LLM, preferred string) *cli.LLM {
	successful := make(map[string]cli.LLM)
	for _, llm := range available {
		for _, r := range results {
			if r.LLM == llm.Name && r.Error == nil {
				successful[llm.Name] = llm
				break
			}
		}
	}
	if len(successful) == 0 {
		return nil
	}
	if preferred != "" && preferred != "auto" {
		if llm, ok := successful[preferred]; ok {
			return &llm
		}
	}
	for _, name := range []string{"claude", "codex", "gemini"} {
		if llm, ok := successful[name]; ok {
			return &llm
		}
	}
	for _, r := range results {
		if r.Error == nil {
			if llm, ok := successful[r.LLM]; ok {
				return &llm
			}
		}
	}
	return nil
}

// selectPromptPack picks the language pack for this multi-LLM run.
// Mirrors the single-LLM logic in internal/review/review.go: an
// explicit review.prompt_pack in config wins, otherwise auto-detect
// from the dominant language across the diff paths. The returned
// string is the embedded pack content; the markdown-output override
// is added by each invoker in internal/cli/invoker.go.
func selectPromptPack(cfg config.Config, diffs []git.Diff) (string, error) {
	packID := cfg.Review.PromptPack
	if packID == "" {
		paths := make([]string, len(diffs))
		for i, d := range diffs {
			paths[i] = d.Path
		}
		packID = lang.Dominant(paths)
	}
	pack, err := prompts.Get(packID)
	if err != nil {
		return "", fmt.Errorf("load prompt pack %q: %w", packID, err)
	}
	return pack, nil
}
