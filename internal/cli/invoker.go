package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Invoker runs an LLM CLI with a diff and returns the review output.
type Invoker interface {
	Review(ctx context.Context, diff string) (string, error)
	// RunPrompt sends a raw prompt to the LLM without wrapping it in a code-review context
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

// NewInvoker creates an invoker for the given LLM.
// Returns nil if the LLM name is unknown.
func NewInvoker(llm LLM) Invoker {
	switch llm.Name {
	case "claude":
		return &ClaudeInvoker{path: llm.Path}
	case "gemini":
		return &GeminiInvoker{path: llm.Path}
	case "codex":
		return &CodexInvoker{path: llm.Path}
	default:
		return nil
	}
}

// CodexInvoker runs the OpenAI Codex CLI.
// Note: Actual CLI invocation pattern needs verification - current implementation
// may not work correctly. Disabled by default pending confirmation.
type CodexInvoker struct {
	path string
}

func (c *CodexInvoker) Review(ctx context.Context, diff string) (string, error) {
	prompt := "You are a code reviewer. Review the following git diff for:\n" +
		"1. Bugs and logical errors\n" +
		"2. Security vulnerabilities\n" +
		"3. Performance issues\n" +
		"4. Best practices violations\n\n" +
		"Provide specific findings with file names and line numbers.\n" +
		"Format as markdown with severity levels (critical, major, warning, info).\n\n" +
		"Diff:\n" + diff

	cmd := exec.CommandContext(ctx, c.path)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("codex review failed: %w", err)
	}

	return string(output), nil
}

func (c *CodexInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, c.path)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("codex failed: %w", err)
	}
	return string(output), nil
}

// GeminiInvoker runs the Google Gemini CLI.
// Uses: git diff | gemini -p "Review these changes for bugs and security issues"
type GeminiInvoker struct {
	path string
}

func (g *GeminiInvoker) Review(ctx context.Context, diff string) (string, error) {
	prompt := "Review these code changes for bugs, security issues, and best practices. " +
		"Provide specific findings with file names and line numbers. " +
		"Format as markdown with severity levels (critical, major, warning, info)."

	cmd := exec.CommandContext(ctx, g.path, "-p", prompt)
	cmd.Stdin = strings.NewReader(diff)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gemini review failed: %w", err)
	}

	return string(output), nil
}

func (g *GeminiInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
	// Pass prompt via -p flag (Gemini CLI doesn't read prompts from stdin)
	// Note: Very large prompts may hit ARG_MAX limits (~2MB on Linux, ~256KB on macOS)
	// If this becomes an issue, we'd need to investigate alternative invocation patterns
	cmd := exec.CommandContext(ctx, g.path, "-p", prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gemini failed: %w", err)
	}
	return string(output), nil
}

// ClaudeInvoker runs the Anthropic Claude CLI.
// Uses stdin pipe similar to Gemini.
type ClaudeInvoker struct {
	path string
}

func (c *ClaudeInvoker) Review(ctx context.Context, diff string) (string, error) {
	prompt := "You are a code reviewer. Review the following git diff for:\n" +
		"1. Bugs and logical errors\n" +
		"2. Security vulnerabilities\n" +
		"3. Performance issues\n" +
		"4. Best practices violations\n\n" +
		"Provide specific findings with file names and line numbers.\n" +
		"Format as markdown with severity levels (critical, major, warning, info).\n\n" +
		"Diff:\n" + diff

	// Use stdin as primary method
	cmd := exec.CommandContext(ctx, c.path)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude review failed: %w", err)
	}

	return string(output), nil
}

func (c *ClaudeInvoker) RunPrompt(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, c.path)
	cmd.Stdin = strings.NewReader(prompt)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude failed: %w", err)
	}
	return string(output), nil
}
