package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/multi"
	"github.com/mshykov/local-review/internal/output"
	"github.com/mshykov/local-review/internal/review"
)

// pickAgents returns the LLMs to run for this invocation. Wraps
// selectAgents with a real cli.DetectAll() + classify() call; tests
// drive selectAgents directly with synthetic input.
func pickAgents(cfg config.Config, sf *sharedFlags) (active []cli.LLM, configDisabled []string) {
	detected := cli.DetectAll()
	ready := make(map[string]bool, len(detected))
	for _, llm := range detected {
		status, _ := classify(llm)
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
			active = append(active, withTimeout(llm, cfg))
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
		active = append(active, withTimeout(llm, cfg))
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

func withTimeout(llm cli.LLM, cfg config.Config) cli.LLM {
	if c, ok := cfg.LLMs[llm.Name]; ok && c.TimeoutSec > 0 {
		llm.TimeoutSec = c.TimeoutSec
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
	if len(diffs) == 0 {
		fmt.Println("No changes to review.")
		return nil
	}
	diffStr := formatDiffForLLM(diffs)

	commit, branch, err := resolveCommitBranch(mode, ref)
	if err != nil {
		return err
	}

	startTime := time.Now()
	storage := multi.NewStorage(cfg.Storage.BasePath)
	orch := multi.NewOrchestrator(active, storage)

	results, err := orch.RunParallel(ctx, diffStr, commit, branch)
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

	// Restore the pre-commit gate broken by the v0.5 multi-default flip.
	// Single-LLM has structured findings → exits 2 cleanly. Multi-LLM
	// merger returns markdown, so we look for non-empty severity
	// sections to decide whether to fail the commit.
	if mergedHasBlocking(mergedContent) {
		os.Exit(2)
	}
	return nil
}

// mergedHasBlocking returns true when the merged markdown report has a
// "## Critical Issues" or "## Major Issues" section with at least one
// finding (i.e., not just the placeholder *(None)* marker the merge
// prompt template emits for empty buckets). Used to keep `local-review
// staged` exiting 2 in a pre-commit hook even though the multi-LLM
// merger returns prose, not structured findings.
func mergedHasBlocking(markdown string) bool {
	if markdown == "" {
		return false
	}
	return sectionHasContent(markdown, "Critical Issues") ||
		sectionHasContent(markdown, "Major Issues")
}

// sectionHasContent returns true when a "## <name>" heading is followed
// by non-placeholder body text before the next "## " heading. Whitespace
// and the common *(None)* placeholder line don't count as content.
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
		if line == "" {
			continue
		}
		if line == "*(None)*" || strings.HasPrefix(line, "*(") {
			// Placeholder lines like "*(None)*" or
			// "*(Block merge — will break production...)*"
			continue
		}
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
// current invocation. Mirrors the validation the old multi command did
// to catch detached-HEAD and unresolvable-ref cases.
func resolveCommitBranch(mode git.Mode, ref string) (string, string, error) {
	commit := git.CurrentCommit()
	branch := git.CurrentBranch()

	if mode == git.ModeCommit && ref != "" {
		commit = ref
		if len(commit) < 40 {
			resolved := git.ResolveRef(ref)
			if resolved == "" {
				return "", "", fmt.Errorf("failed to resolve ref '%s' to commit hash", ref)
			}
			commit = resolved
		}
	}

	if commit == "HEAD" {
		return "", "", fmt.Errorf("failed to get current commit (git rev-parse failed)")
	}
	if branch == "unknown" {
		return "", "", fmt.Errorf("failed to get current branch (detached HEAD or git error)")
	}
	if s := git.SanitizeCommit(commit); s == "" || len(s) < 6 {
		return "", "", fmt.Errorf("invalid commit hash '%s' (sanitized to '%s')", commit, s)
	}
	return commit, branch, nil
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
	// withTimeout in pickAgents already enforces a non-zero TimeoutSec,
	// so this is purely a defense for callers that bypass that path.
	mergeTimeout := time.Duration(mergeLLM.TimeoutSec) * time.Second
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
		output.WriteText(os.Stdout, rep)
	}

	if hasBlocking(rep) {
		os.Exit(2)
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
