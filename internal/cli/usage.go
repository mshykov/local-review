package cli

// TokenUsage captures how many tokens a single LLM call consumed.
// Populated by each invoker from the vendor CLI's structured output
// (claude/gemini JSON mode) or stdout metadata (codex).
//
// Zero values mean "we couldn't determine usage from this call" —
// the CLI version may not support a structured-output flag, or its
// output shape didn't match what we expected. Callers should treat
// `usage.IsZero()` as "unknown" rather than "no tokens used."
type TokenUsage struct {
	// InputTokens is what the user paid for to send (prompt + diff +
	// any system message wrapping). Anthropic calls this
	// `input_tokens`; Google calls it `promptTokenCount`; OpenAI
	// calls it `prompt_tokens`. We normalise to the same field name
	// across vendors.
	InputTokens int

	// OutputTokens is what the model generated and the user paid
	// for receiving. Anthropic: `output_tokens`. Google:
	// `candidatesTokenCount`. OpenAI: `completion_tokens`.
	OutputTokens int

	// TotalOnly indicates the source CLI reported a single combined
	// total without an input/output split. Codex's pre-v0.128
	// stdout metadata is the canonical example ("tokens used: N").
	// When set, the total is in InputTokens (we fold it there
	// instead of inventing a 50/50 split) and display callers
	// should render as "Nk total" rather than "Nk in / 0 out" —
	// the latter would mislead users into thinking the model
	// produced no output. Tools that aggregate across calls should
	// still use Total() for the running sum.
	TotalOnly bool
}

// IsZero reports whether neither token count was populated. The
// caller uses this to decide between "show '12.3k in / 4.5k out'"
// and "omit the token suffix entirely" on the per-LLM completion
// line — printing "0 in / 0 out" would mislead users into thinking
// the call was free when actually we just didn't know.
func (u TokenUsage) IsZero() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0
}

// Total is the sum of input + output, useful for closing-line
// summaries that show overall consumption across all agents. For
// TotalOnly usage, InputTokens already holds the full total so the
// math still works — OutputTokens is 0 by definition there.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens
}
