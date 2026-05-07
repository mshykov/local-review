package multi

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

// Orchestrator coordinates parallel reviews from multiple LLMs.
type Orchestrator struct {
	llms    []cli.LLM
	storage *ReviewStorage
}

// NewOrchestrator creates a new Orchestrator with the given LLMs and storage.
func NewOrchestrator(llms []cli.LLM, storage *ReviewStorage) *Orchestrator {
	return &Orchestrator{
		llms:    llms,
		storage: storage,
	}
}

// ReviewResult holds the result of a single LLM review.
//
// Tokens is populated from the CLI's structured output (claude /
// gemini JSON, codex stdout metadata) when available. Zero values
// mean "we couldn't determine usage" — the CLI version may be too
// old to support a JSON flag, or the output shape didn't match
// what we expected. Display callers should check Tokens.IsZero()
// rather than printing "0 in / 0 out" which would mislead users.
type ReviewResult struct {
	LLM      string
	Version  string
	Mode     string
	Output   string
	Error    error
	Duration time.Duration
	FilePath string
	Tokens   cli.TokenUsage
}

// RunParallel executes reviews concurrently for all configured LLMs.
// Returns results for all LLMs (including failures).
//
// systemPrompt is the language-specific prompt pack content the
// caller has already loaded (lang.Dominant + prompts.Get). Passed to
// every invoker so all agents review the diff against identical
// review-rules and severity tiering. Empty string is allowed — each
// invoker has a generic fallback.
func (o *Orchestrator) RunParallel(ctx context.Context, systemPrompt, diff, commit, branch string) ([]ReviewResult, error) {
	var wg sync.WaitGroup
	results := make([]ReviewResult, len(o.llms))

	for i, llm := range o.llms {
		wg.Add(1)
		go func(idx int, l cli.LLM) {
			defer wg.Done()

			start := time.Now()
			result := ReviewResult{
				LLM:     l.Name,
				Version: l.Version,
				Mode:    "cli", // TODO: pass mode from invoker once API fallback is implemented
			}

			// Create invoker
			invoker := cli.NewInvoker(l)
			if invoker == nil {
				result.Error = fmt.Errorf("failed to create invoker for %s", l.Name)
				result.Duration = time.Since(start)
				results[idx] = result
				return
			}

			// Create context with timeout from config; falls back to
			// cli.DefaultTimeoutSec — same constant the runner's
			// applyConfig fallback and the roster's display fallback
			// use, so what the user sees ("timeout: Ns") matches
			// what actually fires. `<= 0` (rather than `== 0`)
			// protects against a negative `timeout_seconds: -1`
			// typo in user config.
			timeout := time.Duration(l.TimeoutSec) * time.Second
			if l.TimeoutSec <= 0 {
				timeout = time.Duration(cli.DefaultTimeoutSec) * time.Second
			}
			reviewCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// Run review
			output, tokens, err := invoker.Review(reviewCtx, systemPrompt, diff)
			result.Duration = time.Since(start)
			result.Tokens = tokens

			if err != nil {
				result.Error = err
				results[idx] = result
				return
			}

			result.Output = output

			// Save to disk
			path, err := o.storage.SaveReview(branch, commit, l.Name, l.Version, output)
			if err != nil {
				result.Error = fmt.Errorf("save review: %w", err)
				results[idx] = result
				return
			}

			result.FilePath = path
			results[idx] = result
		}(i, llm)
	}

	wg.Wait()
	return results, nil
}

// CountSuccessful returns the number of successful reviews (Error == nil); for framing decisions prefer CountWithOutput.
func CountSuccessful(results []ReviewResult) int {
	count := 0
	for _, r := range results {
		if r.Error == nil {
			count++
		}
	}
	return count
}

// CountWithOutput returns the number of reviews with non-blank Output (matches BuildMergeInput's filter).
func CountWithOutput(results []ReviewResult) int {
	count := 0
	for _, r := range results {
		if HasMergeableOutput(r) {
			count++
		}
	}
	return count
}

// HasMergeableOutput reports whether r has non-whitespace Output (single source of truth across CountWithOutput, BuildMergeInput, selectMergeLLM).
func HasMergeableOutput(r ReviewResult) bool {
	return strings.TrimSpace(r.Output) != ""
}

// GetSuccessful returns only successful review results.
func GetSuccessful(results []ReviewResult) []ReviewResult {
	var successful []ReviewResult
	for _, r := range results {
		if r.Error == nil {
			successful = append(successful, r)
		}
	}
	return successful
}
