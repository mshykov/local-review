package multi

import (
	"context"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

// fakeInvoker pretends to be an LLM CLI for streaming tests. Sleeps
// for `delay` then returns either canned output or an error. We use
// it to drive the orchestrator's channel without shelling out to
// real binaries (which would make tests slow, flaky, and dependent
// on the CI host's PATH).
type fakeInvoker struct {
	delay  time.Duration
	output string
	err    error
	tokens cli.TokenUsage
}

func (f *fakeInvoker) Review(ctx context.Context, _, _ string) (string, cli.TokenUsage, error) {
	select {
	case <-time.After(f.delay):
		return f.output, f.tokens, f.err
	case <-ctx.Done():
		return "", cli.TokenUsage{}, ctx.Err()
	}
}

func (f *fakeInvoker) RunPrompt(ctx context.Context, _ string) (string, cli.TokenUsage, error) {
	return f.Review(ctx, "", "")
}

func TestRunParallel_StreamsCompletionOrder(t *testing.T) {
	// Seed three agents with very different durations so completion
	// order is deterministic: codex finishes first (10ms), claude
	// second (50ms), gemini last (150ms). The roster order
	// (claude, gemini, codex) deliberately differs so we know the
	// channel is yielding in completion order, not roster order.
	delays := map[string]time.Duration{
		"claude": 50 * time.Millisecond,
		"gemini": 150 * time.Millisecond,
		"codex":  10 * time.Millisecond,
	}
	llms := []cli.LLM{
		{Name: "claude", Path: "fake", Version: "test", TimeoutSec: 30},
		{Name: "gemini", Path: "fake", Version: "test", TimeoutSec: 30},
		{Name: "codex", Path: "fake", Version: "test", TimeoutSec: 30},
	}
	storage := NewStorage(t.TempDir())
	orch := NewOrchestrator(llms, storage)
	orch.invokerFactory = func(l cli.LLM) cli.Invoker {
		return &fakeInvoker{delay: delays[l.Name], output: "## Major\n- a finding\n"}
	}

	ch, err := orch.RunParallel(context.Background(), "", "diff", "abc123", "main")
	if err != nil {
		t.Fatalf("RunParallel returned err: %v", err)
	}

	var order []string
	for r := range ch {
		order = append(order, r.LLM)
		if r.Error != nil {
			t.Errorf("agent %s failed: %v", r.LLM, r.Error)
		}
	}

	want := []string{"codex", "claude", "gemini"}
	if len(order) != len(want) {
		t.Fatalf("got %d results, want %d", len(order), len(want))
	}
	for i, name := range want {
		if order[i] != name {
			t.Errorf("emission[%d] = %s, want %s (full order: %v)", i, order[i], name, order)
		}
	}
}

func TestRunParallel_ChannelClosesAfterAll(t *testing.T) {
	// The streaming pattern only works if the channel reliably closes
	// — otherwise `for r := range ch` in the runner hangs forever.
	// Pin the close-after-wg.Wait contract: a five-agent run, all
	// finish, range loop exits.
	llms := make([]cli.LLM, 5)
	for i := range llms {
		llms[i] = cli.LLM{Name: "fake", Path: "fake", Version: "test", TimeoutSec: 30}
	}
	storage := NewStorage(t.TempDir())
	orch := NewOrchestrator(llms, storage)
	orch.invokerFactory = func(cli.LLM) cli.Invoker {
		return &fakeInvoker{delay: time.Millisecond, output: "x"}
	}

	ch, err := orch.RunParallel(context.Background(), "", "diff", "abc", "main")
	if err != nil {
		t.Fatalf("RunParallel: %v", err)
	}
	count := 0
	done := make(chan struct{})
	go func() {
		for range ch {
			count++
		}
		close(done)
	}()
	select {
	case <-done:
		// Channel closed cleanly.
	case <-time.After(2 * time.Second):
		t.Fatalf("channel never closed; got %d results before timing out", count)
	}
	if count != 5 {
		t.Errorf("got %d results, want 5", count)
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
		return &fakeInvoker{delay: time.Millisecond, output: "ok"}
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
