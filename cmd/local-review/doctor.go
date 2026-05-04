package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/cli"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check LLM CLI installations and authentication status",
		Long: `Doctor checks which LLM CLIs are installed and available for multi-review.

It detects:
  - Claude CLI (claude)
  - Gemini CLI (gemini)
  - OpenAI Codex CLI (codex)

For each CLI, it shows version and installation status.
If any are missing, it provides installation instructions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	fmt.Println("Checking LLM installations...")
	fmt.Println()

	llms := cli.DetectAll()

	// Count available LLMs
	availableCount := 0
	for _, llm := range llms {
		if llm.Available {
			availableCount++
		}
	}

	// Print status for each LLM
	for _, llm := range llms {
		displayName := getDisplayName(llm.Name)

		if llm.Available {
			fmt.Printf("✓ %-15s v%-10s (found at %s)\n", displayName, llm.Version, llm.Path)
		} else {
			fmt.Printf("✗ %-15s not found\n", displayName)
			printInstallInstructions(llm.Name)
		}
	}

	fmt.Println()
	fmt.Printf("%d/%d LLMs ready for multi-review.\n", availableCount, len(llms))

	if availableCount < len(llms) {
		fmt.Println()
		fmt.Println("Missing LLMs:", getMissingLLMNames(llms))
	}

	// Check for API fallback configurations
	fmt.Println()
	printAPIFallbackStatus()

	return nil
}

func getDisplayName(name string) string {
	switch name {
	case "claude":
		return "Claude CLI"
	case "gemini":
		return "Gemini CLI"
	case "codex":
		return "Codex CLI"
	default:
		return name
	}
}

func printInstallInstructions(name string) {
	switch name {
	case "claude":
		fmt.Println("  Install: npm install -g @anthropic-ai/claude-code")
		fmt.Println("  Auth:    claude login")
	case "gemini":
		fmt.Println("  Install: npm install -g @google/gemini-cli")
		fmt.Println("  Requires: Node.js 20+")
	case "codex":
		fmt.Println("  Install: npm install -g @openai/codex")
		fmt.Println("  Note: requires a ChatGPT Plus subscription")
	}
}

func getMissingLLMNames(llms []cli.LLM) string {
	var missing []string
	for _, llm := range llms {
		if !llm.Available {
			missing = append(missing, llm.Name)
		}
	}

	if len(missing) == 0 {
		return "none"
	}

	return strings.Join(missing, ", ")
}

func printAPIFallbackStatus() {
	fmt.Println("API fallbacks configured:")

	// Check for API keys in environment (in deterministic order)
	apiKeys := []struct {
		Name   string
		EnvVar string
	}{
		{"Claude", "ANTHROPIC_API_KEY"},
		{"Gemini", "GEMINI_API_KEY"},
		{"Codex", "OPENAI_API_KEY"},
	}

	for _, key := range apiKeys {
		if os.Getenv(key.EnvVar) != "" {
			fmt.Printf("✓ %-15s (%s set)\n", key.Name, key.EnvVar)
		} else {
			fmt.Printf("✗ %-15s (no API key)\n", key.Name)
		}
	}

	fmt.Println()
	fmt.Println("Note: CLI mode is preferred. API keys are used as fallback only.")
}
