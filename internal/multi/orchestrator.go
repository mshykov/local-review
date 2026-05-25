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
//
// invokerFactory is an in-package test seam. Production code uses
// cli.NewInvoker; orchestrator_test.go swaps in fake invokers with
// controlled durations so the streaming contract (channel emits in
// completion order, closes after all done) can be pinned without
// shelling out to real CLIs. Kept unexported to avoid leaking the
// hook into the package's public API.
type Orchestrator struct {
	llms           []cli.LLM
	storage        *ReviewStorage
	invokerFactory func(cli.LLM) cli.Invoker
}

// NewOrchestrator creates a new Orchestrator with the given LLMs and storage.
func NewOrchestrator(llms []cli.LLM, storage *ReviewStorage) *Orchestrator {
	return &Orchestrator{
		llms:           llms,
		storage:        storage,
		invokerFactory: cli.NewInvoker,
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

// RunParallel executes reviews concurrently for all configured LLMs
// and returns a channel that emits each ReviewResult as its agent
// finishes. The channel is closed after every agent has reported,
// so callers can range over it. Buffered to len(llms) so a slow
// consumer can't deadlock the workers.
//
// Streaming (added in v0.6.7) replaced the prior "block on wg.Wait,
// return slice" shape because users with one slow agent (gemini-3.x
// preview at 5+ min) saw a blank terminal for the whole run with
// zero feedback. Now each per-LLM line prints as the agent
// completes; the runner accumulates into a slice for the merge
// step. Emission order = completion order, not roster order — the
// CLI dropped the [N/M] prefix in favor of bare agent names so the
// new ordering doesn't read as misleading numbering.
//
// systemPrompt is the language-specific prompt pack content the
// caller has already loaded (lang.Dominant + prompts.Get). Passed to
// every invoker so all agents review the diff against identical
// review-rules and severity tiering. Empty string is allowed — each
// invoker has a generic fallback.
//
// The error return is reserved for synchronous setup failures (none
// today; always nil). Per-agent failures travel inside ReviewResult
// .Error so the channel still reports them and the runner can
// surface a per-LLM failure line in completion order.
func (o *Orchestrator) RunParallel(ctx context.Context, systemPrompt, diff, commit, branch string) (<-chan ReviewResult, error) {
	ch := make(chan ReviewResult, len(o.llms))
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		for _, llm := range o.llms {
			wg.Add(1)
			go func(l cli.LLM) {
				defer wg.Done()
				ch <- o.runOne(ctx, l, systemPrompt, diff, commit, branch)
			}(llm)
		}
		wg.Wait()
	}()
	return ch, nil
}

// runOne executes a single LLM's review and returns the result.
// Extracted from RunParallel's per-agent goroutine so the streaming
// wrapper stays a one-liner — the original inline body had grown to
// the point that the channel-send pattern was hard to see.
func (o *Orchestrator) runOne(ctx context.Context, l cli.LLM, systemPrompt, diff, commit, branch string) ReviewResult {
	start := time.Now()
	result := ReviewResult{
		LLM:     l.Name,
		Version: l.Version,
		Mode:    "cli", // TODO: pass mode from invoker once API fallback is implemented
	}

	invoker := o.invokerFactory(l)
	if invoker == nil {
		result.Error = fmt.Errorf("failed to create invoker for %s", l.Name)
		result.Duration = time.Since(start)
		return result
	}

	// Per-agent timeout from config; falls back to cli.DefaultTimeoutSec
	// — same constant the runner's applyConfig fallback and the roster's
	// display fallback use, so what the user sees ("timeout: Ns") matches
	// what actually fires. `<= 0` (rather than `== 0`) protects against
	// a negative `timeout_seconds: -1` typo in user config.
	timeout := time.Duration(l.TimeoutSec) * time.Second
	if l.TimeoutSec <= 0 {
		timeout = time.Duration(cli.DefaultTimeoutSec) * time.Second
	}
	reviewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, tokens, err := invoker.Review(reviewCtx, systemPrompt, diff)
	result.Duration = time.Since(start)
	result.Tokens = tokens

	if err != nil {
		result.Error = err
		return result
	}

	result.Output = output

	path, err := o.storage.SaveReview(branch, commit, l.Name, l.Version, output)
	if err != nil {
		result.Error = fmt.Errorf("save review: %w", err)
		return result
	}

	result.FilePath = path
	return result
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

// ZeroMergeableReason classifies *why* a result set has no mergeable
// output. Only meaningful when GateDecision.WithOutput == 0.
type ZeroMergeableReason int

const (
	// ZeroMergeableAllFailed: every review's Error is non-nil. The
	// LLMs crashed or the CLI invocation errored.
	ZeroMergeableAllFailed ZeroMergeableReason = iota

	// ZeroMergeableAllEmpty: every review's Error is nil, but no
	// review produced non-blank Output. The CLI exited zero with
	// empty stdout — historically a real bug (CodeRabbit caught a
	// case where this slipped past the gate with "all succeeded"
	// framing). Distinct from AllFailed because "succeeded with
	// empty output" is an LLM-emitter pathology, not a crash.
	ZeroMergeableAllEmpty

	// ZeroMergeableMixed: a mix of failures (Error != nil) and
	// successes-with-empty-output. Pre-consolidation the runner
	// would print "all returned empty output" here, misleading
	// users into debugging the wrong problem.
	ZeroMergeableMixed
)

// GateDecision is a single-pass summary of a result set, capturing
// both the "Error == nil" view (Successful) and the "has mergeable
// output" view (WithOutput). These two views CAN diverge and
// historically did, producing two distinct bugs:
//
//  1. SaveReview-failed-with-output: Error != nil but Output != "".
//     Counted in WithOutput, not Successful — the merger can still
//     consolidate the in-memory output even though persistence failed.
//
//  2. CLI-exited-zero-with-empty-output: Error == nil but Output == "".
//     Counted in Successful, not WithOutput — the merger has nothing
//     to consume even though the per-LLM call "succeeded."
//
// Pre-fix the runner threaded both counts through six call sites; this
// type is the single source so the metrics can't drift again
// (audit/tech-debt.md "## cmd/local-review [part 2/3]" major finding
// on runner.go:156, surfaced by `local-review audit --topic tech-debt`).
type GateDecision struct {
	Total      int // len(results)
	Successful int // Error == nil (regardless of Output)
	WithOutput int // HasMergeableOutput == true (regardless of Error)
}

// DecideGate computes a GateDecision from a result set in a single pass.
// The intent: derive both counts at the call site that needs them once,
// then thread the GateDecision through everything downstream — no second
// traversal, no possibility of the two metrics observing different
// snapshots of `results` (results is a slice, so the underlying array
// won't change, but the historical concern was call-site drift, not
// data-race drift).
func DecideGate(results []ReviewResult) GateDecision {
	g := GateDecision{Total: len(results)}
	for _, r := range results {
		if r.Error == nil {
			g.Successful++
		}
		if HasMergeableOutput(r) {
			g.WithOutput++
		}
	}
	return g
}

// HasMergeable reports whether at least one review has mergeable output.
// Use this instead of `WithOutput > 0` at gate sites — the named
// predicate documents intent ("is there anything for the merger to
// consume?") instead of leaving the reader to infer it from the count.
func (g GateDecision) HasMergeable() bool {
	return g.WithOutput > 0
}

// Failed returns the count of reviews where Error != nil (regardless
// of Output). Symmetric with Successful = Total - Failed.
func (g GateDecision) Failed() int {
	return g.Total - g.Successful
}

// ClassifyZero categorises why a result set produced nothing
// mergeable. Only meaningful when HasMergeable() is false; the runner
// uses it to pick the right error message ("all N failed" vs "all N
// returned empty output" vs the mixed-mode case).
//
// When HasMergeable() is true the result is meaningless — guard at
// the call site with `if !g.HasMergeable() { ... ClassifyZero() ... }`.
//
// Implementation note: when WithOutput == 0, every Successful entry
// is by definition "succeeded but produced no output" (no entry can
// be in WithOutput, so the intersection of Successful and WithOutput
// is empty). That's why the AllEmpty case compares Successful to
// Total — it's exact in the only state ClassifyZero is consulted in.
func (g GateDecision) ClassifyZero() ZeroMergeableReason {
	switch {
	case g.Failed() == g.Total:
		return ZeroMergeableAllFailed
	case g.Successful == g.Total:
		return ZeroMergeableAllEmpty
	default:
		return ZeroMergeableMixed
	}
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
