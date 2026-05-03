package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
	"github.com/mshykov/local-review/internal/git"
	"github.com/mshykov/local-review/internal/multi"
)

func multiCmd(sf *sharedFlags) *cobra.Command {
	var mergeWith string

	cmd := &cobra.Command{
		Use:   "multi",
		Short: "Run parallel reviews with multiple LLMs",
		Long: `Multi runs code reviews in parallel using multiple AI models.

This command:
1. Detects installed LLM CLIs (claude, gemini, codex, gh copilot)
2. Runs reviews in parallel using enabled LLMs
3. Saves each review to .local-review/reviews/<branch>/<commit>_<llm>.md
4. (Future) Merges findings into a consolidated report

Example:
  local-review multi staged
  local-review multi commit abc123
  local-review multi branch main`,
	}

	// Add --merge-with flag
	cmd.PersistentFlags().StringVar(&mergeWith, "merge-with", "", "LLM to use for merging (default: config value or auto)")

	cmd.AddCommand(multiStagedCmd(sf, &mergeWith))
	cmd.AddCommand(multiCommitCmd(sf, &mergeWith))
	cmd.AddCommand(multiBranchCmd(sf, &mergeWith))

	return cmd
}

func multiStagedCmd(sf *sharedFlags, mergeWith *string) *cobra.Command {
	return &cobra.Command{
		Use:   "staged",
		Short: "Multi-LLM review of staged changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMultiReview(cmd.Context(), sf, git.ModeStaged, "", *mergeWith)
		},
	}
}

func multiCommitCmd(sf *sharedFlags, mergeWith *string) *cobra.Command {
	return &cobra.Command{
		Use:   "commit [<rev>]",
		Short: "Multi-LLM review of a single commit (default: HEAD)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runMultiReview(cmd.Context(), sf, git.ModeCommit, ref, *mergeWith)
		},
	}
}

func multiBranchCmd(sf *sharedFlags, mergeWith *string) *cobra.Command {
	return &cobra.Command{
		Use:   "branch [<base>]",
		Short: "Multi-LLM review of the current branch (default: main)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base := ""
			if len(args) == 1 {
				base = args[0]
			}
			return runMultiReview(cmd.Context(), sf, git.ModeBranch, base, *mergeWith)
		},
	}
}

func runMultiReview(ctx context.Context, sf *sharedFlags, mode git.Mode, ref, mergeWith string) error {
	// 1. Load config
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Validate config for multi-LLM mode
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// 2. Detect installed LLMs
	detected := cli.DetectAll()
	enabled := filterEnabledLLMs(detected, cfg.LLMs)

	if len(enabled) == 0 {
		return fmt.Errorf("no LLMs available for multi-review (run 'local-review doctor' to check installation)")
	}

	fmt.Printf("Running review with %d LLMs...\n", len(enabled))
	for _, llm := range enabled {
		fmt.Printf("  • %s v%s\n", llm.Name, llm.Version)
	}
	fmt.Println()

	// 3. Extract diff
	diffs, err := git.Extract(mode, ref)
	if err != nil {
		return fmt.Errorf("extract diff: %w", err)
	}

	if len(diffs) == 0 {
		fmt.Println("No changes to review.")
		return nil
	}

	diffStr := formatDiffForLLM(diffs)

	// 4. Get commit and branch info once
	commit := git.CurrentCommit()
	branch := git.CurrentBranch()

	// For commit mode, use the provided ref if given
	if mode == git.ModeCommit && ref != "" {
		// Resolve the ref to a commit hash
		commit = ref
		// If it's a short hash or branch name, resolve it
		if len(commit) < 40 {
			resolvedCommit := git.ResolveRef(ref)
			if resolvedCommit != "" {
				commit = resolvedCommit
			} else {
				return fmt.Errorf("failed to resolve ref '%s' to commit hash", ref)
			}
		}
	}

	// Validate commit and branch before proceeding
	// git.CurrentCommit() returns "HEAD" on failure, which sanitizes to "EA"
	// git.CurrentBranch() returns "unknown" on failure
	if commit == "HEAD" {
		return fmt.Errorf("failed to get current commit (git rev-parse failed)")
	}
	if branch == "unknown" {
		return fmt.Errorf("failed to get current branch (detached HEAD or git error)")
	}
	// Ensure commit is a valid hex string (after potential resolution)
	sanitized := git.SanitizeCommit(commit)
	if sanitized == "" || len(sanitized) < 6 {
		return fmt.Errorf("invalid commit hash '%s' (sanitized to '%s')", commit, sanitized)
	}

	startTime := time.Now()

	// 5. Run parallel reviews
	storage := multi.NewStorage(cfg.Storage.BasePath)
	orch := multi.NewOrchestrator(enabled, storage)

	results, err := orch.RunParallel(ctx, diffStr, commit, branch)
	if err != nil {
		return fmt.Errorf("run reviews: %w", err)
	}

	// 6. Print status for each LLM

	for i, r := range results {
		if r.Error != nil {
			fmt.Printf("[%d/%d] %s ✗ (%v)\n", i+1, len(results), r.LLM, r.Error)
		} else {
			fmt.Printf("[%d/%d] %s ✓ (%.1fs)\n", i+1, len(results), r.LLM, r.Duration.Seconds())
		}
	}
	fmt.Println()

	// Count successful reviews
	successCount := multi.CountSuccessful(results)

	// 7. Build metadata (save after merge completes)
	metadata := buildMetadata(commit, branch, results, startTime)

	// 8. Merge reviews if we have successful results
	if successCount > 0 {
		fmt.Println()
		fmt.Println("Merging reviews...")

		// Select merge LLM
		preferred := cfg.Merge.PreferredLLM
		if mergeWith != "" {
			preferred = mergeWith
		}
		mergeLLM := selectMergeLLM(results, enabled, preferred)
		if mergeLLM == nil {
			fmt.Println("Warning: no LLM available for merging (skipping merge)")
		} else {
			fmt.Printf("Using %s for merge...\n", mergeLLM.Name)

			// Create merger
			merger, err := multi.NewMerger(*mergeLLM)
			if err != nil {
				fmt.Printf("Warning: failed to create merger: %v\n", err)
			} else {
				// Build merge input
				mergeInput := multi.BuildMergeInput(results, cfg.Merge.ConsensusThreshold)

				// Create context with timeout for merge operation
				mergeTimeout := time.Duration(mergeLLM.TimeoutSec) * time.Second
				if mergeLLM.TimeoutSec == 0 {
					mergeTimeout = 120 * time.Second
				}
				mergeCtx, cancel := context.WithTimeout(ctx, mergeTimeout)
				defer cancel()

				// Run merge
				mergeStart := time.Now()
				merged, err := merger.Merge(mergeCtx, mergeInput)
				mergeDuration := time.Since(mergeStart)

				if err != nil {
					fmt.Printf("Warning: merge failed: %v\n", err)
					metadata.Merge.Status = "failed"
					metadata.Merge.Error = err.Error()
				} else {
					// Save merged review
					mergedPath, err := storage.SaveMerged(branch, commit, merged)
					if err != nil {
						fmt.Printf("Warning: failed to save merged review: %v\n", err)
					} else {
						fmt.Printf("✓ Merged review saved to: %s (%.1fs)\n", mergedPath, mergeDuration.Seconds())

						// Update metadata
						metadata.Merge.LLM = mergeLLM.Name
						metadata.Merge.Status = "success"
						metadata.Merge.DurationMs = mergeDuration.Milliseconds()
					}
				}
			}
		}
	} else {
		// No successful reviews - mark merge as skipped
		metadata.Merge.Status = "skipped"
	}

	// 9. Save metadata (with merge info if merge succeeded)
	metaPath, err := storage.SaveMetadata(branch, commit, metadata)
	if err != nil {
		fmt.Printf("Warning: failed to save metadata: %v\n", err)
	}

	// 10. Print summary
	fmt.Println()
	fmt.Printf("✓ Review complete: %d/%d LLMs succeeded\n", successCount, len(results))
	fmt.Printf("Reviews saved to: %s/%s/\n", cfg.Storage.BasePath, git.SanitizeBranchName(branch))
	if metaPath != "" {
		fmt.Printf("Metadata: %s\n", metaPath)
	}

	return nil
}

// filterEnabledLLMs returns only LLMs that are both detected and enabled in config.
// Unrecognized LLMs (not in config) are included by default.
// Populates TimeoutSec from config (default: 120 seconds).
func filterEnabledLLMs(detected []cli.LLM, configs map[string]config.LLMConfig) []cli.LLM {
	var enabled []cli.LLM
	for _, llm := range detected {
		// Skip if not available
		if !llm.Available {
			continue
		}

		// Check if enabled in config (nil means enabled by default)
		if cfg, ok := configs[llm.Name]; ok {
			// LLM is in config - respect enabled setting
			if cfg.Enabled == nil || *cfg.Enabled {
				// Populate timeout from config (default: 120)
				timeout := cfg.TimeoutSec
				if timeout == 0 {
					timeout = 120
				}
				llm.TimeoutSec = timeout
				enabled = append(enabled, llm)
			}
		} else {
			// LLM not in config - include by default with default timeout
			llm.TimeoutSec = 120
			enabled = append(enabled, llm)
		}
	}
	return enabled
}

// formatDiffForLLM converts git diffs to a string suitable for LLM input.
func formatDiffForLLM(diffs []git.Diff) string {
	var b strings.Builder
	for _, d := range diffs {
		b.WriteString(fmt.Sprintf("## File: %s\n", d.Path))
		for _, h := range d.Hunks {
			b.WriteString(h.Header)
			b.WriteString("\n")
			b.WriteString(h.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// buildMetadata creates metadata from review results.
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

// selectMergeLLM selects which LLM to use for merging reviews.
// Priority: preferred config > auto (claude > codex > gemini > copilot) > first successful
func selectMergeLLM(results []multi.ReviewResult, availableLLMs []cli.LLM, preferred string) *cli.LLM {
	// Build map of successful LLMs
	successfulMap := make(map[string]cli.LLM)
	for _, llm := range availableLLMs {
		// Check if this LLM succeeded
		for _, r := range results {
			if r.LLM == llm.Name && r.Error == nil {
				successfulMap[llm.Name] = llm
				break
			}
		}
	}

	if len(successfulMap) == 0 {
		return nil
	}

	// If preferred is specified and available, use it
	if preferred != "" && preferred != "auto" {
		if llm, ok := successfulMap[preferred]; ok {
			return &llm
		}
	}

	// Auto mode: try in priority order
	priorityOrder := []string{"claude", "codex", "gemini", "copilot"}
	for _, name := range priorityOrder {
		if llm, ok := successfulMap[name]; ok {
			return &llm
		}
	}

	// Fallback: return first successful (in execution order)
	for _, r := range results {
		if r.Error == nil {
			if llm, ok := successfulMap[r.LLM]; ok {
				return &llm
			}
		}
	}

	return nil
}
