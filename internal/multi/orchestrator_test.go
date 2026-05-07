package multi

import (
	"context"
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
