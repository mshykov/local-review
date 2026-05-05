package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Invoker runs an LLM CLI with a diff and returns the review output.
//
// Review takes a `systemPrompt` (the language-specific prompt pack
// content the runner has already loaded via lang.Dominant +
// prompts.Get) so each agent reviews against the same review-rules,
// severity tiering, and hard rules the single-LLM path uses. Empty
// systemPrompt means "fall back to the agent's built-in generic
// prompt" — useful for tests and as a defensive default.
type Invoker interface {
	Review(ctx context.Context, systemPrompt, diff string) (string, error)
	// RunPrompt sends a raw prompt to the LLM without wrapping it in a code-review context
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

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

// NewInvoker creates an invoker for the given LLM. The Model field on
// LLM is threaded into each invoker so per-agent --claude-model /
// --gemini-model / --codex-model flag overrides actually reach the CLI
// command line. An empty Model leaves the CLI on its default.
//
// Returns nil if the LLM name is unknown.
func NewInvoker(llm LLM) Invoker {
	switch llm.Name {
	case "claude":
		return &ClaudeInvoker{path: llm.Path, model: llm.Model}
	case "gemini":
		return &GeminiInvoker{path: llm.Path, model: llm.Model}
	case "codex":
		return &CodexInvoker{path: llm.Path, model: llm.Model}
	default:
		return nil
	}
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
	path  string
	model string // codex exec -m <model>; empty = CLI default
}

func (c *CodexInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, error) {
	prompt := buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff
	return c.runExec(ctx, prompt, "codex review")
}

func (c *CodexInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
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
func (c *CodexInvoker) runExec(ctx context.Context, prompt, errLabel string) (string, error) {
	tmp, err := os.CreateTemp("", "codex-out-*.txt")
	if err != nil {
		return "", fmt.Errorf("%s: create temp output file: %w", errLabel, err)
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
	if combined, err := cmd.CombinedOutput(); err != nil {
		// Surface the CLI's own stderr so users can see "auth required",
		// "rate limited", etc. instead of a bare "exit status 1".
		return "", fmt.Errorf("%s failed: %w (output: %s)", errLabel, err, strings.TrimSpace(string(combined)))
	}

	out, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("%s: read codex output: %w", errLabel, err)
	}
	return string(out), nil
}

// GeminiInvoker runs the Google Gemini CLI.
// Uses: git diff | gemini -p "Review these changes for bugs and security issues"
type GeminiInvoker struct {
	path  string
	model string // gemini -m <model>; empty = CLI default
}

func (g *GeminiInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, error) {
	// gemini's `-p` is appended to stdin in headless mode (per its
	// --help). Put a marker in -p, route the full pack-prompt + diff
	// via stdin to dodge ARG_MAX on long pack content.
	args := []string{"-p", "Follow the instructions in stdin."}
	if g.model != "" {
		args = append(args, "-m", g.model)
	}
	cmd := exec.CommandContext(ctx, g.path, args...)
	cmd.Stdin = strings.NewReader(buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gemini review failed: %w", err)
	}

	return string(output), nil
}

func (g *GeminiInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
	// gemini's --help: "-p, --prompt: Run in non-interactive mode with
	// the given prompt. Appended to input on stdin (if any)." So a tiny
	// marker via -p activates headless mode and the real prompt body
	// goes via stdin — sidestepping ARG_MAX (~256KB on macOS, ~2MB on
	// Linux) that the previous "whole prompt via -p" implementation hit
	// on merger prompts that aggregate multiple per-LLM reviews.
	args := []string{"-p", "Follow the instructions in stdin."}
	if g.model != "" {
		args = append(args, "-m", g.model)
	}
	cmd := exec.CommandContext(ctx, g.path, args...)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gemini failed: %w", err)
	}
	return string(output), nil
}

// ClaudeInvoker runs the Anthropic Claude CLI.
// Uses stdin pipe similar to Gemini.
type ClaudeInvoker struct {
	path  string
	model string // claude --model <id>; empty = CLI default
}

func (c *ClaudeInvoker) Review(ctx context.Context, systemPrompt, diff string) (string, error) {
	prompt := buildReviewPrompt(systemPrompt) + "\n\n# Diff\n\n" + diff
	return c.run(ctx, prompt, "claude review")
}

func (c *ClaudeInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return c.run(ctx, prompt, "claude")
}

// run is the shared driver. Splits args into "model + stdin prompt" so
// per-agent --claude-model overrides reach the CLI.
func (c *ClaudeInvoker) run(ctx context.Context, prompt, errLabel string) (string, error) {
	var args []string
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", errLabel, err)
	}
	return string(output), nil
}
