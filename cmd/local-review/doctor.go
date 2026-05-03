package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

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
  - GitHub CLI (gh) for Copilot

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
	case "gh":
		return "Copilot CLI"
	default:
		return name
	}
}

func printInstallInstructions(name string) {
	switch name {
	case "claude":
		fmt.Println("  Install: npm install -g @anthropic/claude-cli")
		fmt.Println("  Auth:    claude login")
	case "gemini":
		fmt.Println("  Install: npm install -g @google/gemini-cli@0.40.0")
		fmt.Println("  Requires: Node.js 20+")
	case "codex":
		fmt.Println("  Install: npm install -g @openai/codex@0.128.0")
	case "gh":
		fmt.Println("  Install: brew install gh")
		fmt.Println("  Auth:    gh auth login")
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
		{"Copilot", "GITHUB_TOKEN"},
	}

	for _, key := range apiKeys {
		llm, envVar := key.Name, key.EnvVar

		// Special case for Copilot - check gh auth status with timeout
		if llm == "Copilot" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, "gh", "auth", "status")
			cmd.Stdout = io.Discard // Suppress gh output
			cmd.Stderr = io.Discard // Suppress gh output
			err := cmd.Run()
			cancel() // Explicitly cancel to avoid leak in loop

			if err == nil {
				fmt.Printf("✓ %-15s (gh auth configured)\n", llm)
			} else if os.Getenv(envVar) != "" {
				fmt.Printf("✓ %-15s (%s set)\n", llm, envVar)
			} else {
				fmt.Printf("✗ %-15s (not authenticated - run 'gh auth login')\n", llm)
			}
			continue
		}

		if os.Getenv(envVar) != "" {
			fmt.Printf("✓ %-15s (%s set)\n", llm, envVar)
		} else {
			fmt.Printf("✗ %-15s (no API key)\n", llm)
		}
	}

	fmt.Println()
	fmt.Println("Note: CLI mode is preferred. API keys are used as fallback only.")
}
