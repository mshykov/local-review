package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// initCmd scaffolds a `.local-review.yml` interactively. The wizard is
// deliberately small: it produces a minimal v0 single-provider config
// that the user can hand-edit later, not the full schema. We'd rather
// the user finish with a working file than a complete one.
func initCmd() *cobra.Command {
	var force bool
	var location string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactively scaffold a .local-review.yml",
		Long: `Init walks you through a few questions and writes a minimal
.local-review.yml file. Pick a provider, give it your API key env var name,
and you're reviewing.

By default the config goes in the current directory; pass --global to
write ~/.local-review.yml instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := resolveTarget(location)
			if err != nil {
				return err
			}
			return runInit(cmd.OutOrStdout(), os.Stdin, target, force)
		},
	}
	cmd.Flags().StringVar(&location, "location", "local", `where to write: "local" (./.local-review.yml) or "global" (~/.local-review.yml)`)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the file if it already exists")
	return cmd
}

// providerPreset is a copy-paste-ready set of defaults for a known
// provider. The wizard offers these as a numbered list; "Other" lets
// the user enter a custom base_url + model.
type providerPreset struct {
	label       string // shown in the menu
	baseURL     string
	defaultMdl  string
	apiKeyEnv   string
	requiresKey bool   // false for Ollama (no key needed)
	note        string // shown after the user picks it, before model prompt
}

// Order matters — this is the menu order users see.
var providerPresets = []providerPreset{
	{
		label:       "OpenAI",
		baseURL:     "https://api.openai.com/v1",
		defaultMdl:  "gpt-4o-mini",
		apiKeyEnv:   "OPENAI_API_KEY",
		requiresKey: true,
		note:        "Cheap default: gpt-4o-mini. Bump to gpt-4o for harder reviews.",
	},
	{
		label:       "Anthropic (Claude)",
		baseURL:     "https://api.anthropic.com/v1",
		defaultMdl:  "claude-3-5-sonnet-20241022",
		apiKeyEnv:   "ANTHROPIC_API_KEY",
		requiresKey: true,
		note:        "Uses Anthropic's chat-completions-compatible endpoint, not /v1/messages.",
	},
	{
		label:       "Mistral (EU-hosted)",
		baseURL:     "https://api.mistral.ai/v1",
		defaultMdl:  "codestral-latest",
		apiKeyEnv:   "MISTRAL_API_KEY",
		requiresKey: true,
		note:        "Codestral is purpose-built for code. EU-hosted; matters if data residency is a constraint.",
	},
	{
		label:       "DeepSeek",
		baseURL:     "https://api.deepseek.com/v1",
		defaultMdl:  "deepseek-chat",
		apiKeyEnv:   "DEEPSEEK_API_KEY",
		requiresKey: true,
		note:        "Cheapest of the cloud options. Note: hosted in China — prefer Ollama for sensitive code.",
	},
	{
		label:       "Ollama (local, fully offline)",
		baseURL:     "http://localhost:11434/v1",
		defaultMdl:  "qwen2.5-coder:7b",
		apiKeyEnv:   "",
		requiresKey: false,
		note:        "Runs against a local Ollama server. No API key needed. Pick any model you've pulled (e.g. qwen2.5-coder:32b, deepseek-coder-v2, codestral).",
	},
	{
		label:       "Other (custom OpenAI-compatible endpoint)",
		baseURL:     "",
		defaultMdl:  "",
		apiKeyEnv:   "LOCAL_REVIEW_API_KEY",
		requiresKey: true,
		note:        "Any /v1/chat/completions endpoint works (Groq, Together, OpenRouter, vLLM, etc.).",
	},
}

func runInit(out io.Writer, in io.Reader, target string, force bool) error {
	r := bufio.NewReader(in)

	if _, err := os.Stat(target); err == nil && !force {
		fmt.Fprintf(out, "%s already exists.\n", target)
		yn, err := promptString(out, r, "Overwrite?", "n")
		if err != nil {
			return err
		}
		if !isYes(yn) {
			fmt.Fprintln(out, "Aborted; no changes made.")
			return nil
		}
	}

	fmt.Fprintln(out, "local-review init — quick setup. Press Enter to accept defaults.")
	fmt.Fprintln(out)

	preset, err := promptProvider(out, r)
	if err != nil {
		return err
	}

	baseURL := preset.baseURL
	if baseURL == "" {
		baseURL, err = promptString(out, r, "Base URL (e.g. https://api.example.com/v1)", "")
		if err != nil {
			return err
		}
		if baseURL == "" {
			return fmt.Errorf("base URL is required for a custom provider")
		}
	}

	model, err := promptString(out, r, "Model", preset.defaultMdl)
	if err != nil {
		return err
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}

	var apiKeyEnv string
	if preset.requiresKey {
		apiKeyEnv, err = promptString(out, r, "API key environment variable", preset.apiKeyEnv)
		if err != nil {
			return err
		}
	}

	minSeverity, err := promptChoice(out, r, "Minimum severity to show", []string{"info", "warning", "major", "critical"}, 1)
	if err != nil {
		return err
	}

	maxFindingsStr, err := promptString(out, r, "Maximum findings per review", "20")
	if err != nil {
		return err
	}
	maxFindings, err := strconv.Atoi(strings.TrimSpace(maxFindingsStr))
	if err != nil || maxFindings <= 0 {
		return fmt.Errorf("max findings must be a positive integer, got %q", maxFindingsStr)
	}

	yml := renderConfig(baseURL, model, apiKeyEnv, minSeverity, maxFindings)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Config preview:")
	fmt.Fprintln(out, "----------------")
	fmt.Fprint(out, yml)
	fmt.Fprintln(out, "----------------")

	confirm, err := promptString(out, r, fmt.Sprintf("Write to %s?", target), "y")
	if err != nil {
		return err
	}
	if !isYes(confirm) {
		fmt.Fprintln(out, "Aborted; no changes made.")
		return nil
	}

	if err := os.WriteFile(target, []byte(yml), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "✓ Wrote %s\n", target)
	if preset.requiresKey && apiKeyEnv != "" {
		fmt.Fprintf(out, "  Set your API key:  export %s=...\n", apiKeyEnv)
	}
	fmt.Fprintln(out, "  Try a review:      local-review staged")
	return nil
}

func resolveTarget(location string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(location)) {
	case "", "local":
		return ".local-review.yml", nil
	case "global":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return home + "/.local-review.yml", nil
	default:
		return "", fmt.Errorf(`--location must be "local" or "global", got %q`, location)
	}
}

func promptProvider(out io.Writer, r *bufio.Reader) (providerPreset, error) {
	fmt.Fprintln(out, "Which provider?")
	for i, p := range providerPresets {
		fmt.Fprintf(out, "  %d) %s\n", i+1, p.label)
	}
	choice, err := promptString(out, r, "Choose", "1")
	if err != nil {
		return providerPreset{}, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(choice))
	if err != nil || n < 1 || n > len(providerPresets) {
		return providerPreset{}, fmt.Errorf("choice must be 1-%d, got %q", len(providerPresets), choice)
	}
	preset := providerPresets[n-1]
	if preset.note != "" {
		fmt.Fprintf(out, "  %s\n", preset.note)
	}
	return preset, nil
}

// promptChoice asks for a value from a fixed allow-list, returning the
// user's selection by name. The default is shown via (1-based) index.
func promptChoice(out io.Writer, r *bufio.Reader, label string, options []string, defaultIdx int) (string, error) {
	if defaultIdx < 0 || defaultIdx >= len(options) {
		defaultIdx = 0
	}
	defaultLabel := options[defaultIdx]
	fmt.Fprintf(out, "%s (%s) [%s]: ", label, strings.Join(options, "/"), defaultLabel)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	v := strings.TrimSpace(line)
	if v == "" {
		return defaultLabel, nil
	}
	for _, opt := range options {
		if strings.EqualFold(v, opt) {
			return opt, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s, got %q", label, strings.Join(options, "/"), v)
}

func promptString(out io.Writer, r *bufio.Reader, label, defaultVal string) (string, error) {
	if defaultVal != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultVal)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	v := strings.TrimSpace(line)
	if v == "" {
		return defaultVal, nil
	}
	return v, nil
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

// renderConfig writes a clean, commented YAML — not via go-yaml's
// marshaler, because we want comments and a stable field order. This is
// a local-review config; the audience is humans, not parsers.
func renderConfig(baseURL, model, apiKeyEnv, minSeverity string, maxFindings int) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# .local-review.yml — generated by `local-review init`.")
	fmt.Fprintln(&b, "# Edit freely; see examples/ in the repo for the full schema.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "provider:")
	fmt.Fprintf(&b, "  base_url: %s\n", baseURL)
	fmt.Fprintf(&b, "  model: %s\n", model)
	if apiKeyEnv != "" {
		fmt.Fprintf(&b, "  api_key_env: %s\n", apiKeyEnv)
	}
	fmt.Fprintln(&b, "  timeout_seconds: 60")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "review:")
	fmt.Fprintf(&b, "  min_severity: %s\n", minSeverity)
	fmt.Fprintf(&b, "  max_findings: %d\n", maxFindings)
	fmt.Fprintln(&b, "  exclude:")
	fmt.Fprintln(&b, `    - "**/*.lock"`)
	fmt.Fprintln(&b, `    - "**/dist/**"`)
	fmt.Fprintln(&b, `    - "**/build/**"`)
	fmt.Fprintln(&b, `    - "**/node_modules/**"`)
	return b.String()
}
