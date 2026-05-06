package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// stderrTailMaxLen caps how much stderr we surface inline in the
// per-LLM failure line. Long stderr (codex's banner output, gemini's
// debug logs) drown the actionable signal — and the actionable bit
// ("auth required", "rate limited", "model not found") is almost
// always toward the end. We keep the tail.
const stderrTailMaxLen = 300

// ClassifyExit produces a short, user-facing summary of why a CLI
// subprocess failed, suitable for the per-LLM line:
//
//	[1/3] claude ✗ <ClassifyExit output>
//
// Every classification that we recognise ends with an *actionable*
// next step — "try a smaller diff", "raise timeout_sec", etc. — so a
// user staring at a failure has a path forward without diving into
// stderr or strace. Pre-fix the failure line was the bare wrapped
// error ("claude review failed: signal: killed (output: )"), which
// reliably triggered "delete the tool" frustration.
//
// agent is the LLM name ("claude", "gemini", "codex") — embedded into
// hints that reference per-agent config (e.g. `llms.<agent>.timeout_sec`).
func ClassifyExit(ctx context.Context, err error, combinedOutput []byte, agent string) string {
	// Context state is the most reliable signal: when the parent
	// context expired or was cancelled, the child's exec error is
	// often a generic "signal: killed" with no other distinguishing
	// info, but ctx.Err() has already settled to the right reason.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("timeout — try `local-review commit HEAD` for a smaller diff, or raise llms.%s.timeout_sec in .local-review.yml", agent)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	// SIGKILL detection via error string. Go's exec package formats
	// signal-killed exits as "signal: killed". String-matching is
	// less elegant than syscall.WaitStatus.Signaled() but cross-
	// platform without build-tag gymnastics — and Windows doesn't
	// have SIGKILL semantics anyway, so the behavior matches the
	// platform's reality.
	if strings.Contains(errMsg, "signal: killed") {
		// Avoid suggesting "--only to skip <agent>" because if the
		// user is already running with `--only <agent>` (one-agent
		// run that crashed), that hint is a contradiction. Smaller
		// diff is the universally correct first step regardless of
		// how the run was scoped.
		return fmt.Sprintf("killed — likely out of memory or a hard timeout for %s; try a smaller diff: `local-review commit HEAD` (last commit), `local-review staged` (staged only), or pin a smaller-context model via `llms.%s.model:`", agent, agent)
	}

	// Non-zero exit (no signal). Surface the stderr tail because
	// the CLIs emit actionable info there: claude says "Please run
	// `claude login`", gemini says "API key not valid", codex says
	// "Tool 'web_search_preview' is not supported with gpt-4", etc.
	if strings.HasPrefix(errMsg, "exit status ") || strings.Contains(errMsg, "exited with status") {
		stderr := strings.TrimSpace(string(combinedOutput))
		if stderr != "" {
			if len(stderr) > stderrTailMaxLen {
				stderr = "…" + stderr[len(stderr)-stderrTailMaxLen:]
			}
			return fmt.Sprintf("%s: %s", errMsg, stderr)
		}
		return fmt.Sprintf("%s (no stderr captured — re-run with `%s --help` to verify auth and model)", errMsg, agent)
	}

	if errMsg != "" {
		return errMsg
	}
	return "unknown error"
}
