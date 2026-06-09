// Package provider implements the HTTP/OpenAI-compatible review-agent
// invoker. provider.Invoker is the second concrete implementation of
// agents.Invoker (the first being the CLI-subprocess invokers in
// internal/cli) — every Ollama / vLLM / OpenAI / Together / Groq /
// Anthropic-compat endpoint runs through this one type. (The type is
// just `Invoker` in this package — Go style discourages `Provider`-
// prefixed names like ProviderInvoker because callers already spell
// `provider.Invoker`.)
//
// Layering:
//
//	internal/agents         <- the Invoker contract + TokenUsage
//	internal/llm            <- low-level HTTP client (Client.Complete)
//	internal/agents/provider (this package) <- glue: agents.Invoker over llm.Client
//
// No dependency the other way — llm doesn't know about agents, and
// agents doesn't know about HTTP. Provider sits on top.
//
// Why a separate package (not internal/llm/invoker.go): keeps llm a
// minimal raw-HTTP client (rule: no SDKs, no high-level review
// concepts in there) and gives every Invoker implementation its own
// home — the symmetry with internal/cli (CLI invokers) is intentional.
package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mshykov/local-review/internal/agents"
	"github.com/mshykov/local-review/internal/llm"
)

// Invoker is the HTTP-provider implementation of agents.Invoker.
// One Invoker per configured provider entry (e.g. one for a local
// Ollama qwen, one for a remote vLLM deepseek). Each carries its own
// base_url + model + (optional) api key — the same shape every
// OpenAI-compatible provider takes.
//
// Stateless beyond the client; safe to construct per call or per
// run. The underlying llm.Client owns the HTTP timeout.
type Invoker struct {
	// Name identifies the agent in the roster + reviews-on-disk
	// (".local-review/reviews/<branch>/<commit>_<llm>_<version>.md" —
	// for providers, "<version>" is filled with the model id). Free-form
	// per the v0.14 design — user chooses ("qwen", "local-fast",
	// "air-gapped").
	Name string

	// Model is the provider's model id (e.g. "qwen2.5-coder:7b",
	// "gpt-4o-mini"). Sent verbatim to the provider's
	// /v1/chat/completions; the provider rejects unknown ids.
	Model string

	// client is the underlying HTTP client. Owns base_url, api key,
	// timeout. Constructed by New().
	client *llm.Client
}

// New builds an Invoker for one configured provider entry. The
// per-call timeout is owned by the underlying llm.Client; pass 0 for
// the package default. apiKey may be empty when base_url points at a
// local-or-LAN host (isLocalURL bypass in internal/llm) — Ollama
// doesn't authenticate, and forcing a dummy key is exactly the
// friction the bypass was added to remove.
func New(name, baseURL, apiKey, apiKeyEnv, model string, timeoutSec int) *Invoker {
	return &Invoker{
		Name:   name,
		Model:  model,
		client: llm.New(baseURL, apiKey, apiKeyEnv, model, timeoutSec),
	}
}

// Review wraps a system prompt + diff into a chat-completions call.
// The production review path asks for markdown (buildReviewPrompt
// appends a "respond in markdown, NOT JSON" override); a structured-JSON
// mode is reserved for the future (Resolve appends the findings schema
// only when ResolveOptions.RequireJSON is set, which no production
// caller does today). This invoker is format-agnostic; it just shuttles
// bytes.
func (p *Invoker) Review(ctx context.Context, systemPrompt, diff string) (string, agents.TokenUsage, error) {
	msgs := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: diff},
	}
	return p.complete(ctx, msgs)
}

// RunPrompt sends a raw prompt without the review wrapper. Used by
// the merger (concatenating per-LLM reviews into a merge prompt) and
// audit (which builds its own per-chunk prompts).
func (p *Invoker) RunPrompt(ctx context.Context, prompt string) (string, agents.TokenUsage, error) {
	return p.complete(ctx, []llm.Message{{Role: "user", Content: prompt}})
}

// complete is the shared driver. Returns trimmed text + usage mapped
// from llm.Usage to agents.TokenUsage (so the format the rest of the
// codebase already speaks comes out of this invoker unchanged).
//
// jsonMode is hardcoded to FALSE: the system prompt drives output
// format, not the HTTP response_format header. Setting json_object
// here would contradict the multi-LLM markdown override and produce a
// stray JSON reply the merger can't read — same lesson as v0.13.0's
// "RequireJSON belongs in the pack, not on the wire." If a future
// caller genuinely needs response_format, surface it via a dedicated
// path (with its own test) rather than re-introducing a dead-code
// parameter (flagged by the 3-LLM self-review on PR 1).
//
// Failures wrap the underlying error with the agent name so the
// roster line (`qwen ✗ <reason>`) attributes correctly when multiple
// providers run in parallel.
func (p *Invoker) complete(ctx context.Context, msgs []llm.Message) (string, agents.TokenUsage, error) {
	text, u, err := p.client.Complete(ctx, msgs, false)
	if err != nil {
		return "", agents.TokenUsage{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	usage := agents.TokenUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
	// Some providers (older Ollama builds, partial OpenAI-compat
	// implementations) return only `total_tokens` and leave the split
	// fields zero. Folding the total into InputTokens with TotalOnly
	// matches the codex pre-v0.128 pattern (see agents.TokenUsage doc)
	// so the user still sees a count instead of a misleading "unknown."
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && u.TotalTokens > 0 {
		usage.InputTokens = u.TotalTokens
		usage.TotalOnly = true
	}
	return strings.TrimSpace(text), usage, nil
}

// Compile-time confirmation that Invoker actually satisfies the
// abstract contract — a mismatch fails the build instead of a
// runtime "interface not implemented" deep inside the runner.
var _ agents.Invoker = (*Invoker)(nil)
