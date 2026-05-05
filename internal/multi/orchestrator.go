package multi

import (
	"context"
	"fmt"
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
type ReviewResult struct {
	LLM      string
	Version  string
	Mode     string
	Output   string
	Error    error
	Duration time.Duration
	FilePath string
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

			// Create context with timeout from config (default: 120s)
			timeout := time.Duration(l.TimeoutSec) * time.Second
			if l.TimeoutSec == 0 {
				timeout = 120 * time.Second
			}
			reviewCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			// Run review
			output, err := invoker.Review(reviewCtx, systemPrompt, diff)
			result.Duration = time.Since(start)

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

// CountSuccessful returns the number of successful reviews.
func CountSuccessful(results []ReviewResult) int {
	count := 0
	for _, r := range results {
		if r.Error == nil {
			count++
		}
	}
	return count
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
