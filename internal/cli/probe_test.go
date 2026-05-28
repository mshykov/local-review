package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeProbeInvoker is a stand-in for a real LLM CLI invoker. The
// orchestrator package has its own fake (orchestrator_test.go);
// duplicating here rather than exporting that one keeps each
// package's test seams local to the package they test (Go
// convention) and avoids a public-API "fakeInvoker" we'd be
// stuck with forever.
type fakeProbeInvoker struct {
	output string
	err    error
	delay  time.Duration
}

func (f *fakeProbeInvoker) Review(ctx context.Context, _, _ string) (string, TokenUsage, error) {
	return "", TokenUsage{}, errors.New("Review not used in probe tests")
}

func (f *fakeProbeInvoker) RunPrompt(ctx context.Context, _ string) (string, TokenUsage, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			// Caller deadline expired before our simulated delay
			// finished. Surface ctx.Err() so the production code
			// path matches what a real CLI does when killed by
			// context cancellation.
			return "", TokenUsage{}, ctx.Err()
		}
	}
	return f.output, TokenUsage{}, f.err
}

// --- Probe (single-LLM) ----------------------------------------------------

func TestProbe_ReadyOnNonEmptyResponse(t *testing.T) {
	inv := &fakeProbeInvoker{output: "OK"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Probe(ctx, inv, "claude")
	if r.LLM != "claude" {
		t.Errorf("LLM = %q, want claude", r.LLM)
	}
	if !r.IsReady() {
		t.Errorf("IsReady() = false, want true (got status %s, err %v)", r.Status, r.Err)
	}
}

// "CLI exited 0 with empty stdout" is a real-world LLM-emitter
// pathology (the GateDecision refactor in PR #77 catalogued it
// for the real-review path). The probe must surface it as a
// failure — letting it pass would mean the real review would
// proceed against an LLM that doesn't actually produce findings,
// defeating the point of the probe.
func TestProbe_ErrorOnEmptyResponse(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"empty string", ""},
		{"whitespace only", "   \n\t   "},
		{"newline only", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := &fakeProbeInvoker{output: tc.out}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			r := Probe(ctx, inv, "claude")
			if r.Status != ProbeError {
				t.Errorf("Status = %s, want ProbeError (CLI exited 0 with empty output should not be Ready)", r.Status)
			}
			if r.Err == nil {
				t.Errorf("Err is nil, want a descriptive empty-response error")
			}
		})
	}
}

// Auth, capacity-exhausted, network — all the same shape from the
// probe's POV: invoker returns an error that isn't a context
// deadline. Status must be ProbeError so the readiness block
// renders a ✗ with the vendor's own message.
func TestProbe_ErrorOnInvokerError(t *testing.T) {
	inv := &fakeProbeInvoker{err: errors.New("You have exhausted your capacity on this model.")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Probe(ctx, inv, "gemini")
	if r.Status != ProbeError {
		t.Errorf("Status = %s, want ProbeError", r.Status)
	}
	if r.Err == nil || !strings.Contains(r.Err.Error(), "exhausted your capacity") {
		t.Errorf("Err = %v, want to preserve the vendor capacity-exhausted message", r.Err)
	}
}

// Timeout vs error distinction matters for the readiness block's
// rendering — "(timeout after Ns)" is a different hint than
// "(auth failed)". Probe must classify timeouts correctly.
func TestProbe_TimeoutOnDeadlineExceeded(t *testing.T) {
	// 100ms delay vs 30ms timeout — the invoker won't return
	// before the context expires, so the fake's `<-ctx.Done()`
	// branch fires and returns ctx.Err() (context.DeadlineExceeded).
	inv := &fakeProbeInvoker{delay: 100 * time.Millisecond, output: "OK"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	r := Probe(ctx, inv, "claude")
	if r.Status != ProbeTimeout {
		t.Errorf("Status = %s, want ProbeTimeout (deadline expired before invoker returned)", r.Status)
	}
}

// Real-world: the probe error is wrapped through ClassifyExit by
// the time it reaches us, so errors.Is(err, DeadlineExceeded) can
// miss. Probe must also recognise the substring as a timeout
// (belt-and-braces — this case verifies the text-match branch).
func TestProbe_TimeoutFromWrappedErrorString(t *testing.T) {
	// Invoker returns a wrapped error whose Error() contains
	// "deadline exceeded" but does NOT unwrap to context.DeadlineExceeded
	// (no errors.Is chain) — simulating the real ClassifyExit-
	// wrapped output from the production claude/codex/gemini
	// invokers.
	wrappedErr := errors.New("claude: deadline exceeded after 30s — try increasing timeout_seconds")
	inv := &fakeProbeInvoker{err: wrappedErr}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := Probe(ctx, inv, "claude")
	if r.Status != ProbeTimeout {
		t.Errorf("Status = %s, want ProbeTimeout (text-match fallback should classify this)", r.Status)
	}
}

// --- ProbeAll (fan-out) -----------------------------------------------------

func TestProbeAll_PreservesRosterOrder(t *testing.T) {
	llms := []LLM{
		{Name: "claude", Path: "fake"},
		{Name: "gemini", Path: "fake"},
		{Name: "codex", Path: "fake"},
	}
	// All ready. Use different delays so completion order
	// differs from roster order — codex would finish first if
	// we sorted by completion, but the result slice must mirror
	// the input roster.
	factory := func(l LLM) Invoker {
		switch l.Name {
		case "claude":
			return &fakeProbeInvoker{output: "OK", delay: 30 * time.Millisecond}
		case "gemini":
			return &fakeProbeInvoker{output: "OK", delay: 20 * time.Millisecond}
		case "codex":
			return &fakeProbeInvoker{output: "OK", delay: 10 * time.Millisecond}
		}
		return nil
	}

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory, nil, false)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	want := []string{"claude", "gemini", "codex"}
	for i, name := range want {
		if results[i].LLM != name {
			t.Errorf("results[%d].LLM = %q, want %q (roster order broken)", i, results[i].LLM, name)
		}
		if !results[i].IsReady() {
			t.Errorf("results[%d] (%s) not ready: %v", i, name, results[i].Err)
		}
	}
}

// Mixed shape: one ready, one error, one timeout. Captures the
// real-world readiness-block scenario the probe was designed
// for (the v0.10.0 dogfood: claude + codex ready, gemini
// exhausted-capacity).
func TestProbeAll_MixedReadyErrorTimeout(t *testing.T) {
	llms := []LLM{
		{Name: "claude", Path: "fake"},
		{Name: "gemini", Path: "fake"},
		{Name: "codex", Path: "fake"},
	}
	factory := func(l LLM) Invoker {
		switch l.Name {
		case "claude":
			return &fakeProbeInvoker{output: "OK"}
		case "gemini":
			return &fakeProbeInvoker{err: errors.New("You have exhausted your capacity on this model.")}
		case "codex":
			// 100ms delay > 30ms per-LLM timeout → ProbeTimeout
			return &fakeProbeInvoker{output: "OK", delay: 100 * time.Millisecond}
		}
		return nil
	}

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 30*time.Millisecond, factory, nil, false)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if !results[0].IsReady() {
		t.Errorf("claude should be ready, got %s: %v", results[0].Status, results[0].Err)
	}
	if results[1].Status != ProbeError {
		t.Errorf("gemini should be ProbeError, got %s", results[1].Status)
	}
	if results[2].Status != ProbeTimeout {
		t.Errorf("codex should be ProbeTimeout, got %s", results[2].Status)
	}
}

// nil from the invoker factory (unknown LLM name, simulating
// cli.NewInvoker on an unrecognised name) must produce a
// ProbeError emission, not a panic or silent skip — the runner
// relies on one ProbeResult per input LLM to render the readiness
// block without holes.
func TestProbeAll_NilInvokerFactoryProducesError(t *testing.T) {
	llms := []LLM{
		{Name: "claude", Path: "fake"},
		{Name: "unknown", Path: "fake"},
	}
	factory := func(l LLM) Invoker {
		if l.Name == "unknown" {
			return nil
		}
		return &fakeProbeInvoker{output: "OK"}
	}

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory, nil, false)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].IsReady() {
		t.Errorf("claude should be ready")
	}
	if results[1].Status != ProbeError {
		t.Errorf("unknown should be ProbeError")
	}
	if results[1].Err == nil {
		t.Errorf("unknown.Err is nil, want a descriptive error")
	}
}

// Parallelism check: if probes ran serially, three 50ms probes
// would take 150ms; parallel they take ~50ms. We allow 100ms
// slack to absorb CI scheduler jitter while still failing if
// the goroutines serialise.
func TestProbeAll_FansOutInParallel(t *testing.T) {
	const delay = 50 * time.Millisecond
	llms := []LLM{
		{Name: "claude", Path: "fake"},
		{Name: "gemini", Path: "fake"},
		{Name: "codex", Path: "fake"},
	}
	factory := func(l LLM) Invoker {
		return &fakeProbeInvoker{output: "OK", delay: delay}
	}

	start := time.Now()
	_ = ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory, nil, false)
	elapsed := time.Since(start)

	// Three sequential 50ms calls would take ~150ms. Parallel
	// should be ~50ms. Threshold at 100ms gives 50ms of slack
	// for CI runner jitter while still detecting the
	// serialisation regression.
	if elapsed > 100*time.Millisecond {
		t.Errorf("ProbeAll took %v with delay=%v per probe — looks serial, expected parallel (<100ms)", elapsed, delay)
	}
}

func TestSplitReady_PartitionsByStatus(t *testing.T) {
	results := []ProbeResult{
		{LLM: "claude", Status: ProbeReady},
		{LLM: "gemini", Status: ProbeError, Err: errors.New("capacity")},
		{LLM: "codex", Status: ProbeReady},
		{LLM: "future", Status: ProbeTimeout, Err: fmt.Errorf("ctx deadline")},
	}
	ready, notReady := SplitReady(results)
	wantReady := []string{"claude", "codex"}
	wantNotReady := []string{"gemini", "future"}

	if !equalStringSlices(ready, wantReady) {
		t.Errorf("ready = %v, want %v", ready, wantReady)
	}
	if !equalStringSlices(notReady, wantNotReady) {
		t.Errorf("notReady = %v, want %v", notReady, wantNotReady)
	}
}

func TestProbeStatusString(t *testing.T) {
	// Pin the rendered names — they appear in logs / tests and
	// changing them would silently break greps and snapshot
	// assertions that aren't easy to find.
	cases := map[ProbeStatus]string{
		ProbeReady:        "ready",
		ProbeError:        "error",
		ProbeTimeout:      "timeout",
		ProbeCanceled:     "canceled",
		ProbeStatus(999):  "unknown",
		ProbeStatus(-100): "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ProbeStatus(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hungInvoker simulates a CLI whose subprocess hangs past
// SIGKILL: ignores ctx.Done() entirely and blocks until its own
// `release` channel closes. Without v0.10.5's race-on-ctx.Done
// pattern, a Probe against this invoker would hang for the full
// `release` duration regardless of the per-LLM ctx deadline.
// With the fix, Probe returns immediately when ctx expires; the
// hung goroutine drains in the background, unblocking only when
// the test signals release.
type hungInvoker struct {
	release chan struct{} // close to unblock; nil = block forever
}

func (h *hungInvoker) Review(ctx context.Context, _, _ string) (string, TokenUsage, error) {
	return "", TokenUsage{}, errors.New("Review not used in probe tests")
}

func (h *hungInvoker) RunPrompt(_ context.Context, _ string) (string, TokenUsage, error) {
	// Deliberately ignore ctx — mirrors the bug we're testing
	// against (real CLIs whose cmd.Wait() blocks on pipe drain
	// after exec.CommandContext SIGKILLs the process).
	if h.release != nil {
		<-h.release
	}
	return "OK", TokenUsage{}, nil
}

// Regression test for the v0.10.5 probe-timeout fix. Pre-fix,
// Probe called inv.RunPrompt synchronously; if the underlying
// CLI hung past ctx.Deadline (the v0.10.0-RC and PR #86 dogfood
// observed cases), the probe phase wallclock was bounded by the
// CLI's subprocess-death time, not the per-LLM ctx deadline. A
// 10s ctx deadline could produce a 4-minute probe phase.
//
// With the fix in place, Probe must return ProbeTimeout within a
// small margin of the ctx deadline, regardless of whether the
// underlying invoker honors ctx cancellation.
func TestProbe_RespectsCtxDeadlineEvenWhenInvokerHangs(t *testing.T) {
	// Tight deadline (30ms) + an invoker that ignores ctx and
	// will block forever unless we explicitly release it. The
	// test concludes within tens of ms; the goroutine the fix
	// leaks is reaped via defer-close at the end.
	release := make(chan struct{})
	defer close(release) // unblocks the leaked goroutine on test exit
	inv := &hungInvoker{release: release}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	r := Probe(ctx, inv, "claude")
	elapsed := time.Since(start)

	if r.Status != ProbeTimeout {
		t.Errorf("Status = %s, want ProbeTimeout (hung invoker should not let probe complete normally)", r.Status)
	}
	// Threshold: the 30ms deadline plus generous slack for the
	// goroutine + channel + select scheduler. 200ms gives enough
	// headroom for CI runner jitter while still failing if the
	// fix is regressed (pre-fix this would have been blocked
	// forever, exceeding any threshold).
	if elapsed > 200*time.Millisecond {
		t.Errorf("Probe took %v with hung invoker and 30ms ctx — race-on-ctx.Done is broken", elapsed)
	}
}

// Companion test: same hung-invoker shape but inside ProbeAll's
// fan-out. v0.10.0-RC's dogfood specifically reported the probe
// PHASE wallclock as 4m34s when one LLM hung; the fan-out should
// be bounded by the per-LLM timeout, not the slow LLM's
// subprocess-death time.
func TestProbeAll_PhaseRespectsTimeoutEvenWhenOneInvokerHangs(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	llms := []LLM{
		{Name: "claude", Path: "fake"},
		{Name: "gemini-hung", Path: "fake"},
		{Name: "codex", Path: "fake"},
	}
	factory := func(l LLM) Invoker {
		if l.Name == "gemini-hung" {
			return &hungInvoker{release: release}
		}
		return &fakeProbeInvoker{output: "OK"}
	}

	const perLLM = 50 * time.Millisecond
	start := time.Now()
	results := ProbeAllWithInvokerFactory(context.Background(), llms, perLLM, factory, nil, false)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	// gemini-hung must surface as Timeout, not block the whole phase.
	if results[1].Status != ProbeTimeout {
		t.Errorf("gemini-hung result = %s, want ProbeTimeout", results[1].Status)
	}
	// Phase wallclock must be bounded by perLLM + scheduler slack,
	// NOT by however long the hung invoker would have taken.
	if elapsed > perLLM+200*time.Millisecond {
		t.Errorf("ProbeAll phase took %v with one hung invoker and %v per-LLM cap — fan-out is not respecting the cap", elapsed, perLLM)
	}
}

// v0.10.5 added the canceled-vs-timeout distinction. Before this,
// a user Ctrl+C during the probe phase would surface as
// "every active LLM failed pre-flight" because Probe returned
// ProbeTimeout indiscriminately on ctx.Done(). The fix branches
// on ctx.Err() inside the Done() case.
func TestProbe_CanceledIsDistinctFromTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	inv := &hungInvoker{release: release}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately — the ctx.Done() branch fires with
	// ctx.Err() == context.Canceled (not DeadlineExceeded).
	cancel()

	r := Probe(ctx, inv, "claude")
	if r.Status != ProbeCanceled {
		t.Errorf("Status = %s, want ProbeCanceled (user-cancel must not look like a timeout)", r.Status)
	}
	if !errors.Is(r.Err, context.Canceled) {
		t.Errorf("Err = %v, want to wrap context.Canceled", r.Err)
	}
}

// And the inverse — a DeadlineExceeded must still classify as
// ProbeTimeout, not ProbeCanceled. The distinction matters at
// the runner: timeout = skip this LLM, canceled = abort the run.
func TestProbe_DeadlineExceededStillTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	inv := &hungInvoker{release: release}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	r := Probe(ctx, inv, "claude")
	if r.Status != ProbeTimeout {
		t.Errorf("Status = %s, want ProbeTimeout (deadline expiry must not look like a cancel)", r.Status)
	}
	if !errors.Is(r.Err, context.DeadlineExceeded) {
		t.Errorf("Err = %v, want to wrap context.DeadlineExceeded", r.Err)
	}
}

// Race window: when ctx is canceled, the invoker may honor ctx
// and return context.Canceled BEFORE Probe's outer
// case <-ctx.Done() fires. Without the guard at the top of
// classifyProbeOutcome, the canceled result would have fallen
// through to ProbeError instead of ProbeCanceled — surfacing as
// "every LLM failed pre-flight" instead of propagating cleanly.
// (Claude + codex both caught this on PR #88's second dogfood.)
//
// The fake invoker here returns context.Canceled DIRECTLY rather
// than blocking long enough for ctx.Done() to fire — simulating
// a CLI that honors ctx promptly. This forces the result through
// the outcomeCh path, exercising classifyProbeOutcome's canceled
// branch instead of Probe's outer Done() branch.
func TestProbe_CanceledOutcomeFromInvokerStillClassifiesAsCanceled(t *testing.T) {
	inv := &fakeProbeInvoker{err: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel; invoker reads ctx.Err and returns ctx.Err
	// Race: depending on Go scheduler, Probe's outer Done() may
	// fire first OR the invoker's outcomeCh send may win. Either
	// way the result MUST be ProbeCanceled, never ProbeError.

	r := Probe(ctx, inv, "claude")
	if r.Status != ProbeCanceled {
		t.Errorf("Status = %s, want ProbeCanceled (canceled invoker return must not fall through to ProbeError)", r.Status)
	}
}

// hungInvokerWithPartial extends hungInvoker with a
// PartialStderrCapturer impl, returning a canned vendor message.
// Mirrors what the real ClaudeInvoker / GeminiInvoker / CodexInvoker
// expose: while RunPrompt is blocked on the subprocess pipe drain,
// the partial-stderr buffer already holds whatever the CLI wrote
// before hanging.
type hungInvokerWithPartial struct {
	hungInvoker
	partialMsg string
}

func (h *hungInvokerWithPartial) PartialStderr() string {
	return h.partialMsg
}

// v0.10.6 timeout-classification invariants — table-driven so
// the shared "hung invoker + tight ctx + Probe call" scaffolding
// lives once, and each row pins one observable property of the
// resulting ProbeResult. Sonar flagged duplication across the
// four-test layout the first build had; consolidating here lands
// the same coverage with the setup in one place.
//
// Each case asserts whatever subset of (Status, Err-must-contain,
// Err-must-NOT-contain, errors.Is-unwrap) is meaningful for the
// scenario; empty fields are skipped. Test names follow CLAUDE.md
// rule 9 — each case encodes an invariant, not a call shape.
func TestProbe_TimeoutClassification(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	vendorMsg := "Error: You have exhausted your capacity on this model."

	cases := []struct {
		name            string
		invoker         Invoker
		wantContains    []string // every substring must appear in Err.Error()
		wantNotContains []string // none of these may appear in Err.Error()
		wantUnwraps     error    // errors.Is(Err, this) must hold; nil = skip
	}{
		{
			name: "partial_stderr_surfaces_as_reason",
			invoker: &hungInvokerWithPartial{
				hungInvoker: hungInvoker{release: release},
				partialMsg:  vendorMsg,
			},
			wantContains: []string{"exhausted your capacity", "timeout after"},
			wantUnwraps:  context.DeadlineExceeded,
		},
		{
			// Plain hungInvoker doesn't implement PartialStderrCapturer.
			// Probe must fall through to the generic ctx.Err() path —
			// never panic on the missing optional interface.
			name:        "no_partial_capturer_falls_back_to_ctxerr",
			invoker:     &hungInvoker{release: release},
			wantUnwraps: context.DeadlineExceeded,
		},
		{
			// Whitespace-only partial → fall back, else we'd render
			// `gemini ✗ timeout after 10s — ` with trailing dash.
			name: "whitespace_partial_falls_back_to_ctxerr",
			invoker: &hungInvokerWithPartial{
				hungInvoker: hungInvoker{release: release},
				partialMsg:  "   \n\t  ",
			},
			wantUnwraps: context.DeadlineExceeded,
		},
		{
			// The rendered Error() must NOT include "context
			// deadline exceeded" — display stays clean, the
			// unwrap chain is for programmatic checks (see
			// previous case which pins the unwrap).
			name: "rendered_text_omits_unwrap_target_string",
			invoker: &hungInvokerWithPartial{
				hungInvoker: hungInvoker{release: release},
				partialMsg:  vendorMsg,
			},
			wantNotContains: []string{"context deadline exceeded"},
			wantUnwraps:     context.DeadlineExceeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()

			r := Probe(ctx, tc.invoker, "test-llm")
			if r.Status != ProbeTimeout {
				t.Fatalf("Status = %s, want ProbeTimeout", r.Status)
			}
			if r.Err == nil {
				t.Fatal("Err is nil; expected ctx-Err or partial-stderr error")
			}
			errStr := r.Err.Error()
			for _, sub := range tc.wantContains {
				if !strings.Contains(errStr, sub) {
					t.Errorf("Err = %q, want it to contain %q", errStr, sub)
				}
			}
			for _, sub := range tc.wantNotContains {
				if strings.Contains(errStr, sub) {
					t.Errorf("Err = %q, want it NOT to contain %q (display should stay clean)", errStr, sub)
				}
			}
			if tc.wantUnwraps != nil && !errors.Is(r.Err, tc.wantUnwraps) {
				t.Errorf("errors.Is(Err, %v) = false, want true", tc.wantUnwraps)
			}
		})
	}
}

// --- Provider probe dispatch (v0.14 unified-agent series, PR 3) -------------

// Default (non-strict) mode: provider agents go through the cheap
// HTTP /v1/models probe — NOT Invoker.RunPrompt. providerProbeFn
// passed as a parameter (per-call seam, no package globals) so tests
// can run in parallel without reassignment races (PR 3 self-review
// catch).
func TestProbeAll_ProviderLight_UsesHTTPProbeNotInvoker(t *testing.T) {
	llms := []LLM{
		{Name: "qwen", BaseURL: "http://example/v1", Model: "qwen2.5-coder:7b"},
	}
	// Track which path ran. If the dispatcher routes correctly to the
	// light probe, the invoker factory below must NEVER be called.
	var invokerCalled bool
	factory := func(LLM) Invoker {
		invokerCalled = true
		return &fakeProbeInvoker{output: "OK"}
	}
	probeFn := func(context.Context, string, string, time.Duration) error { return nil }

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 2*time.Second, factory, probeFn, false /* strict */)
	if len(results) != 1 {
		t.Fatalf("len: got %d, want 1", len(results))
	}
	if !results[0].IsReady() {
		t.Errorf("provider should be Ready when light probe succeeds; got %s (%v)", results[0].Status, results[0].Err)
	}
	if invokerCalled {
		t.Error("light-probe path must NOT invoke the chat-completion Invoker (would defeat the cheap-probe contract)")
	}
}

// Strict mode: provider agents fall through to the Invoker path so the
// configured model id is actually exercised (catches "endpoint up but
// model not loaded"). The HTTP-probe seam must NOT be called.
func TestProbeAll_ProviderStrict_UsesInvokerNotHTTPProbe(t *testing.T) {
	llms := []LLM{
		{Name: "qwen", BaseURL: "http://example/v1", Model: "qwen2.5-coder:7b"},
	}
	invokerCalled := false
	factory := func(LLM) Invoker {
		invokerCalled = true
		return &fakeProbeInvoker{output: "OK"}
	}
	httpProbeCalled := false
	probeFn := func(context.Context, string, string, time.Duration) error {
		httpProbeCalled = true
		return nil
	}

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 2*time.Second, factory, probeFn, true /* strict */)
	if !results[0].IsReady() {
		t.Errorf("strict-mode provider must be Ready when Invoker succeeds; got %s", results[0].Status)
	}
	if !invokerCalled {
		t.Error("strict mode must route providers through the Invoker (real chat completion)")
	}
	if httpProbeCalled {
		t.Error("strict mode must NOT also fire the HTTP light probe")
	}
}

// HTTP probe failure surfaces with the right ProbeStatus (Timeout vs
// Error) so the readiness-block renderer can pick the right glyph and
// the runner's cancel-vs-timeout discrimination still works. The
// probe blocks until the per-LLM ctx fires, then returns ctx.Err()
// directly — exercises the unwrapped path.
func TestProbeAll_ProviderLight_TimeoutMapsToProbeTimeout(t *testing.T) {
	llms := []LLM{
		{Name: "slow-qwen", BaseURL: "http://example/v1"},
	}
	probeFn := func(ctx context.Context, _, _ string, _ time.Duration) error {
		<-ctx.Done() // block until the probe's per-LLM ctx fires
		return ctx.Err()
	}

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 20*time.Millisecond, func(LLM) Invoker { return nil }, probeFn, false)
	if results[0].Status != ProbeTimeout {
		t.Errorf("blocked probe past the deadline must yield ProbeTimeout, got %s (%v)", results[0].Status, results[0].Err)
	}
}

// provider.Probe wraps its error chain (`fmt.Errorf("reach %s: %w", url,
// ctx.Err())`) AND has its own inner timeout that may fire before the
// caller's pctx. The classifier must unwrap via errors.Is so wrapped
// DeadlineExceeded still maps to ProbeTimeout (not a generic
// ProbeError + ✗ glyph). Self-review (PR 3) flagged the gap.
func TestProbeAll_ProviderLight_WrappedDeadlineMapsToTimeout(t *testing.T) {
	llms := []LLM{
		{Name: "slow", BaseURL: "http://example/v1"},
	}
	probeFn := func(ctx context.Context, _, _ string, _ time.Duration) error {
		// Return a WRAPPED deadline (matches what provider.Probe actually
		// produces) without the caller's pctx having fired yet — proves
		// the classifier consults the error chain, not just pctx.Err().
		return fmt.Errorf("reach http://example/v1/models: %w", context.DeadlineExceeded)
	}
	results := ProbeAllWithInvokerFactory(context.Background(), llms, 2*time.Second, func(LLM) Invoker { return nil }, probeFn, false)
	if results[0].Status != ProbeTimeout {
		t.Errorf("wrapped DeadlineExceeded must classify as ProbeTimeout, got %s (%v)", results[0].Status, results[0].Err)
	}
}
