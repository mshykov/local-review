package cli

import (
	"fmt"
	"math"
	"strings"
)

// DefaultTimeoutSec is the per-agent timeout in seconds used when
// the resolved LLMConfig has no explicit value. Single source of
// truth shared across the runner's applyConfig fallback, the
// orchestrator's RunParallel fallback, the merge-step fallback,
// and the roster line that shows "timeout: Ns" on the pre-run
// roster. Centralising prevents drift between what the user sees
// ("timeout: Ns") and what actually fires.
//
// 10 minutes accommodates worst-case agents (Anthropic Sonnet on a
// thinking model) on worst-case diffs. Users wanting shorter
// timeouts override per-agent via `llms.<agent>.timeout_seconds:`
// in `.local-review.yml`.
const DefaultTimeoutSec = 600

// bytesPerToken is the rough byte-to-token ratio used by
// EstimateTokens. The implementation reads `len(s)` (bytes, not
// runes), so the constant name now reflects what's actually
// measured. Real tokenisers (tiktoken, sentencepiece) give exact
// counts but require pulling vendor SDKs we deliberately don't
// depend on; this approximation only needs to be good enough to
// decide "fit or skip."
//
// 3.5 is conservative for code-heavy English. Combined with
// math.Ceil rounding in EstimateTokens, the result is a true
// upper bound, which biases preflight toward skipping rather than
// feeding an oversized prompt to a model that will OOM or 4xx.
//
// If a future user reports "preflight skipped my agent but the
// real call would have fit", lower this number.
const bytesPerToken = 3.5

// responseSafetyMargin reserves capacity for the LLM's own response
// inside the context window. A code review reply typically runs
// 2K-8K tokens; 10K is comfortably above that, leaving room for
// the system prompt + diff to use the rest.
const responseSafetyMargin = 10_000

// ContextWindow returns the conservative per-LLM context window we
// preflight against, in tokens. Values are the smallest current-
// stable model in each vendor's lineup so we never falsely pass a
// diff that would 4xx on a smaller model.
//
// Why a static table instead of probing the CLI: each CLI exposes
// model context differently (or not at all), and the values change
// monthly. A small table here, conservative by design, fails-safe
// if a vendor *shrinks* a context window (rare but happens during
// rate-limit shifts) at the cost of over-skipping when a vendor
// *grows* one. Users can override per-agent via
// `llms.<agent>.context_window:` once that config field lands.
//
// Returns 0 for unknown agents — caller should treat that as
// "don't preflight, let the agent decide" so a future agent type
// doesn't get silently filtered.
func ContextWindow(agent string) int {
	switch agent {
	case "claude":
		// Sonnet 3.5/4.x and Opus all run 200K. Haiku 4.5 also 200K.
		return 200_000
	case "gemini":
		// Conservative floor. 2.5 Pro is 2M, 3.x ranges 1M-2M, 1.5
		// Pro was 2M. 1M as the floor covers every current stable
		// while leaving slack for previews that may shift down.
		return 1_000_000
	case "codex":
		// gpt-4o = 128K. gpt-5 family = 200K-400K. Conservative
		// floor at 128K means we'll over-skip on gpt-5 but won't
		// over-fit on a gpt-4o-default user.
		return 128_000
	default:
		return 0
	}
}

// SkippedAgent describes one agent that was preflight-skipped, with
// enough detail for the user to understand why and what to do about
// it.
//
// PromptDiffTokens is the upper-bound estimate of the *combined*
// payload sent to the CLI: the system prompt (after wrapping by
// buildReviewPrompt) plus the diff, with the "# Diff" header glue
// included. Named "PromptDiffTokens" rather than "EstimatedTokens"
// so the user-facing message can't accidentally claim "diff is X
// tokens" when X is actually prompt+diff.
type SkippedAgent struct {
	Name             string
	PromptDiffTokens int
	ContextWindow    int
}

// diffSeparator is the literal glue the invokers put between the
// system prompt and the diff before sending. Keep this in sync
// with each Review() implementation in invoker.go — currently all
// three (claude, gemini, codex) use the same shape.
const diffSeparator = "\n\n# Diff\n\n"

// EstimatePromptPayload returns an upper-bound token estimate for
// the exact bytes the invokers ship to the CLI: the wrapped system
// prompt + separator + diff. Using this — rather than estimating
// systemPrompt + diff in isolation — prevents preflight from
// passing an agent that will then 4xx because the actual sent
// payload was bigger than what we measured.
func EstimatePromptPayload(systemPrompt, diff string) int {
	full := buildReviewPrompt(systemPrompt) + diffSeparator + diff
	return EstimateTokens(full)
}

// PreflightFilter takes the active agent list and the system-prompt
// + diff that will be sent to each agent, returns the subset that
// fits in each agent's context window plus a list of skipped
// agents with their numbers.
//
// The filter is purely based on length — we don't know the user's
// model precisely (CLIs may pick their own default) so we use the
// conservative-floor table from ContextWindow. An agent whose name
// isn't in the table is passed through unchanged ("don't preflight
// what we don't know").
//
// Returns the filtered active list, the skipped-agent details, and
// the prompt+diff token estimate (useful for the caller's summary
// line on the all-skipped path).
func PreflightFilter(active []LLM, systemPrompt, diff string) (kept []LLM, skipped []SkippedAgent, promptDiffTokens int) {
	promptDiffTokens = EstimatePromptPayload(systemPrompt, diff)
	for _, a := range active {
		window := ContextWindow(a.Name)
		if window == 0 {
			// Unknown agent — pass through, let the agent's CLI
			// fail naturally if it's too big.
			kept = append(kept, a)
			continue
		}
		if promptDiffTokens+responseSafetyMargin > window {
			skipped = append(skipped, SkippedAgent{
				Name:             a.Name,
				PromptDiffTokens: promptDiffTokens,
				ContextWindow:    window,
			})
			continue
		}
		kept = append(kept, a)
	}
	return kept, skipped, promptDiffTokens
}

// EstimateTokens approximates the token count of s using a fixed
// byte-to-token ratio. Returns a true upper bound (math.Ceil over
// the byte/3.5 division) so preflight biases toward skipping rather
// than feeding an oversized prompt to a model that will 4xx.
//
// We don't pull in a real tokeniser (tiktoken, sentencepiece): they
// require SDK dependencies we deliberately avoid (vendor lock-in,
// binary bloat) and the preflight only needs an upper bound to
// decide "fit or skip". Off-by-20% in the conservative direction
// is fine.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / bytesPerToken))
}

// FormatSkipReason returns the user-facing one-liner explaining why
// an agent was preflight-skipped, with the same actionable-hint
// shape as ClassifyExit's failure messages: numbers + concrete fix.
//
// "prompt+diff" rather than "diff" because the estimate covers the
// full payload (system prompt + glue + diff) — saying only "diff is
// X tokens" misled users into thinking the raw diff alone exceeded
// the context window. Both the prompt+diff total AND the
// response-reservation appear because the gate is "payload + margin
// > window" — saying only "payload exceeds window" is factually
// wrong near the limit.
func FormatSkipReason(s SkippedAgent) string {
	return fmt.Sprintf(
		"prompt+diff is ~%s tokens; with ~%s reserved for the response, would exceed %s's %s context window — try a smaller diff: `local-review commit HEAD` (last commit) or `local-review staged` (staged only)",
		humanInt(s.PromptDiffTokens),
		humanInt(responseSafetyMargin),
		s.Name,
		humanInt(s.ContextWindow),
	)
}

// humanInt formats large integers with a "k" suffix for readability
// (280_000 → "280k"). Below 10k we keep digits; above we round to
// nearest thousand. The preflight numbers are always 5+ digits in
// the cases that trigger it, so this is mostly cosmetic.
func humanInt(n int) string {
	if n < 10_000 {
		return fmt.Sprintf("%d", n)
	}
	rounded := (n + 500) / 1000
	return fmt.Sprintf("%dk", rounded)
}

// SkipSummary returns a multi-line summary block listing every
// skipped agent and what the user should do. Empty string when
// none were skipped.
func SkipSummary(skipped []SkippedAgent) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range skipped {
		fmt.Fprintf(&b, "⚠ Skipping %s: %s\n", s.Name, FormatSkipReason(s))
	}
	return b.String()
}
