package cli

// TokenUsage captures how many tokens a single LLM call consumed.
// Populated by each invoker from the vendor CLI's structured output
// (claude/gemini JSON mode) or stdout metadata (codex). Zero values
// mean "we couldn't determine usage from this call" — either the CLI
// version doesn't support a structured-output flag, or its output
// shape didn't match what we expected. Callers should treat
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
// summaries that show overall consumption across all agents.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens
}
