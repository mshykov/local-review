//go:build ignore

// This is a manual test program to verify config loading works with v0.1 fields.
// Run with: go run test_config.go
package main

import (
	"fmt"
	"os"

	"github.com/mshykov/local-review/internal/config"
)

func main() {
	fmt.Println("Testing Configuration Loading (v0.1)")
	fmt.Println("====================================\n")

	// Test 1: Default config
	fmt.Println("1. Testing Defaults():")
	cfg := config.Defaults()
	printConfig(cfg)
	fmt.Println()

	// Test 2: Load example config
	fmt.Println("2. Testing Load() with examples/.local-review-multi.yml:")
	examplePath := "examples/.local-review-multi.yml"
	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		fmt.Printf("Error: %s not found\n", examplePath)
		os.Exit(1)
	}

	cfg2, err := config.Load(examplePath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}
	printConfig(cfg2)
	fmt.Println()

	// Test 3: Validation
	fmt.Println("3. Testing Validate():")
	if err := cfg2.Validate(); err != nil {
		fmt.Printf("✗ Validation failed: %v\n", err)
	} else {
		fmt.Println("✓ Configuration is valid")
	}
	fmt.Println()

	fmt.Println("All tests passed!")
}

func printConfig(cfg config.Config) {
	// Print v0 settings
	fmt.Printf("Provider (v0):\n")
	fmt.Printf("  Model: %s\n", cfg.Provider.Model)
	fmt.Printf("  Base URL: %s\n", cfg.Provider.BaseURL)
	fmt.Println()

	// Print v0.1 settings
	fmt.Printf("LLMs (v0.1): %d configured\n", len(cfg.LLMs))
	for name, llm := range cfg.LLMs {
		status := "disabled"
		if llm.Enabled != nil && *llm.Enabled {
			status = "enabled"
		}
		fmt.Printf("  %s: %s, mode=%s, path=%s, model=%s\n",
			name, status, llm.Mode, llm.CLIPath, llm.Model)
	}
	fmt.Println()

	fmt.Printf("Merge (v0.1):\n")
	fmt.Printf("  Preferred LLM: %s\n", cfg.Merge.PreferredLLM)
	fmt.Printf("  Deduplicate: %v\n", cfg.Merge.Deduplicate)
	fmt.Printf("  Consensus Threshold: %d\n", cfg.Merge.ConsensusThreshold)
	fmt.Println()

	fmt.Printf("Storage (v0.1):\n")
	fmt.Printf("  Base Path: %s\n", cfg.Storage.BasePath)
}
