package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

By default the config goes in the current directory; pass
--location=global to write ~/.local-review.yml instead.`,
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
//
// v0.15: the wizard now writes its output under `llms.<presetName>:`
// (the v0.14 unified-agent shape), not the v0 top-level `provider:`
// block which was removed in v0.15. presetName is the short, free-form
// agent identifier that appears in `doctor`, `--only`, `--with`, and
// the on-disk review filename — users are free to rename it after init.
type providerPreset struct {
	label       string // shown in the menu
	presetName  string // free-form agent name (lowercase, single word)
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
		presetName:  "openai",
		baseURL:     "https://api.openai.com/v1",
		defaultMdl:  "gpt-4o-mini",
		apiKeyEnv:   "OPENAI_API_KEY",
		requiresKey: true,
		note:        "Cheap default: gpt-4o-mini. Bump to gpt-4o for harder reviews.",
	},
	{
		label:       "Anthropic (Claude)",
		presetName:  "anthropic",
		baseURL:     "https://api.anthropic.com/v1",
		defaultMdl:  "claude-sonnet-4-6",
		apiKeyEnv:   "ANTHROPIC_API_KEY",
		requiresKey: true,
		note:        "Uses Anthropic's OpenAI-compatible chat-completions endpoint. Model name must be exact (e.g. claude-sonnet-4-6, claude-opus-4-7) — a wrong name returns 404.",
	},
	{
		label:       "Mistral (EU-hosted)",
		presetName:  "mistral",
		baseURL:     "https://api.mistral.ai/v1",
		defaultMdl:  "codestral-latest",
		apiKeyEnv:   "MISTRAL_API_KEY",
		requiresKey: true,
		note:        "Codestral is purpose-built for code. EU-hosted; matters if data residency is a constraint.",
	},
	{
		label:       "DeepSeek",
		presetName:  "deepseek",
		baseURL:     "https://api.deepseek.com/v1",
		defaultMdl:  "deepseek-chat",
		apiKeyEnv:   "DEEPSEEK_API_KEY",
		requiresKey: true,
		note:        "Cheapest of the cloud options. Note: hosted in China — prefer Ollama for sensitive code.",
	},
	{
		label:       "Kimi (Moonshot)",
		presetName:  "kimi",
		baseURL:     "https://api.moonshot.ai/v1",
		defaultMdl:  "kimi-k2-0905-preview",
		apiKeyEnv:   "MOONSHOT_API_KEY",
		requiresKey: true,
		note:        "Moonshot's OpenAI-compatible endpoint; Kimi K2 is strong on code. Use api.moonshot.cn instead if your key is China-region. Verify the current model id at platform.moonshot.ai.",
	},
	{
		label:       "Groq (fast inference)",
		presetName:  "groq",
		baseURL:     "https://api.groq.com/openai/v1",
		defaultMdl:  "llama-3.3-70b-versatile",
		apiKeyEnv:   "GROQ_API_KEY",
		requiresKey: true,
		note:        "Very fast hosted inference. Pick a coder-capable model from console.groq.com/docs/models (e.g. a Qwen-Coder or Kimi-K2 build when listed).",
	},
	{
		label:       "OpenRouter (gateway to many models)",
		presetName:  "openrouter",
		baseURL:     "https://openrouter.ai/api/v1",
		defaultMdl:  "deepseek/deepseek-chat",
		apiKeyEnv:   "OPENROUTER_API_KEY",
		requiresKey: true,
		note:        "One key, any model — the model id is `vendor/model` (e.g. anthropic/claude-sonnet-4, deepseek/deepseek-chat, moonshotai/kimi-k2). Browse openrouter.ai/models.",
	},
	{
		label:       "Qwen (Alibaba DashScope)",
		presetName:  "qwen",
		baseURL:     "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		defaultMdl:  "qwen-coder-plus",
		apiKeyEnv:   "DASHSCOPE_API_KEY",
		requiresKey: true,
		note:        "DashScope's OpenAI-compatible mode (international endpoint shown; drop `-intl` for the China region). Qwen-Coder models are purpose-built for code.",
	},
	{
		label:       "Ollama (local, fully offline)",
		presetName:  "ollama",
		baseURL:     "http://localhost:11434/v1",
		defaultMdl:  "qwen2.5-coder:7b",
		apiKeyEnv:   "",
		requiresKey: false,
		note:        "Runs against a local Ollama server. No API key needed. Pick any model you've pulled (e.g. qwen2.5-coder:32b, deepseek-coder-v2, codestral).",
	},
	{
		label:       "Other (custom OpenAI-compatible endpoint)",
		presetName:  "provider",
		baseURL:     "",
		defaultMdl:  "",
		apiKeyEnv:   "YOUR_PROVIDER_API_KEY",
		requiresKey: true,
		note:        "Any /v1/chat/completions endpoint works (Groq, Together, OpenRouter, vLLM, etc.). Edit the agent name (`provider:`) under `llms:` in the file to whatever reads best for your team.",
	},
}

func runInit(out io.Writer, in io.Reader, target string, force bool) error {
	r := bufio.NewReader(in)

	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory; refusing to overwrite", target)
		}
		if !force {
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
	}

	fmt.Fprintln(out, "local-review init — quick setup. Press Enter to accept defaults.")
	fmt.Fprintln(out)

	preset, err := promptProviderRetry(out, r)
	if err != nil {
		return err
	}

	baseURL := preset.baseURL
	if baseURL == "" {
		baseURL, err = promptNonEmpty(out, r, "Base URL (e.g. https://api.example.com/v1)", "")
		if err != nil {
			return err
		}
	}

	model, err := promptNonEmpty(out, r, "Model", preset.defaultMdl)
	if err != nil {
		return err
	}

	var apiKeyEnv string
	if preset.requiresKey {
		apiKeyEnv, err = promptString(out, r, "API key environment variable", preset.apiKeyEnv)
		if err != nil {
			return err
		}
	}

	minSeverity, err := promptChoiceRetry(out, r, "Minimum severity to show", []string{"nit", "info", "warning", "major", "critical"}, 2)
	if err != nil {
		return err
	}

	maxFindings, err := promptPositiveIntRetry(out, r, "Maximum findings per review", 20)
	if err != nil {
		return err
	}

	yml := renderConfig(preset.presetName, baseURL, model, apiKeyEnv, minSeverity, maxFindings)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Config preview:")
	fmt.Fprintln(out, "----------------")
	fmt.Fprint(out, yml)
	fmt.Fprintln(out, "----------------")

	if force {
		// --force is the non-interactive flag; don't prompt for the final confirmation either.
		fmt.Fprintf(out, "Writing to %s (--force).\n", target)
	} else {
		confirm, err := promptString(out, r, fmt.Sprintf("Write to %s?", target), "y")
		if err != nil {
			return err
		}
		if !isYes(confirm) {
			fmt.Fprintln(out, "Aborted; no changes made.")
			return nil
		}
	}

	if err := os.WriteFile(target, []byte(yml), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	// os.WriteFile only applies the mode on *create*; an existing
	// .local-review.yml with broader perms keeps them. Lock to 0600
	// explicitly so --force overwrites match the new-file behavior.
	if err := os.Chmod(target, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", target, err)
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
		return filepath.Join(home, ".local-review.yml"), nil
	default:
		return "", fmt.Errorf(`--location must be "local" or "global", got %q`, location)
	}
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

// maxRetries caps how many times we re-prompt on bad input before
// giving up. Without this, a piped script with no valid answer could
// loop forever. Pure-interactive users hit Ctrl-C long before this.
const maxRetries = 5

// promptNonEmpty re-prompts until it gets a non-empty answer (or the
// default). Returns the underlying error if reading stdin fails.
func promptNonEmpty(out io.Writer, r *bufio.Reader, label, defaultVal string) (string, error) {
	for i := 0; i < maxRetries; i++ {
		v, err := promptString(out, r, label, defaultVal)
		if err != nil {
			return "", err
		}
		if v != "" {
			return v, nil
		}
		fmt.Fprintln(out, "  (required) please enter a value.")
	}
	return "", fmt.Errorf("%s: too many empty answers, giving up", label)
}

// promptChoiceRetry re-prompts on bad input instead of aborting the
// whole wizard. Tells the user what's expected each time.
func promptChoiceRetry(out io.Writer, r *bufio.Reader, label string, options []string, defaultIdx int) (string, error) {
	for i := 0; i < maxRetries; i++ {
		v, err := promptChoice(out, r, label, options, defaultIdx)
		if err == nil {
			return v, nil
		}
		fmt.Fprintf(out, "  %v\n", err)
	}
	return "", fmt.Errorf("%s: too many invalid answers, giving up", label)
}

// promptPositiveIntRetry re-prompts until it gets a positive integer.
func promptPositiveIntRetry(out io.Writer, r *bufio.Reader, label string, defaultVal int) (int, error) {
	defStr := strconv.Itoa(defaultVal)
	for i := 0; i < maxRetries; i++ {
		s, err := promptString(out, r, label, defStr)
		if err != nil {
			return 0, err
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr == nil && n > 0 {
			return n, nil
		}
		fmt.Fprintf(out, "  must be a positive integer, got %q\n", s)
	}
	return 0, fmt.Errorf("%s: too many invalid answers, giving up", label)
}

// promptProviderRetry re-prompts on bad provider choice.
func promptProviderRetry(out io.Writer, r *bufio.Reader) (providerPreset, error) {
	fmt.Fprintln(out, "Which provider?")
	for i, p := range providerPresets {
		fmt.Fprintf(out, "  %d) %s\n", i+1, p.label)
	}
	for i := 0; i < maxRetries; i++ {
		choice, err := promptString(out, r, "Choose", "1")
		if err != nil {
			return providerPreset{}, err
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(choice))
		if convErr == nil && n >= 1 && n <= len(providerPresets) {
			preset := providerPresets[n-1]
			if preset.note != "" {
				fmt.Fprintf(out, "  %s\n", preset.note)
			}
			return preset, nil
		}
		fmt.Fprintf(out, "  choice must be 1-%d, got %q\n", len(providerPresets), choice)
	}
	return providerPreset{}, fmt.Errorf("provider: too many invalid answers, giving up")
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

// yamlScalar quotes a string as a YAML double-quoted scalar so that
// values containing '#', ':', leading reserved chars (-, ?, *, &, !,
// |, >, %), or anything else YAML-special parse correctly.
//
// Go's strconv.Quote produces double-quoted strings with the same
// escape semantics YAML supports for double-quoted flow scalars (\n,
// \t, \uXXXX, \"). The output is slightly noisier than unquoted
// YAML, but it's unambiguous and round-trips through any parser.
func yamlScalar(s string) string {
	return strconv.Quote(s)
}

// renderConfig writes a clean, commented YAML — not via go-yaml's
// marshaler, because we want comments and a stable field order. This is
// a local-review config; the audience is humans, not parsers.
//
// v0.15: the output uses the unified `llms.<presetName>:` shape (the
// top-level `provider:` block was removed in v0.15). presetName is
// the agent identifier — free-form, surfaces in `doctor`, `--only`,
// `--with`, and the on-disk review filename. Users are free to rename
// it after init; the wizard picks a sensible vendor-shaped default
// (e.g. `openai`, `ollama`, `anthropic`).
func renderConfig(presetName, baseURL, model, apiKeyEnv, minSeverity string, maxFindings int) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# .local-review.yml — generated by `local-review init`.")
	fmt.Fprintln(&b, "# Edit freely; see examples/ in the repo for the full schema.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "# Provider endpoint. Rename the agent (`"+presetName+":`) to anything you")
	fmt.Fprintln(&b, "# like — it just identifies the entry in `doctor` / `--only` / `--with` /")
	fmt.Fprintln(&b, "# on-disk review filenames. The v0 top-level `provider:` block was removed")
	fmt.Fprintln(&b, "# in v0.15; every endpoint now lives under `llms:` and runs side-by-side")
	fmt.Fprintln(&b, "# with the CLI agents (claude / codex / copilot / gemini).")
	fmt.Fprintln(&b, "llms:")
	fmt.Fprintf(&b, "  %s:\n", presetName)
	fmt.Fprintf(&b, "    base_url: %s\n", yamlScalar(baseURL))
	fmt.Fprintf(&b, "    model: %s\n", yamlScalar(model))
	if apiKeyEnv != "" {
		fmt.Fprintf(&b, "    api_key_env: %s\n", yamlScalar(apiKeyEnv))
	}
	fmt.Fprintln(&b, "    timeout_seconds: 60")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "review:")
	fmt.Fprintf(&b, "  min_severity: %s\n", yamlScalar(minSeverity))
	fmt.Fprintf(&b, "  max_findings: %d\n", maxFindings)
	// Config slices are replaced wholesale by the cascade merge (see
	// internal/config/config.go), so the wizard must reproduce the
	// built-in defaults *plus* any additions. Built-in defaults today are
	// **/*.lock, **/*.snap, **/dist/**, **/build/**; the wizard also adds
	// **/node_modules/** as a convenience. TestRenderConfig_ReproducesAll-
	// DefaultExcludeGlobs guards that every default stays reproduced here.
	fmt.Fprintln(&b, "  exclude:")
	fmt.Fprintln(&b, `    - "**/*.lock"`)
	fmt.Fprintln(&b, `    - "**/*.snap"`)
	fmt.Fprintln(&b, `    - "**/dist/**"`)
	fmt.Fprintln(&b, `    - "**/build/**"`)
	fmt.Fprintln(&b, `    - "**/node_modules/**"`)
	return b.String()
}
