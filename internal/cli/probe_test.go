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

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory)
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

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 30*time.Millisecond, factory)
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

	results := ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory)
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
	_ = ProbeAllWithInvokerFactory(context.Background(), llms, 5*time.Second, factory)
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
