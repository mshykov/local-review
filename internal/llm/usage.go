package llm

// Usage is the prompt/completion token count an OpenAI-compatible
// `/chat/completions` response carries in its `usage` object. Same
// names every major provider uses (OpenAI, Ollama, vLLM, Together,
// OpenRouter, Anthropic's compat endpoint, ...), so one parser shape
// covers them all.
//
// This is the raw HTTP-layer shape. Higher-level code (the provider
// invoker in internal/agents/provider) maps it to agents.TokenUsage,
// keeping llm a low-level client without a dependency on the agents
// abstraction.
//
// Zero values mean the provider didn't return a `usage` object
// (rare for OpenAI-compatible APIs but seen on some older / partial
// implementations). Callers should treat that as "unknown."
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
