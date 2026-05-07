package cli

import (
	"fmt"
	"strings"
)

// charsPerToken is the rough byte-to-token ratio for code-heavy
// English. Real tokenisers (tiktoken, sentencepiece) give exact
// counts but require pulling vendor SDKs we deliberately don't
// depend on. The 3.5 ratio is conservative — it slightly over-
// estimates token count, which biases the preflight toward
// skipping rather than feeding an oversized prompt to a model that
// will OOM or 4xx.
//
// If a future user reports "preflight skipped my agent but the
// real call would have fit", lower this number.
const charsPerToken = 3.5

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
type SkippedAgent struct {
	Name             string
	EstimatedTokens  int
	ContextWindow    int
}

// PreflightFilter takes the active agent list and the prompt+diff
// payload, returns the subset that fits in each agent's context
// window plus a list of skipped agents with their numbers.
//
// The filter is purely based on length — we don't know the user's
// model precisely (CLIs may pick their own default) so we use the
// conservative-floor table from ContextWindow. An agent whose name
// isn't in the table is passed through unchanged ("don't preflight
// what we don't know").
//
// Returns the filtered active list, the skipped-agent details, and
// the estimated token count of the input (useful for the caller's
// summary line).
func PreflightFilter(active []LLM, prompt, diff string) (kept []LLM, skipped []SkippedAgent, estimatedTokens int) {
	estimatedTokens = EstimateTokens(prompt) + EstimateTokens(diff)
	for _, a := range active {
		window := ContextWindow(a.Name)
		if window == 0 {
			// Unknown agent — pass through, let the agent's CLI
			// fail naturally if it's too big.
			kept = append(kept, a)
			continue
		}
		if estimatedTokens+responseSafetyMargin > window {
			skipped = append(skipped, SkippedAgent{
				Name:            a.Name,
				EstimatedTokens: estimatedTokens,
				ContextWindow:   window,
			})
			continue
		}
		kept = append(kept, a)
	}
	return kept, skipped, estimatedTokens
}

// EstimateTokens approximates the token count of s using a fixed
// byte-to-token ratio. Conservative (over-estimates) for code-heavy
// English — see charsPerToken.
//
// We don't pull in a real tokeniser (tiktoken, sentencepiece): they
// require SDK dependencies we deliberately avoid (vendor lock-in,
// binary bloat) and the preflight only needs an order-of-magnitude
// answer to decide "fit or skip". Off-by-20% is fine.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(float64(len(s)) / charsPerToken)
}

// FormatSkipReason returns the user-facing one-liner explaining why
// an agent was preflight-skipped, with the same actionable-hint
// shape as ClassifyExit's failure messages: numbers + concrete fix.
func FormatSkipReason(s SkippedAgent) string {
	return fmt.Sprintf(
		"diff is ~%s tokens, exceeds %s's %s context window — try a smaller diff: `local-review commit HEAD` (last commit) or `local-review staged` (staged only)",
		humanInt(s.EstimatedTokens),
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
