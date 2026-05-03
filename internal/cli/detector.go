// Package cli handles detection, invocation, and version extraction for LLM CLI tools.
package cli

import (
	"os/exec"
	"sync"
)

// LLM represents a detected CLI tool with its metadata.
type LLM struct {
	Name       string // "claude", "gemini", "codex", "copilot"
	Path       string // full path to the binary (e.g., "/usr/local/bin/claude")
	Version    string // version string (e.g., "2.1.0")
	Available  bool   // true if CLI is found in PATH
	TimeoutSec int    // timeout in seconds for this LLM (from config)
}

// DetectAll checks for all supported LLM CLIs and returns their status.
// Returns a slice of LLM structs, one per supported CLI (even if not found).
// Runs detections concurrently to avoid sequential timeouts.
func DetectAll() []LLM {
	llms := []string{"claude", "gemini", "codex", "gh"}
	results := make([]LLM, len(llms))
	var wg sync.WaitGroup

	for i, name := range llms {
		wg.Add(1)
		go func(idx int, llmName string) {
			defer wg.Done()
			results[idx] = Detect(llmName)
		}(i, name)
	}

	wg.Wait()
	return results
}

// Detect checks if a specific LLM CLI is installed and returns its metadata.
// If the CLI is not found, Available will be false.
func Detect(name string) LLM {
	path, err := exec.LookPath(name)
	if err != nil {
		// CLI not found in PATH
		return LLM{
			Name:      name,
			Available: false,
		}
	}

	// Extract version (may return "unknown" if version detection fails)
	version := detectVersion(name, path)

	return LLM{
		Name:      name,
		Path:      path,
		Version:   version,
		Available: true,
	}
}
