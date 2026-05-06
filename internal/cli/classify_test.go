package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClassifyExit_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // ensure deadline passed
	got := ClassifyExit(ctx, errors.New("signal: killed"), nil, "claude")
	if !strings.Contains(got, "timeout") {
		t.Errorf("want classification to mention 'timeout', got: %q", got)
	}
	if !strings.Contains(got, "llms.claude.timeout_sec") {
		t.Errorf("want hint to reference per-agent timeout_sec config, got: %q", got)
	}
	if !strings.Contains(got, "smaller diff") {
		t.Errorf("want hint to suggest smaller diff, got: %q", got)
	}
}

func TestClassifyExit_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ClassifyExit(ctx, errors.New("signal: killed"), nil, "claude")
	if got != "cancelled" {
		t.Errorf("want 'cancelled', got: %q", got)
	}
}

func TestClassifyExit_SIGKILL(t *testing.T) {
	got := ClassifyExit(context.Background(), errors.New("signal: killed"), nil, "claude")
	if !strings.Contains(got, "killed") {
		t.Errorf("want classification to mention 'killed', got: %q", got)
	}
	if !strings.Contains(got, "out of memory") {
		t.Errorf("want hint to mention OOM, got: %q", got)
	}
	if !strings.Contains(got, "local-review commit HEAD") {
		t.Errorf("want hint to suggest commit-only review, got: %q", got)
	}
	if !strings.Contains(got, "--only") {
		t.Errorf("want hint to mention --only, got: %q", got)
	}
}

func TestClassifyExit_NonZeroWithStderr(t *testing.T) {
	stderr := []byte("error: API key not valid. Please pass a valid API key.")
	got := ClassifyExit(context.Background(), errors.New("exit status 1"), stderr, "gemini")
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("want exit status preserved, got: %q", got)
	}
	if !strings.Contains(got, "API key not valid") {
		t.Errorf("want stderr surfaced, got: %q", got)
	}
}

func TestClassifyExit_NonZeroWithoutStderr(t *testing.T) {
	got := ClassifyExit(context.Background(), errors.New("exit status 1"), nil, "codex")
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("want exit status, got: %q", got)
	}
	if !strings.Contains(got, "no stderr captured") {
		t.Errorf("want note that stderr was empty, got: %q", got)
	}
	if !strings.Contains(got, "codex") {
		t.Errorf("want agent name in fallback hint, got: %q", got)
	}
}

func TestClassifyExit_LongStderrTruncatedToTail(t *testing.T) {
	// Build a stderr much longer than stderrTailMaxLen so truncation
	// fires. The actionable info ("rate limit exceeded") goes at the
	// end so we can assert it survives — that's the contract.
	leadMarker := "DROP_THIS_IT_IS_THE_HEAD_AND_SHOULD_BE_TRUNCATED"
	prefix := leadMarker + strings.Repeat("x", 5000) // way past stderrTailMaxLen
	tail := "rate limit exceeded; retry after 60s"
	stderr := []byte(prefix + tail)
	got := ClassifyExit(context.Background(), errors.New("exit status 1"), stderr, "claude")
	if !strings.Contains(got, "rate limit exceeded") {
		t.Errorf("want tail preserved, got: %q", got)
	}
	if strings.Contains(got, leadMarker) {
		t.Errorf("want lead truncated, got: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("want truncation marker, got: %q", got)
	}
}

func TestClassifyExit_UnrecognisedErrorPassesThrough(t *testing.T) {
	got := ClassifyExit(context.Background(), errors.New("some weird error"), nil, "claude")
	if got != "some weird error" {
		t.Errorf("want passthrough, got: %q", got)
	}
}

func TestClassifyExit_NilError(t *testing.T) {
	got := ClassifyExit(context.Background(), nil, nil, "claude")
	if got != "unknown error" {
		t.Errorf("want 'unknown error' for nil err, got: %q", got)
	}
}
