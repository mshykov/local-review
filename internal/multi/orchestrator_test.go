package multi

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

// fakeInvoker pretends to be an LLM CLI for streaming tests. Blocks
// on `release` (a per-agent gate channel the test owns) before
// returning canned output, so the test drives completion order
// deterministically — no sleep deltas to invert under CI scheduler
// jitter. Real binaries would make tests slow, flaky, and dependent
// on the CI host's PATH; this fake sidesteps all three.
//
// release == nil means "respond immediately" — convenient for the
// fast-fail/close tests where order doesn't matter.
type fakeInvoker struct {
	release <-chan struct{}
	output  string
	err     error
	tokens  cli.TokenUsage
}

func (f *fakeInvoker) Review(ctx context.Context, _, _ string) (string, cli.TokenUsage, error) {
	if f.release == nil {
		return f.output, f.tokens, f.err
	}
	select {
	case <-f.release:
		return f.output, f.tokens, f.err
	case <-ctx.Done():
		return "", cli.TokenUsage{}, ctx.Err()
	}
}

func (f *fakeInvoker) RunPrompt(ctx context.Context, _ string) (string, cli.TokenUsage, error) {
	return f.Review(ctx, "", "")
}

func TestRunParallel_StreamsCompletionOrder(t *testing.T) {
	// Drive completion order with explicit release gates instead of
	// sleep deltas. Pre-fix used 10/50/150ms — small enough that a
	// loaded CI runner's goroutine scheduler could invert the first
	// two and make this test flaky. Now the test closes gates in
	// the desired order: codex first, then claude, then gemini.
	// Roster order (claude, gemini, codex) deliberately differs so
	// we know the channel yields in completion order, not roster.
	gates := map[string]chan struct{}{
		"claude": make(chan struct{}),
		"gemini": make(chan struct{}),
		"codex":  make(chan struct{}),
	}
	llms := []cli.LLM{
		{Name: "claude", Path: "fake", Version: "test", TimeoutSec: 30},
		{Name: "gemini", Path: "fake", Version: "test", TimeoutSec: 30},
		{Name: "codex", Path: "fake", Version: "test", TimeoutSec: 30},
	}
	storage := NewStorage(t.TempDir())
	orch := NewOrchestrator(llms, storage)
	orch.invokerFactory = func(l cli.LLM) cli.Invoker {
		return &fakeInvoker{release: gates[l.Name], output: "## Major\n- a finding\n"}
	}

	ch, err := orch.RunParallel(context.Background(), "", "diff", "abc123", "main")
	if err != nil {
		t.Fatalf("RunParallel returned err: %v", err)
	}

	// Release in desired completion order. We read each result
	// immediately after releasing its gate so the next gate-close
	// can't race ahead and let two agents finish "simultaneously"
	// from the channel's perspective.
	want := []string{"codex", "claude", "gemini"}
	for i, name := range want {
		close(gates[name])
		select {
		case r, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed early at i=%d (want %s)", i, name)
			}
			if r.LLM != name {
				t.Errorf("emission[%d] = %s, want %s", i, r.LLM, name)
			}
			if r.Error != nil {
				t.Errorf("agent %s failed: %v", r.LLM, r.Error)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s emission", name)
		}
	}
	// Channel should now be closed. Use a timeout-bounded select
	// instead of a bare `<-ch`: the orchestrator's outer goroutine
	// closes the channel only after wg.Wait() returns, which races
	// in microseconds with the last worker's wg.Done(). Normally
	// imperceptible, but under heavy CI load a bare receive could
	// hang briefly and turn this assertion into a flaky timeout.
	// 2s matches the rest of this file's "test never hangs" budget.
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("channel still has results after all agents released")
		}
	case <-time.After(2 * time.Second):
		t.Errorf("channel never closed within 2s after all agents released")
	}
}

func TestRunParallel_ChannelClosesAfterAll(t *testing.T) {
	// The streaming pattern only works if the channel reliably closes
	// — otherwise `for r := range ch` in the runner hangs forever.
	// Pin the close-after-wg.Wait contract: a five-agent run, all
	// finish, range loop exits.
	//
	// Pre-fix used 5 agents all named "fake" → all wrote to the same
	// SaveReview path concurrently, racing on disk and masking errors
	// (the test ignored r.Error). Now: unique names per agent + we
	// fail the test on any per-agent error so a SaveReview regression
	// would surface here instead of hiding behind a passing count.
	const n = 5
	llms := make([]cli.LLM, n)
	for i := range llms {
		llms[i] = cli.LLM{Name: fmt.Sprintf("fake-%d", i), Path: "fake", Version: "test", TimeoutSec: 30}
	}
	storage := NewStorage(t.TempDir())
	orch := NewOrchestrator(llms, storage)
	orch.invokerFactory = func(cli.LLM) cli.Invoker {
		return &fakeInvoker{output: "x"}
	}

	ch, err := orch.RunParallel(context.Background(), "", "diff", "abc", "main")
	if err != nil {
		t.Fatalf("RunParallel: %v", err)
	}

	// Drain on a goroutine and report the final count via a channel
	// so the main goroutine never reads `count` while the consumer
	// is still writing it. Pre-fix had the consumer write `count`
	// and the timeout arm read it; race-detector latent under the
	// regression case (channel never closes) which is exactly when
	// you don't want a second alarm masking the real bug.
	type drain struct {
		count int
		errs  []error
	}
	doneCh := make(chan drain, 1)
	go func() {
		var d drain
		for r := range ch {
			d.count++
			if r.Error != nil {
				d.errs = append(d.errs, r.Error)
			}
		}
		doneCh <- d
	}()

	select {
	case d := <-doneCh:
		if d.count != n {
			t.Errorf("got %d results, want %d", d.count, n)
		}
		if len(d.errs) > 0 {
			t.Errorf("per-agent errors (test storage path race?): %v", d.errs)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("channel never closed within 2s")
	}
}

func TestRunParallel_FastFailErrorsStillStream(t *testing.T) {
	// A per-agent failure (invoker returns err, or save fails, or the
	// invoker factory returns nil for an unknown name) must still
	// emit on the channel — the runner relies on getting one
	// ReviewResult per LLM regardless of outcome. Pre-streaming,
	// failures lived in the result slice; with streaming a missing
	// emission would silently shrink the agent count and make the
	// "X/N produced output" line lie.
	llms := []cli.LLM{
		{Name: "claude", Path: "fake", Version: "test", TimeoutSec: 30},
		{Name: "broken", Path: "fake", Version: "test", TimeoutSec: 30},
	}
	storage := NewStorage(t.TempDir())
	orch := NewOrchestrator(llms, storage)
	orch.invokerFactory = func(l cli.LLM) cli.Invoker {
		if l.Name == "broken" {
			return nil // simulates cli.NewInvoker on unknown name
		}
		return &fakeInvoker{output: "ok"}
	}

	ch, _ := orch.RunParallel(context.Background(), "", "diff", "abc", "main")
	results := make(map[string]ReviewResult)
	for r := range ch {
		results[r.LLM] = r
	}
	if len(results) != 2 {
		t.Fatalf("got %d emissions, want 2", len(results))
	}
	if results["broken"].Error == nil {
		t.Errorf("broken agent should have Error set")
	}
	if results["claude"].Error != nil {
		t.Errorf("claude agent should have succeeded, got: %v", results["claude"].Error)
	}
}

// --- GateDecision tests ----------------------------------------------------
//
// These tests pin the *meaning* of the gate (per CLAUDE.md rule 9: tests
// encode the invariant, not the call). The dual-metric pattern that
// preceded GateDecision drifted across 5 review rounds historically
// (`CountSuccessful` → `CountWithOutput` migration); naming each case
// for the underlying scenario makes a regression obvious from the
// failure line, not from re-reading the body.

func TestDecideGate_AllSuccessful(t *testing.T) {
	results := []ReviewResult{
		{LLM: "claude", Output: "finding"},
		{LLM: "gemini", Output: "finding"},
		{LLM: "codex", Output: "finding"},
	}
	g := DecideGate(results)
	if g.Total != 3 || g.Successful != 3 || g.WithOutput != 3 {
		t.Errorf("Total/Successful/WithOutput = %d/%d/%d, want 3/3/3", g.Total, g.Successful, g.WithOutput)
	}
	if !g.HasMergeable() {
		t.Error("HasMergeable() = false, want true (3 reviews with output)")
	}
	if got := g.Failed(); got != 0 {
		t.Errorf("Failed() = %d, want 0", got)
	}
}

func TestDecideGate_AllFailed(t *testing.T) {
	agentFailed := errors.New("agent failed")
	results := []ReviewResult{
		{LLM: "claude", Error: agentFailed},
		{LLM: "gemini", Error: agentFailed},
	}
	g := DecideGate(results)
	if g.Total != 2 || g.Successful != 0 || g.WithOutput != 0 {
		t.Errorf("counts = %d/%d/%d, want 2/0/0", g.Total, g.Successful, g.WithOutput)
	}
	if g.HasMergeable() {
		t.Error("HasMergeable() = true, want false (no output)")
	}
	if got := g.ClassifyZero(); got != ZeroMergeableAllFailed {
		t.Errorf("ClassifyZero() = %v, want ZeroMergeableAllFailed", got)
	}
}

// "Succeeded but empty" is a real LLM-emitter pathology — the CLI exits
// 0 with blank stdout. Pre-consolidation this slipped past a Successful-
// only gate check and reached the merger with nothing to consume.
func TestDecideGate_AllSucceededButEmpty(t *testing.T) {
	results := []ReviewResult{
		{LLM: "claude", Output: ""},
		{LLM: "gemini", Output: "   \n\t"},
	}
	g := DecideGate(results)
	if g.Total != 2 || g.Successful != 2 || g.WithOutput != 0 {
		t.Errorf("counts = %d/%d/%d, want 2/2/0", g.Total, g.Successful, g.WithOutput)
	}
	if g.HasMergeable() {
		t.Error("HasMergeable() = true, want false (whitespace-only output is not mergeable)")
	}
	if got := g.ClassifyZero(); got != ZeroMergeableAllEmpty {
		t.Errorf("ClassifyZero() = %v, want ZeroMergeableAllEmpty (every Error nil, no Output)", got)
	}
}

func TestDecideGate_MixedFailedAndEmpty(t *testing.T) {
	agentFailed := errors.New("agent failed")
	results := []ReviewResult{
		{LLM: "claude", Error: agentFailed},
		{LLM: "gemini", Output: ""}, // succeeded with no output
	}
	g := DecideGate(results)
	if g.Total != 2 || g.Successful != 1 || g.WithOutput != 0 {
		t.Errorf("counts = %d/%d/%d, want 2/1/0", g.Total, g.Successful, g.WithOutput)
	}
	if g.HasMergeable() {
		t.Error("HasMergeable() = true, want false")
	}
	if got := g.ClassifyZero(); got != ZeroMergeableMixed {
		t.Errorf("ClassifyZero() = %v, want ZeroMergeableMixed (one failed, one succeeded with no output)", got)
	}
}

// SaveReview-failed-with-output: Error is set but Output is populated.
// The merger CAN still consume the in-memory output. Pre-consolidation
// a Successful-only check would abort the merge here ("all failed")
// even though the merger would have produced a valid consolidated
// report. GateDecision must count this as mergeable.
func TestDecideGate_SaveReviewFailedWithOutput_StillMergeable(t *testing.T) {
	saveErr := errors.New("save review: disk full")
	results := []ReviewResult{
		{LLM: "claude", Output: "finding", Error: saveErr},
		{LLM: "gemini", Output: "finding", Error: saveErr},
	}
	g := DecideGate(results)
	if g.Total != 2 || g.Successful != 0 || g.WithOutput != 2 {
		t.Errorf("counts = %d/%d/%d, want 2/0/2 (both save-failed but both have output)", g.Total, g.Successful, g.WithOutput)
	}
	if !g.HasMergeable() {
		t.Error("HasMergeable() = false, want true (save failure does not erase in-memory Output)")
	}
}

// Mixed real-world shape that surfaces both views diverging at once.
// One succeeded with output, one save-failed with output, one crashed:
// Successful=1 (only the clean one), WithOutput=2 (the two with output),
// Failed=2 (save-fail counts as failed in the Error sense).
func TestDecideGate_MixedShapeBothViewsDiverge(t *testing.T) {
	saveErr := errors.New("save review: disk full")
	hardErr := errors.New("claude review failed: signal: killed")
	results := []ReviewResult{
		{LLM: "claude", Output: "finding"},
		{LLM: "gemini", Output: "finding", Error: saveErr},
		{LLM: "codex", Error: hardErr},
	}
	g := DecideGate(results)
	if g.Total != 3 || g.Successful != 1 || g.WithOutput != 2 {
		t.Errorf("counts = %d/%d/%d, want 3/1/2", g.Total, g.Successful, g.WithOutput)
	}
	if got := g.Failed(); got != 2 {
		t.Errorf("Failed() = %d, want 2", got)
	}
	if !g.HasMergeable() {
		t.Error("HasMergeable() = false, want true (2 with output)")
	}
}

func TestDecideGate_Empty(t *testing.T) {
	g := DecideGate(nil)
	if g.Total != 0 || g.Successful != 0 || g.WithOutput != 0 {
		t.Errorf("counts = %d/%d/%d, want all zero for nil input", g.Total, g.Successful, g.WithOutput)
	}
	if g.HasMergeable() {
		t.Error("HasMergeable() = true on empty set, want false")
	}
}
