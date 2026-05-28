package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mshykov/local-review/internal/agents/provider"
)

// providerProbe wraps internal/agents/provider.Probe in a function-
// value the package-level providerProbeFunc seam can point at. Plain
// function alias so tests can swap it without an interface (the seam
// is reassignment, not dependency injection).
func providerProbe(ctx context.Context, baseURL, apiKey string, timeout time.Duration) error {
	return provider.Probe(ctx, baseURL, apiKey, timeout)
}

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

	// ProbeCanceled: the parent context was canceled — typically
	// the user pressing Ctrl+C, OR a parent timeout firing across
	// the whole run rather than the per-LLM probe budget. Distinct
	// from ProbeTimeout because the user-facing message is different
	// ("user interrupt" vs "vendor slow today") and the runner's
	// post-probe error handling needs to short-circuit on cancel
	// rather than complain about "every LLM failed pre-flight"
	// when the real cause was a deliberate stop signal.
	ProbeCanceled
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
	case ProbeCanceled:
		return "canceled"
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

// probeTimeoutErr is the error type Probe returns on a ctx-
// deadline expiry when partial-stderr surfaced a vendor message.
// Custom type so:
//
//   - Error() returns the display-friendly "timeout after Ns —
//     <vendor message>" — what the readiness block renders.
//   - Unwrap() returns the underlying context.DeadlineExceeded
//     so callers using `errors.Is(err, context.DeadlineExceeded)`
//     keep working as they did pre-v0.10.6, when ProbeTimeout's
//     Err was just ctx.Err() directly.
//
// Codex caught the missing Unwrap chain on PR #91's own dogfood —
// without this type, my fmt.Errorf("timeout after %s — %s", ...)
// produced a plain string error that broke the errors.Is path.
type probeTimeoutErr struct {
	display string
	cause   error // typically context.DeadlineExceeded
}

func (p *probeTimeoutErr) Error() string { return p.display }
func (p *probeTimeoutErr) Unwrap() error { return p.cause }

// PartialStderrCapturer is an OPTIONAL interface invokers can
// implement to expose whatever's been written to the subprocess's
// stderr so far — without waiting for the subprocess to actually
// exit. Used by Probe to surface the vendor's diagnostic line
// (e.g. "You have exhausted your capacity on this model.") in
// the readiness block when a probe times out, instead of the
// generic "timeout after 10s" we'd otherwise have to print
// because the subprocess is hung past SIGKILL on pipe drain.
//
// The current production invokers (Claude / Gemini / Codex) all
// implement this — they buffer up to 4 KiB of stderr via a
// stderrCapture written in parallel to the normal cmd.Stderr
// destination. The interface is optional so tests + future
// invokers can opt out by simply not implementing it; Probe
// falls back to the generic timeout text in that case.
type PartialStderrCapturer interface {
	PartialStderr() string
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

	// Run inv.RunPrompt on a background goroutine and race it
	// against ctx.Done(). v0.10.4 had a real bug here: when the
	// underlying CLI (typically gemini under capacity-exhausted
	// auth conditions) hangs past ctx.Deadline, exec.CommandContext
	// SIGKILLs the subprocess but cmd.Wait() — inside RunPrompt —
	// still blocks on stdout/stderr pipe drainage until the
	// subprocess actually exits. For a hung CLI that wedge can be
	// minutes. Pre-fix the probe phase took 4m34s on the same case
	// the feature was designed to eliminate (the original ~4 min
	// gemini hang was the whole reason v0.10.1 introduced the
	// probe).
	//
	// Fix: race RunPrompt against ctx.Done(). When ctx expires
	// first, return ProbeTimeout immediately. The background
	// goroutine keeps draining until the OS finally reaps the
	// subprocess — but the user-visible wall-clock is exactly
	// the per-LLM timeout the caller set.
	//
	// The leaked goroutine is bounded: exec.CommandContext SIGKILLs
	// the subprocess at ctx.Done, the OS reaps it, the pipe FDs
	// close, cmd.Wait returns. The goroutine then exits. Worst case
	// the goroutine outlives Probe by however long the subprocess
	// takes to actually die — typically milliseconds, occasionally
	// seconds, but never indefinitely. The send to outcomeCh has
	// a buffer of 1, so the goroutine doesn't block on send even
	// if no-one's reading.
	type probeOutcome struct {
		out string
		err error
	}
	outcomeCh := make(chan probeOutcome, 1)
	go func() {
		out, _, err := inv.RunPrompt(ctx, probePrompt)
		outcomeCh <- probeOutcome{out: out, err: err}
	}()

	select {
	case r := <-outcomeCh:
		return classifyProbeOutcome(name, start, r.out, r.err, ctx)
	case <-ctx.Done():
		// Context expired before RunPrompt returned. Branch on
		// ctx.Err() to distinguish a deadline (the v0.10.1
		// feature's intended target — vendor SLOW today) from a
		// cancel (user Ctrl+C or parent timeout firing across the
		// whole run). The first build of v0.10.5 returned
		// ProbeTimeout unconditionally here, which conflated the
		// two and would surface "every LLM failed pre-flight" on
		// what was actually a deliberate user interrupt (codex
		// caught this in PR #88's own dogfood — fixed before
		// merge).
		//
		// The background goroutine keeps draining (see above);
		// Probe returns immediately on either signal.
		pr := ProbeResult{
			LLM:      name,
			Err:      ctx.Err(),
			Duration: time.Since(start),
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			pr.Status = ProbeCanceled
		} else {
			// DeadlineExceeded or anything else context-shaped:
			// classify as Timeout. The DeadlineExceeded path is
			// the v0.10.1 target case; "anything else" is a
			// defensive default that keeps Probe from ever
			// returning ProbeReady on a canceled context.
			pr.Status = ProbeTimeout
			// v0.10.6: peek the invoker's partial stderr (if it
			// supports the capability). When a CLI prints its
			// error to stderr BEFORE hanging on a network call —
			// gemini's "exhausted capacity" pattern, codex's
			// auth-failure messages — the relevant text is
			// already in the partial buffer at this point.
			// Surface it as the readiness block's reason text
			// instead of the generic "timeout after 10s." Limit
			// to 240 chars so a misbehaving CLI dumping a
			// stack trace doesn't blow up the line; the runner's
			// singleLine() collapses any internal newlines.
			if capturer, ok := inv.(PartialStderrCapturer); ok {
				if partial := strings.TrimSpace(capturer.PartialStderr()); partial != "" {
					if len(partial) > 240 {
						partial = partial[:240] + "…"
					}
					// Use probeTimeoutErr (custom type) instead
					// of fmt.Errorf so the rendered text stays
					// clean ("timeout after Ns — <vendor>") while
					// errors.Is(err, context.DeadlineExceeded)
					// still works — the original ctx.Err() chain
					// is preserved via Unwrap.
					pr.Err = &probeTimeoutErr{
						display: fmt.Sprintf("timeout after %s — %s",
							time.Since(start).Round(10*time.Millisecond), partial),
						cause: ctx.Err(),
					}
				}
			}
		}
		return pr
	}
}

// classifyProbeOutcome owns the err/output → ProbeStatus mapping
// for the non-timeout path. Extracted from Probe so the inline
// flow in Probe reads as "race RunPrompt against ctx; on either
// outcome, classify and return" — the classification rules are
// load-bearing enough to deserve their own named function.
func classifyProbeOutcome(name string, start time.Time, out string, err error, ctx context.Context) ProbeResult {
	pr := ProbeResult{LLM: name, Duration: time.Since(start)}

	if err != nil {
		pr.Err = err
		// Cancellation MUST win over the timeout / generic-error
		// classification: when ctx is canceled (Ctrl+C, parent
		// context cancel), the invoker may return ctx.Err() ==
		// context.Canceled BEFORE Probe's outer `case <-ctx.Done()`
		// fires — so the result lands here, in classifyProbeOutcome,
		// rather than in Probe's dedicated ProbeCanceled branch.
		// Without this guard the canceled case would have fallen
		// through to ProbeError, which then surfaces as "every LLM
		// failed pre-flight" instead of propagating cleanly. The
		// race window is tight but real (claude+codex caught it on
		// PR #88's second dogfood — the fix for ctx-done in this
		// same PR opened it).
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			pr.Status = ProbeCanceled
			return pr
		}
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
// strict toggles how provider agents are probed:
//
//   - false (default): provider agents use a cheap HTTP `GET /v1/models`
//     check — proves the endpoint is up and auth is accepted without
//     loading the model or burning tokens. Right default for the common
//     "is my Ollama tailnet box reachable?" question. CLI agents always
//     use the existing `Reply OK` subprocess probe regardless (they
//     have no lighter alternative).
//
//   - true: every agent (provider AND CLI) does the full `Reply OK`
//     via the Invoker contract — for providers that's an actual
//     `POST /v1/chat/completions` exercising the named model, catching
//     "endpoint up but model not loaded" cases the light probe misses.
//     Useful when the configured model id matters; surfaced via the
//     `--strict-probe` flag.
func ProbeAll(ctx context.Context, llms []LLM, perLLMTimeout time.Duration, strict bool) []ProbeResult {
	return ProbeAllWithInvokerFactory(ctx, llms, perLLMTimeout, NewInvoker, providerProbe, strict)
}

// ProviderProbeFunc is the seam shape ProbeAllWithInvokerFactory uses
// to drive the cheap HTTP /v1/models check. Production code passes
// providerProbe (which adapts internal/agents/provider.Probe); tests
// pass deterministic fakes. Per-call (not a package global) so tests
// can run in parallel without seam reassignment races.
type ProviderProbeFunc func(ctx context.Context, baseURL, apiKey string, timeout time.Duration) error

// ProbeAllWithInvokerFactory is the test-seam variant of ProbeAll.
// Production code calls ProbeAll; this is exported only so the
// runner's tests can substitute fake invokers (the same pattern
// internal/multi.Orchestrator uses for its unexported
// invokerFactory field). Kept as a public function rather than
// a struct field so callers don't have to construct a Prober type
// for a single override — the seam is the function pointer
// itself.
//
// See ProbeAll for the `strict` semantics.
func ProbeAllWithInvokerFactory(ctx context.Context, llms []LLM, perLLMTimeout time.Duration, factory func(LLM) Invoker, providerProbeFn ProviderProbeFunc, strict bool) []ProbeResult {
	if providerProbeFn == nil {
		providerProbeFn = providerProbe
	}
	results := make([]ProbeResult, len(llms))
	var wg sync.WaitGroup
	for i, l := range llms {
		wg.Add(1)
		go func(idx int, llm LLM) {
			defer wg.Done()
			// Provider agents in light mode: HTTP /v1/models is enough.
			// In strict mode, fall through to the Invoker path below,
			// which exercises the configured model via a real chat call.
			if llm.BaseURL != "" && !strict {
				results[idx] = probeProviderLight(ctx, llm, perLLMTimeout, providerProbeFn)
				return
			}
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

// probeProviderLight runs the cheap `GET <base_url>/models` readiness
// check (in internal/agents/provider) and maps the outcome into the
// same ProbeResult shape ProbeAll returns for CLI agents — so the
// runner's readiness block, ✓/✗ glyphs, and downstream filtering work
// identically across kinds.
//
// The duration we report is wall-clock from entry to either probe
// completion or ctx expiry, so the "qwen ✓ (0.4s)" line shows the
// actual time the user waited — not a vendor-reported latency that
// might exclude TCP setup.
func probeProviderLight(ctx context.Context, llm LLM, timeout time.Duration, providerProbeFn ProviderProbeFunc) ProbeResult {
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := providerProbeFn(pctx, llm.BaseURL, llm.APIKey, timeout)
	duration := time.Since(start)
	if err == nil {
		return ProbeResult{LLM: llm.Name, Status: ProbeReady, Duration: duration}
	}
	// Distinguish user-Ctrl+C / parent-cancel from a real failure or
	// vendor timeout — same disambiguation Probe (the CLI path) does so
	// the runner's "every LLM failed" handler can short-circuit on
	// cancel. Use errors.Is rather than a pctx.Err() identity check
	// because provider.Probe wraps the cause (`fmt.Errorf("reach %s:
	// %w", url, ctx.Err())`) AND has its own inner ctx that may fire
	// before the parent — a plain pctx.Err() check missed the wrapped
	// inner-deadline case and emitted ProbeError + the generic ✗ glyph
	// instead of ProbeTimeout (flagged by the PR 3 self-review).
	if errors.Is(err, context.Canceled) {
		return ProbeResult{LLM: llm.Name, Status: ProbeCanceled, Duration: duration, Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ProbeResult{LLM: llm.Name, Status: ProbeTimeout, Duration: duration, Err: err}
	}
	return ProbeResult{LLM: llm.Name, Status: ProbeError, Duration: duration, Err: err}
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
