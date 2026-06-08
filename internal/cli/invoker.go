package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mshykov/local-review/internal/agents"
	"github.com/mshykov/local-review/internal/agents/provider"
)

// Invoker is the review-agent contract. Moved to internal/agents in
// v0.14 so the HTTP provider invoker (internal/agents/provider) could
// share it without depending on this CLI-subprocess package. CLI
// callers continue to use cli.Invoker unchanged via this alias.
//
// See agents.Invoker for the full contract; the CLI subprocess
// invokers below all satisfy it (they shell out to the vendor CLI,
// parse its structured output for tokens, and return markdown or
// JSON depending on what the resolved system prompt asked for).
type Invoker = agents.Invoker

// multiLLMOutputOverride tells the agent to respond in markdown
// instead of JSON. The prompt packs mandate JSON output for the
// single-LLM path (which parses structured findings); multi-LLM
// agents need to emit markdown so the merger can consolidate prose
// across reviewers. We append this AFTER the pack so the LLM's most
// recent instruction wins.
const multiLLMOutputOverride = `

---
**Output format for this review**: respond in human-readable markdown
with severity headings (## Critical Issues, ## Major Issues, ## Warnings,
## Info / Notes). Each finding: file path + line number, short title,
brief explanation, suggested fix. Do NOT return JSON — a separate
merger step will consolidate findings across reviewers.
`

// buildReviewPrompt assembles the per-agent review prompt from the
// caller-supplied systemPrompt (a language-specific prompt pack from
// internal/prompts) and the multi-LLM markdown-output override.
//
// An empty systemPrompt falls back to a generic 4-bullet review prompt
// so the agent still does *something* useful — defends against tests
// or callers that haven't been updated to pass the pack content. The
// generic fallback used to be the *default* in every invoker; since
// v0.6.x the runner threads the pack through, so this is just a safety
// net.
func buildReviewPrompt(systemPrompt string) string {
	if systemPrompt == "" {
		systemPrompt = "You are a code reviewer. Review the diff below for:\n" +
			"1. Bugs and logical errors\n" +
			"2. Security vulnerabilities\n" +
			"3. Performance issues\n" +
			"4. Best practices violations\n\n" +
			"Provide specific findings with file names and line numbers."
	}
	return systemPrompt + multiLLMOutputOverride
}

// NewInvoker creates an invoker for the given LLM. The Model and
// APIKey fields on LLM are threaded into each invoker so per-agent
// --claude-model / --gemini-model / --codex-model flag overrides
// actually reach the CLI command line, and so a key sourced from a
// user-named env var (cfg.LLMs[name].APIKeyEnv) still reaches the
// subprocess under the canonical name the CLI itself expects.
// An empty Model leaves the CLI on its default; an empty APIKey
// means "rely on the CLI's own auth flow / OAuth session."
//
// Returns nil if the LLM name is unknown (for CLI agents) — provider
// agents (BaseURL set) are always recognised, so they never return nil
// here regardless of name.
func NewInvoker(llm LLM) Invoker {
	// Provider agents — discriminated by BaseURL, not by name. Name is
	// free-form for providers (user picks "qwen", "local-fast", …) so a
	// name-switch wouldn't work. The provider invoker is one type that
	// covers every OpenAI-compatible endpoint.
	if llm.BaseURL != "" {
		return provider.New(llm.Name, llm.BaseURL, llm.APIKey, llm.APIKeyEnv, llm.Model, llm.TimeoutSec)
	}
	switch llm.Name {
	case "claude":
		return &ClaudeInvoker{path: llm.Path, model: llm.Model, apiKey: llm.APIKey}
	case "gemini":
		return &GeminiInvoker{path: llm.Path, model: llm.Model, apiKey: llm.APIKey}
	case "codex":
		return &CodexInvoker{path: llm.Path, model: llm.Model, apiKey: llm.APIKey}
	case "antigravity":
		return &AntigravityInvoker{path: llm.Path, model: llm.Model, apiKey: llm.APIKey}
	case "copilot":
		return &CopilotInvoker{path: llm.Path, model: llm.Model, apiKey: llm.APIKey}
	default:
		return nil
	}
}

// withInjectedKey returns os.Environ() augmented (or overridden) with
// canonicalEnv=apiKey when apiKey is non-empty. This lets a user keep
// the key under any env-var name they like in their shell — the CLI
// always sees the canonical name it knows how to read.
//
// Pre-existing env vars set by the parent shell still pass through;
// our line wins because Go's exec.Cmd uses last-occurrence semantics
// when the same name appears multiple times.
func withInjectedKey(canonicalEnv, apiKey string) []string {
	env := os.Environ()
	if apiKey == "" {
		return env
	}
	return append(env, canonicalEnv+"="+apiKey)
}

// CodexInvoker runs the OpenAI Codex CLI.
//
// Bare `codex` (no subcommand) opens an interactive TUI — that's what the
// pre-v0.5.1 invoker was doing, which is why every codex review failed
// with `exit status 1`. We use `codex exec` (non-interactive), pipe the
// prompt over stdin, and have codex write only the final assistant
// message to a temp file via --output-last-message. That sidesteps both
// the interactive-TUI failure AND the noisy "session id / tokens used"
// preamble that codex exec normally prints to stdout.
//
// We deliberately don't use `codex review` (the dedicated review
// subcommand) because it re-extracts the diff itself from the local
// git tree, conflicting with the orchestrator's "extract once, fan out
// to all LLMs with the same diff string" contract.
type CodexInvoker struct {
	path   string
	model  string // codex exec -m <model>; empty = CLI default
	apiKey string // injected as OPENAI_API_KEY into subprocess env

	// Embedded partial-stderr capture (v0.10.6). Provides the
	// PartialStderr() method via Go struct-method promotion;
	// run() stores a fresh stderrCapture into the embedded
	// `capture` field on every invocation. See
	// internal/cli/stderr_capture.go partialStderrField for the
	// shared implementation.
	partialStderrField
}

func (c *CodexInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, TokenUsage, error) {
	prompt := buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff
	return c.runExec(ctx, prompt, "codex review")
}

func (c *CodexInvoker) RunPrompt(ctx context.Context, prompt string) (string, TokenUsage, error) {
	return c.runExec(ctx, prompt, "codex")
}

// runExec is the shared `codex exec --output-last-message` driver for
// Review and RunPrompt. errLabel customises the error prefix so callers
// can tell "review failed" from "merge failed" in logs.
//
// Why a temp file: `codex exec` prints session metadata
// ("session id: ...", "tokens used", banner output) intermixed with
// the assistant's reply on stdout. There's no flag for "raw last
// message to stdout"; --output-last-message is the only documented
// non-prose path and writes to a file. Parsing the prose stdout is
// fragile (codex's banner format has changed across minor versions),
// so we accept the disk I/O — one temp file per review, deleted via
// defer — as the price of a stable contract. If codex ever ships a
// stdout-only flag, drop the file.
//
// Token usage: codex exec doesn't have a JSON-output flag (verified
// against `codex exec --help` on v0.128.0). We parse the same stdout
// metadata block we already capture (for stderr tail on errors) for
// the "tokens used" line. parseCodexStdoutTokens returns TokenUsage{}
// when the line isn't found, so a future codex version that drops
// the line silently degrades to "no token info" rather than failing
// the whole review.
func (c *CodexInvoker) runExec(ctx context.Context, prompt, errLabel string) (string, TokenUsage, error) {
	tmp, err := os.CreateTemp("", "codex-out-*.txt")
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("%s: create temp output file: %w", errLabel, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	args := []string{"exec", "--output-last-message", tmpPath}
	if c.model != "" {
		args = append(args, "-m", c.model)
	}
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = withInjectedKey(CanonicalAPIKeyEnv["codex"], c.apiKey)
	// Switched from CombinedOutput() to separate stdout/stderr
	// streams (v0.10.6) so we can tee stderr through a partial
	// buffer for the probe layer. We reconstruct the same
	// combined-bytes contract the existing code downstream
	// expects (token parsing + ClassifyExit input) by
	// concatenating manually after Run.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = c.teeStderr(&stderrBuf)
	runErr := cmd.Run()
	combined := append(stdoutBuf.Bytes(), stderrBuf.Bytes()...)
	if runErr != nil {
		// ClassifyExit produces the user-facing summary with an
		// actionable hint (smaller diff for SIGKILL, raise timeout for
		// deadline, surface stderr tail for non-zero exit). The
		// errLabel arg is unused here now — the caller's per-LLM line
		// already prefixes the agent name, so prefixing it again would
		// just produce "codex ✗ codex review failed: ..." duplication.
		_ = errLabel
		return "", TokenUsage{}, fmt.Errorf("%s", ClassifyExit(ctx, runErr, combined, "codex"))
	}

	out, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("%s: read codex output: %w", errLabel, err)
	}
	// Pass the response text so the parser can strip the trailing
	// duplicate codex writes at end-of-stdout. Without this, pattern-
	// shaped text in the reply (e.g. quoted test fixtures) bypasses
	// our latest-position logic by appearing AFTER the real summary.
	usage := parseCodexStdoutTokens(string(combined), string(out))
	return string(out), usage, nil
}

// GeminiInvoker runs the Google Gemini CLI.
// Uses: git diff | gemini -p "Review these changes for bugs and security issues"
type GeminiInvoker struct {
	path   string
	model  string // gemini -m <model>; empty = CLI default
	apiKey string // injected as GEMINI_API_KEY into subprocess env

	// Embedded partial-stderr capture (v0.10.6). Gemini is the
	// CLI this feature primarily targets — its "You have
	// exhausted your capacity on this model." message lands in
	// stderr long before the network call finally times out. See
	// partialStderrField in stderr_capture.go.
	partialStderrField
}

func (g *GeminiInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, TokenUsage, error) {
	return g.run(ctx, buildReviewPrompt(systemPrompt)+"\n\n# Diff\n\n"+diff)
}

func (g *GeminiInvoker) RunPrompt(ctx context.Context, prompt string) (string, TokenUsage, error) {
	return g.run(ctx, prompt)
}

// run is the shared driver for Review and RunPrompt.
//
// gemini's --help: "-p, --prompt: Run in non-interactive mode with
// the given prompt. Appended to input on stdin (if any)." A tiny
// marker via -p activates headless mode; the real prompt body goes
// via stdin — sidestepping ARG_MAX (~256KB on macOS, ~2MB on Linux)
// the "whole prompt via -p" implementation hit on merger prompts.
//
// `-o json` requests structured output. parseGeminiJSON handles two
// shapes seen in the wild — the modern stats.models.<id>.tokens
// structure and the older Vertex-style usageMetadata. Returns
// TokenUsage{} when neither shape parses (e.g., a future schema
// drift) so the review still ships even if we lose token info for
// that run.
//
// Minimum gemini CLI version: v0.40 (the version where `-o json`
// stabilised). Older CLIs without the flag exit with an unknown-
// argument error and ClassifyExit surfaces the stderr — *not* a
// graceful plain-text fall-through. The v0.6.6 CLI-version baseline
// is documented in CHANGELOG and fails-fast rather than producing
// token-less reviews.
//
// Stdout and stderr are captured separately so JSON parsing only
// sees the structured response. Pre-fix this used CombinedOutput
// and any stderr noise (deprecation banners, "new version available"
// nags, Node-version warnings) interleaved into the JSON, breaking
// json.Unmarshal and silently dropping tokens via the raw-text
// fallback. On error we concatenate stdout+stderr for ClassifyExit
// so the user-facing message still includes the stderr tail.
func (g *GeminiInvoker) run(ctx context.Context, prompt string) (string, TokenUsage, error) {
	args := []string{"-p", "Follow the instructions in stdin.", "-o", "json"}
	if g.model != "" {
		args = append(args, "-m", g.model)
	}
	cmd := exec.CommandContext(ctx, g.path, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = withInjectedKey(CanonicalAPIKeyEnv["gemini"], g.apiKey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	// Tee stderr through a live partial buffer (see ClaudeInvoker
	// for the rationale). The MultiWriter ensures the existing
	// post-run `stderr.Bytes()` path is unaffected.
	cmd.Stderr = g.teeStderr(&stderr)
	if err := cmd.Run(); err != nil {
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		return "", TokenUsage{}, fmt.Errorf("%s", ClassifyExit(ctx, err, combined, "gemini"))
	}
	text, usage := parseGeminiJSON(stdout.Bytes())
	return text, usage, nil
}

// ClaudeInvoker runs the Anthropic Claude CLI.
// Uses stdin pipe similar to Gemini.
type ClaudeInvoker struct {
	path   string
	model  string // claude --model <id>; empty = CLI default
	apiKey string // injected as ANTHROPIC_API_KEY into subprocess env

	// Embedded partial-stderr capture (v0.10.6). The probe layer
	// peeks the captured bytes via the auto-promoted
	// PartialStderr() method when ctx expires, surfacing the
	// vendor's diagnostic even while the subprocess is hung past
	// SIGKILL on pipe drain. See partialStderrField in
	// stderr_capture.go.
	partialStderrField
}

func (c *ClaudeInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, TokenUsage, error) {
	prompt := buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff
	return c.run(ctx, prompt)
}

func (c *ClaudeInvoker) RunPrompt(ctx context.Context, prompt string) (string, TokenUsage, error) {
	return c.run(ctx, prompt)
}

// run is the shared driver. Splits args into "model + stdin prompt" so
// per-agent --claude-model overrides reach the CLI.
//
// Uses `--print --output-format json` so the response is a single
// JSON object containing both the assistant's reply and a usage
// block we can extract token counts from.
//
// Minimum claude CLI version: any release supporting
// `--output-format json` (well-established since claude-code shipped
// non-interactive mode). If the user runs a CLI old enough not to
// recognise the flag, the subprocess exits with an "unknown flag"
// error and ClassifyExit surfaces the stderr — *not* a graceful
// fall-through to plain text. Older CLIs are unsupported by design;
// the documented v0.6.6 CLI-version baseline fails-fast rather than
// silently producing token-less reviews.
//
// Stdout and stderr are captured separately so JSON parsing only
// sees the structured response. Pre-fix this used CombinedOutput
// and any stderr noise (Anthropic auth-refresh notices, npm-install
// "new version available" banners) interleaved into the JSON,
// breaking json.Unmarshal and silently dropping tokens via the raw-
// text fallback. On error we concatenate stdout+stderr for
// ClassifyExit so the user-facing message still includes the
// stderr tail.
func (c *ClaudeInvoker) run(ctx context.Context, prompt string) (string, TokenUsage, error) {
	args := []string{"--print", "--output-format", "json"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = withInjectedKey(CanonicalAPIKeyEnv["claude"], c.apiKey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	// Tee stderr through a live partial buffer so the probe layer
	// can peek mid-flight (v0.10.6). teeStderr installs a fresh
	// capture and returns a MultiWriter to the existing stderr
	// destination — additive, no change to post-run handling.
	cmd.Stderr = c.teeStderr(&stderr)
	if err := cmd.Run(); err != nil {
		// No error-label prefix here: the runner's per-LLM completion
		// line already names the agent ("claude ✗ ..."), so a second
		// "claude review failed:" prefix on the message would
		// duplicate. ClassifyExit's output is the user-facing summary;
		// no caller-side framing needed.
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		return "", TokenUsage{}, fmt.Errorf("%s", ClassifyExit(ctx, err, combined, "claude"))
	}
	text, usage := parseClaudeJSON(stdout.Bytes())
	return text, usage, nil
}

// AntigravityInvoker runs Google's Antigravity CLI (`agy`), the
// successor to the Gemini CLI. Google announced (2026-05) that the
// Gemini CLI stops serving Pro/Ultra/free-tier requests on
// 2026-06-18, with Antigravity as the replacement.
//
// NOT WIRED INTO THE REVIEW FAN-OUT (cli.IsReviewCapable("antigravity")
// == false). The 2026-05 authenticated dogfood found agy's `--print`
// mode runs a full autonomous agent loop: it explores the repo, runs
// git/python, reconstructs its OWN diff (ignoring the one we hand it),
// and streams tool-step narration ("I will run git diff…") to stdout
// rather than a clean review. A real `local-review review --only
// antigravity` produced 6.5 KB of narration, zero findings, and an
// empty merged report. So agy is detected + surfaced in `doctor`
// (statusExperimental) but excluded from the active set. This invoker
// is retained as the starting point for a future structured-output
// integration; until then it's only reachable via NewInvoker for the
// type/interface tests, never from a real review run.
//
// Three ways agy differs from the other invokers, all confirmed by
// probing `agy --help` / `agy --version` on v1.0.2:
//
//  1. The prompt is a POSITIONAL ARG to `-p`, not stdin. agy's
//     `-p` flag "needs an argument" — piping a prompt to it errors.
//     So unlike claude/gemini/codex (all stdin-fed), agy gets the
//     whole prompt+diff on argv. macOS ARG_MAX is 1 MiB; run() guards
//     with an explicit 256 KiB ceiling so an oversized prompt fails
//     with a clear message instead of a cryptic "argument list too
//     long" from exec. (In real review runs PreflightFilter would cap
//     prompt size first, but agy is excluded from the fan-out, so that
//     backstop never runs here — hence the in-invoker guard.)
//
//  2. No structured-output flag. agy has no `-o json` /
//     `--output-format` (verified against `agy --help` v1.0.2), so
//     there's nothing to parse token usage from — RunPrompt returns
//     TokenUsage{}. Display callers already handle the zero case
//     ("we couldn't determine usage"); agy reviews render without
//     a token count, same as codex pre-v0.128.
//
//  3. `--dangerously-skip-permissions` is passed so the headless
//     call doesn't hang on a tool-permission prompt. Our prompt is
//     self-contained (the diff is in the prompt text; agy doesn't
//     need filesystem tools to review it), but agy is agentic and
//     may still try to read files / run tools unless told to
//     auto-approve. Without the flag a `-p` run can block forever
//     waiting on a permission prompt that has no TTY to answer it.
//
// Auth is OAuth (Google login via `agy`), like claude — there's no
// API-key env var, so CanonicalAPIKeyEnv has no "antigravity"
// entry and apiKey is unused here. An unauthenticated agy surfaces
// "Authentication required. Please visit the URL..." which the
// pre-flight probe captures and renders as the readiness-block
// reason; no special-casing needed.
type AntigravityInvoker struct {
	path   string
	model  string // agy currently exposes no --model flag; reserved for when it does
	apiKey string // unused (OAuth auth); kept for NewInvoker symmetry

	// Embedded partial-stderr capture (v0.10.6) — same as the
	// other invokers; lets the probe surface agy's own diagnostic
	// (e.g. the OAuth "Authentication required" message) on timeout.
	partialStderrField
}

func (a *AntigravityInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, TokenUsage, error) {
	prompt := buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff
	return a.run(ctx, prompt)
}

func (a *AntigravityInvoker) RunPrompt(ctx context.Context, prompt string) (string, TokenUsage, error) {
	return a.run(ctx, prompt)
}

// run drives `agy -p <prompt> --dangerously-skip-permissions`.
// The prompt rides on argv (agy doesn't read stdin — see the type
// doc). No JSON output to parse, so the assistant's reply is the
// raw trimmed stdout and usage is always zero.
//
// NOTE: this code path is NOT used by real reviews (see the type
// doc — agy is excluded from the fan-out). The 2026-05 dogfood
// showed `-p` does NOT print a clean response: agy runs an agentic
// loop and emits step-narration, and on short prompts can return
// empty stdout entirely. A future integration will need a different
// invocation contract (structured output and/or suppressing the
// agent loop) before this can drive a review. Kept as scaffolding.
func (a *AntigravityInvoker) run(ctx context.Context, prompt string) (string, TokenUsage, error) {
	// Explicit argv ceiling. The prompt rides on argv (agy doesn't read
	// stdin), so a pathologically large prompt would hit the OS ARG_MAX
	// (1 MiB on macOS) and surface as a cryptic "argument list too long"
	// from exec. In real review runs PreflightFilter caps prompt size at
	// the model's context window long before this — but agy is excluded
	// from the fan-out, so that backstop never runs here; this guard
	// makes the scaffolding fail with a clear message regardless of how
	// it's driven. The 256 KiB ceiling leaves generous headroom below
	// ARG_MAX (env + other argv share the budget).
	const maxPromptBytes = 256 << 10
	if len(prompt) > maxPromptBytes {
		return "", TokenUsage{}, fmt.Errorf("antigravity prompt too large: %d bytes (max %d)", len(prompt), maxPromptBytes)
	}
	args := []string{"-p", prompt, "--dangerously-skip-permissions"}
	cmd := exec.CommandContext(ctx, a.path, args...)
	// No stdin: agy reads the prompt from argv. Leave cmd.Stdin nil.
	cmd.Env = os.Environ() // OAuth session lives in agy's own config dir; no key to inject
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = a.teeStderr(&stderr)
	if err := cmd.Run(); err != nil {
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		return "", TokenUsage{}, fmt.Errorf("%s", ClassifyExit(ctx, err, combined, "antigravity"))
	}
	// No structured output → return trimmed stdout, zero usage.
	return strings.TrimSpace(stdout.String()), TokenUsage{}, nil
}

// CopilotInvoker runs the GitHub Copilot CLI (`copilot`), GitHub's
// agentic coding assistant, in non-interactive mode as a review agent.
//
// Unlike Antigravity — the other agentic CLI we evaluated, gated out
// because its `--print` emits agent narration instead of a review —
// Copilot's `-p` mode returns a clean answer: the review prose goes to
// STDOUT while agent/usage telemetry (MCP status, "Requests N Premium",
// the token summary) goes to STDERR. The 2026-05 dogfood confirmed it
// reviews the diff it's handed (`Changes +0 -0` — it does NOT
// reconstruct its own) and catches planted bugs, so it joins the
// active fan-out as a first-class agent.
//
// Invocation specifics (verified against `copilot --help` v1.0.54):
//   - `-p, --prompt <text>` runs one prompt non-interactively. The
//     prompt is a POSITIONAL value to the flag (not stdin), so it rides
//     on argv — run() guards against ARG_MAX, with PreflightFilter as
//     the upstream cap.
//   - `--available-tools=` (empty whitelist) disables ALL tools. This
//     is the security boundary: the diff in the prompt is untrusted, so
//     we must NOT let a prompt-injecting diff drive Copilot's
//     shell/write/url tools. With no tools available there's nothing to
//     approve, so non-interactive mode needs no `--allow-all-tools` and
//     can't hang on a permission prompt. See run() for the full note.
//   - `--no-ask-user` stops the agent from blocking on a clarifying
//     question in non-interactive mode.
//   - `--no-color` strips ANSI so the captured stdout is clean markdown.
//   - `--model <id>` selects the model (e.g. gpt-5.3-codex); empty =
//     CLI default.
//
// Auth: a `copilot login` session stored under ~/.copilot (or
// $COPILOT_HOME), OR a token in COPILOT_GITHUB_TOKEN. We inject the
// resolved apiKey (when configured) as COPILOT_GITHUB_TOKEN. The
// copilot CLI also reads GH_TOKEN / GITHUB_TOKEN, but doctor will NOT
// auto-enable Copilot on those generic tokens (see checkCopilotAuth)
// — they're too common in CI to safely activate a paid reviewer.
//
// Cost: each run consumes a Copilot "Premium request" from the user's
// subscription — it is NOT BYOK-free like a Gemini API key. The token
// summary on stderr is vendor-rounded; parseCopilotStderrTokens reads
// it best-effort and degrades to zero usage if the format shifts.
type CopilotInvoker struct {
	path   string
	model  string // copilot --model <id>; empty = CLI default
	apiKey string // injected as COPILOT_GITHUB_TOKEN into subprocess env

	// Embedded partial-stderr capture (v0.10.6) — lets the probe
	// surface Copilot's own diagnostic on timeout. See
	// partialStderrField in stderr_capture.go.
	partialStderrField
}

func (c *CopilotInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, TokenUsage, error) {
	return c.run(ctx, buildReviewPrompt(systemPrompt)+"\n\n# Diff\n\n"+diff)
}

func (c *CopilotInvoker) RunPrompt(ctx context.Context, prompt string) (string, TokenUsage, error) {
	return c.run(ctx, prompt)
}

// run drives `copilot -p <prompt> --available-tools= --no-ask-user
// --no-color`. stdout is the clean review; stderr carries telemetry we
// parse best-effort for token counts. See the type doc for the flag
// rationale (notably why tools are disabled, not allow-all'd).
func (c *CopilotInvoker) run(ctx context.Context, prompt string) (string, TokenUsage, error) {
	// The prompt rides on argv (`-p <text>`), so guard against the OS
	// ARG_MAX (~1 MiB on macOS) — an oversized prompt should fail with
	// a clear message, not a cryptic "argument list too long". The
	// runner's PreflightFilter is the upstream cap; this is the backstop.
	const maxPromptBytes = 256 << 10
	if len(prompt) > maxPromptBytes {
		return "", TokenUsage{}, fmt.Errorf("copilot prompt too large: %d bytes (max %d)", len(prompt), maxPromptBytes)
	}
	// SECURITY: disable ALL tools (`--available-tools=` with an empty
	// list is Copilot's whitelist-to-nothing). The review prompt embeds
	// the diff, which is attacker-controllable (you review PRs from
	// untrusted contributors) — running with `--allow-all-tools` would
	// let a prompt-injecting diff drive Copilot's shell/write/url tools
	// to mutate the workspace or exfiltrate. A diff review needs no
	// tools (the dogfood confirmed `Changes +0 -0` even with tools
	// allowed), so the safe contract is "no tools available." With none
	// visible there's nothing to approve, so we don't need
	// --allow-all-tools and there's no permission prompt to hang on.
	// --no-ask-user additionally stops the agent from blocking on a
	// clarifying question in non-interactive mode.
	args := []string{"-p", prompt, "--available-tools=", "--no-ask-user", "--no-color"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Env = withInjectedKey(CanonicalAPIKeyEnv["copilot"], c.apiKey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = c.teeStderr(&stderr)
	if err := cmd.Run(); err != nil {
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		return "", TokenUsage{}, fmt.Errorf("%s", ClassifyExit(ctx, err, combined, "copilot"))
	}
	// Clean review on stdout; usage summary on stderr (best-effort).
	return strings.TrimSpace(stdout.String()), parseCopilotStderrTokens(stderr.String()), nil
}
