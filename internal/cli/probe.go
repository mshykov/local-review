package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProbeStatus is the outcome of a pre-flight readiness probe.
//
// Distinct from a per-LLM review error because the user-facing
// rendering differs: a probe failure prints a one-character marker
// (✗) in the readiness block and gets a single-line error summary,
// not the multi-line stderr-tail treatment a real-review failure
// gets. Conflating the two would surface noise in the readiness
// block (which is meant to read at a glance) and hide auth-vs-
// capacity-vs-network distinctions a user needs to act on.
type ProbeStatus int

const (
	// ProbeReady: the CLI returned a non-empty response within the
	// per-LLM timeout window. The LLM is fit to participate in the
	// real review run.
	ProbeReady ProbeStatus = iota

	// ProbeError: the invoker returned an error other than a context
	// deadline — typically auth-not-ready, capacity exhausted on the
	// model, or network/connectivity. The error message carries the
	// vendor's own string (ClassifyExit'd) so the user sees what to
	// fix without re-running the real review.
	ProbeError

	// ProbeTimeout: the probe context's deadline expired before the
	// CLI replied. Distinct from ProbeError because it usually means
	// "vendor SLOW today" rather than "permanently broken" — the
	// real-review timeout is much longer and might still complete.
	// Treated the same as ProbeError for run-or-skip purposes; the
	// distinct status exists so the readiness block can render a
	// clearer "(timeout after Ns)" hint instead of a generic
	// error string.
	ProbeTimeout
)

// String returns a one-word identifier suitable for rendering /
// logging. The readiness block uses ✓/✗ glyphs at the call site;
// this is for log lines and tests.
func (s ProbeStatus) String() string {
	switch s {
	case ProbeReady:
		return "ready"
	case ProbeError:
		return "error"
	case ProbeTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// ProbeResult is the per-LLM outcome of a pre-flight readiness
// probe. Symmetric with multi.ReviewResult in that we always emit
// one of these per LLM (even on failure) so callers can render a
// complete readiness block without back-tracking.
type ProbeResult struct {
	LLM      string
	Status   ProbeStatus
	Err      error         // populated iff Status != ProbeReady
	Duration time.Duration // wall-clock of the probe call
}

// IsReady is a named predicate so call sites don't litter the
// runner with `r.Status == cli.ProbeReady` comparisons.
func (r ProbeResult) IsReady() bool {
	return r.Status == ProbeReady
}

// probePrompt is the smallest payload that exercises an LLM's
// auth + capacity without burning measurable tokens. "Reply with
// exactly: OK" is short, unambiguous, and produces a deterministic
// response that lets the probe distinguish "CLI replied" from
// "CLI exited 0 with empty stdout" (a real LLM-emitter pathology
// the GateDecision refactor catalogued for the real-review path —
// the probe defends against the same thing pre-flight).
//
// Not a constant the caller can override: a configurable probe
// prompt is a footgun (users supplying a 50KB prompt would defeat
// the point) and the current literal is small enough that no real
// LLM should ever refuse it. If we later need probe-prompt
// tuning per vendor, do it inside Probe(), not via a parameter.
const probePrompt = "Reply with exactly: OK"

// DefaultProbeTimeout is the per-LLM deadline for the pre-flight
// probe. 10s is long enough that a healthy CLI's startup + minimal
// round-trip fits comfortably (claude-code is the slowest in our
// roster at ~3-5s on a warm cache) and short enough that a hung
// CLI doesn't make the readiness block itself feel like the
// 4-minute gemini hang we're trying to eliminate.
//
// Public constant so doctor and future callers can use the same
// value as the runner — if we tune it later, every consumer
// updates at once instead of drifting.
const DefaultProbeTimeout = 10 * time.Second

// Probe invokes inv with a tiny "reply OK" prompt and returns the
// outcome. The caller owns the context's deadline: this function
// does no inner timeout management so there's a single source of
// truth for "how long do we wait before declaring an LLM
// unresponsive" (the per-LLM ctx deadline set by ProbeAll, or by
// a test passing context.WithTimeout directly).
//
// name is plumbed through to ProbeResult.LLM so the caller can
// build the readiness block without re-deriving identity from the
// invoker (Invoker is an interface and doesn't expose name).
func Probe(ctx context.Context, inv Invoker, name string) ProbeResult {
	start := time.Now()
	out, _, err := inv.RunPrompt(ctx, probePrompt)
	pr := ProbeResult{LLM: name, Duration: time.Since(start)}

	if err != nil {
		pr.Err = err
		// errors.Is(ctx.Err()) is the structurally-correct check,
		// but ClassifyExit wraps the underlying context error in
		// its own formatted message string by the time we see it
		// here, so errors.Is on the returned err can miss. We also
		// match on the rendered text as a belt-and-braces guard —
		// either signal classifies the probe as a timeout so the
		// readiness block renders the right glyph + hint.
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(ctx.Err(), context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "deadline exceeded") {
			pr.Status = ProbeTimeout
		} else {
			pr.Status = ProbeError
		}
		return pr
	}

	// CLI exited 0 but said nothing — an LLM-emitter pathology
	// distinct from "errored." Catch it as ProbeError so the
	// readiness block surfaces a ✗ instead of letting the real
	// review proceed against an LLM that won't actually produce
	// findings either.
	if strings.TrimSpace(out) == "" {
		pr.Status = ProbeError
		pr.Err = errors.New("CLI exited 0 with empty response")
		return pr
	}

	pr.Status = ProbeReady
	return pr
}

// ProbeAll runs Probe on each LLM in parallel and returns the
// results in roster order. Each LLM gets its own context with
// perLLMTimeout — slow LLMs don't hold up faster ones, and a
// hung CLI on one agent doesn't block the entire readiness block
// (which was the v0.10.0-RC observed failure: gemini's
// exhausted-capacity error surfacing after a ~4 min hang while
// claude and codex sat idle waiting for it).
//
// Returns one ProbeResult per input LLM, indexed by roster
// position (not completion order, unlike orchestrator.RunParallel).
// Roster order matches the readiness block's left-to-right layout
// in the runner; reordering by completion would make the block
// non-deterministic across runs, hurting readability when users
// compare two runs side by side.
//
// invokerFactory is an unexported test seam (see ProbeAllWithInvokerFactory).
// Production callers use ProbeAll which wires in cli.NewInvoker.
func ProbeAll(ctx context.Context, llms []LLM, perLLMTimeout time.Duration) []ProbeResult {
	return ProbeAllWithInvokerFactory(ctx, llms, perLLMTimeout, NewInvoker)
}

// ProbeAllWithInvokerFactory is the test-seam variant of ProbeAll.
// Production code calls ProbeAll; this is exported only so the
// runner's tests can substitute fake invokers (the same pattern
// internal/multi.Orchestrator uses for its unexported
// invokerFactory field). Kept as a public function rather than
// a struct field so callers don't have to construct a Prober type
// for a single override — the seam is the function pointer
// itself.
func ProbeAllWithInvokerFactory(ctx context.Context, llms []LLM, perLLMTimeout time.Duration, factory func(LLM) Invoker) []ProbeResult {
	results := make([]ProbeResult, len(llms))
	var wg sync.WaitGroup
	for i, l := range llms {
		wg.Add(1)
		go func(idx int, llm LLM) {
			defer wg.Done()
			inv := factory(llm)
			if inv == nil {
				results[idx] = ProbeResult{
					LLM:    llm.Name,
					Status: ProbeError,
					Err:    fmt.Errorf("no invoker for %s", llm.Name),
				}
				return
			}
			pctx, cancel := context.WithTimeout(ctx, perLLMTimeout)
			defer cancel()
			results[idx] = Probe(pctx, inv, llm.Name)
		}(i, l)
	}
	wg.Wait()
	return results
}

// SplitReady partitions a ProbeResult slice into "ready" and
// "not ready" subsets, preserving order within each subset. Used
// by the runner to filter the active LLM list to the ones that
// passed pre-flight, while keeping the not-ready ones around for
// rendering the readiness block.
//
// Operates on names so the caller can correlate with their own
// LLM slice without a second lookup. Returned slices are nil
// when empty (Go idiom) — callers should `len(...)`-check rather
// than `!= nil`-check.
func SplitReady(results []ProbeResult) (ready, notReady []string) {
	for _, r := range results {
		if r.IsReady() {
			ready = append(ready, r.LLM)
		} else {
			notReady = append(notReady, r.LLM)
		}
	}
	return ready, notReady
}
