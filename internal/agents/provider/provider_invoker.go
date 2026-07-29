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
	"errors"
	"fmt"
	"io"
	"os"
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
	sent := estimateTokens(msgs)
	warnIfSlowLocalRun(p.Name, sent, p.client.IsLocalEndpoint())

	text, u, err := p.client.Complete(ctx, msgs, false)
	if err != nil {
		return "", agents.TokenUsage{}, fmt.Errorf("%s: %s", p.Name, describeProviderErr(ctx, err, p.Name, sent, p.client.IsLocalEndpoint()))
	}
	warnIfTruncated(p.Name, sent, u.PromptTokens)
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

// warnOut is where provider diagnostics are written. A package var so
// tests can capture them without touching the process's real stderr.
var warnOut io.Writer = os.Stderr

// Thresholds for the diagnostics below. All deliberately conservative:
// a missed warning is a nuisance, a false alarm on every run trains
// users to ignore the channel entirely.
const (
	// truncationFloorTokens is the smallest prompt worth checking for
	// silent truncation. Below this, the ~4-chars-per-token estimate is
	// too coarse to distinguish truncation from estimation error.
	truncationFloorTokens = 2000

	// slowLocalPromptTokens is where a local model's generation speed
	// (~10 tok/s for a 7B on Apple silicon) starts to mean double-digit
	// minutes. Cloud endpoints are exempt — they're fast enough that a
	// large prompt is unremarkable.
	slowLocalPromptTokens = 8000
)

// estimateTokens approximates a prompt's token count with the common
// ~4-characters-per-token heuristic. Deliberately rough: it only needs
// to be good enough to spot an ORDER-OF-MAGNITUDE gap between what we
// sent and what the provider says it processed. Never used for billing
// or budgeting, so the 20-40% error typical on code is irrelevant here.
func estimateTokens(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n / 4
}

// warnIfTruncated compares the provider's OWN reported prompt_tokens
// against what we estimate we sent, and warns when the provider
// processed dramatically less.
//
// Why this matters more than a normal error: llama.cpp-backed servers
// (Ollama) silently DROP prompt overflow past the context window and
// still return HTTP 200. A 2026-07 dogfood run sent a 15,147-token
// diff to a 4096-context model, which processed 2,050 tokens and
// returned "No issues found" — a clean-looking APPROVE on 14% of the
// change. A reviewer that fails silent is worse than one that fails
// loud, so this converts the silent case into a visible one.
//
// Requires a 2x gap before warning: the estimate is coarse, and
// providers legitimately differ from it (tokenizer, chat-template
// overhead). Real truncation shows up as 5-10x.
func warnIfTruncated(name string, sent, reported int) {
	if reported <= 0 || sent < truncationFloorTokens || reported >= sent/2 {
		return
	}
	// ONE write: the orchestrator runs a goroutine per agent, so a
	// multi-call Fprintf would interleave two agents' warnings into an
	// unreadable braid.
	emitWarning(fmt.Sprintf(
		"WARNING: %s processed only ~%s of the ~%s prompt tokens sent — the input was very likely TRUNCATED to fit the model's context window.\n"+
			"         This review saw a fraction of your diff; treat any \"no issues found\" as unverified. Raise the endpoint's context length\n"+
			"         (e.g. OLLAMA_CONTEXT_LENGTH=32768 ollama serve), review a smaller change (`local-review commit <rev>`), or use a cloud agent.\n",
		name, humanTokens(reported), humanTokens(sent)))
}

// warnIfSlowLocalRun flags a large prompt heading to a local endpoint
// BEFORE the request goes out, so the user can cancel instead of
// discovering the cost after a 20-minute wait (2026-07 dogfood: a
// 17.7k-token diff against a local 7B took 20m42s, and an earlier
// attempt burned the full 600s timeout producing nothing).
func warnIfSlowLocalRun(name string, sent int, local bool) {
	if !local || sent < slowLocalPromptTokens {
		return
	}
	emitWarning(fmt.Sprintf(
		"NOTE: sending ~%s prompt tokens to local endpoint %q — local models generate slowly (a 7B on Apple silicon is ~10 tok/s),\n"+
			"      so this can take many minutes and may hit llms.%s.timeout_seconds. For a faster pass use a cloud agent (`--only claude`)\n"+
			"      or review a smaller change (`local-review commit <rev>`).\n",
		humanTokens(sent), name, name))
}

// emitWarning writes a fully-composed diagnostic in a single call.
// Provider agents run concurrently (one goroutine per agent in
// internal/multi), so composing first and writing once keeps each
// warning contiguous instead of interleaved with another agent's.
func emitWarning(msg string) {
	fmt.Fprint(warnOut, msg)
}

// describeProviderErr turns a failed provider call into an actionable
// message, mirroring what cli.ClassifyExit already does for CLI agents
// (which had the hint and providers didn't — a deadline surfaced as a
// bare "context deadline exceeded" with no guidance, 2026-07 dogfood).
func describeProviderErr(ctx context.Context, err error, name string, sent int, local bool) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		msg := fmt.Sprintf("timeout — raise llms.%s.timeout_seconds, or review a smaller change (`local-review commit <rev>`)", name)
		if local {
			msg += fmt.Sprintf("; a ~%s-token prompt against a local model is often slower than the timeout allows, so a cloud agent (`--only claude`) is the quicker path", humanTokens(sent))
		}
		return msg
	}
	return err.Error()
}

// humanTokens renders a token count as "17.4k" / "950" for messages.
func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000.0)
	}
	return fmt.Sprintf("%d", n)
}
