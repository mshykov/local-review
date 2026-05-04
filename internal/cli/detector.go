// Package cli handles detection, invocation, and version extraction for LLM CLI tools.
package cli

import (
	"os/exec"
	"sync"
)

// LLM represents a detected CLI tool with its metadata.
type LLM struct {
	Name    string // "claude", "gemini", "codex"
	Path    string // full path to the binary (e.g., "/usr/local/bin/claude")
	Version string // version string (e.g., "2.1.0"), or "unknown" if version probe failed
	// Available is true only when both the binary is in PATH AND the
	// `--version` probe returned a parseable string. A binary that
	// resolves but whose version probe fails (broken symlink, corrupted
	// install, missing runtime) is reported Available=false so callers
	// don't try to invoke an unusable tool.
	Available  bool
	TimeoutSec int // timeout in seconds for this LLM (from config)
}

// DetectAll checks for all supported LLM CLIs and returns their status.
// Returns a slice of LLM structs, one per supported CLI (even if not found).
// Runs detections concurrently to avoid sequential timeouts.
func DetectAll() []LLM {
	// Map of LLM names to their binary names (for cases where they differ)
	llmBinaries := map[string]string{
		"claude": "claude",
		"gemini": "gemini",
		"codex":  "codex",
	}

	llms := []string{"claude", "gemini", "codex"}
	results := make([]LLM, len(llms))
	var wg sync.WaitGroup

	for i, name := range llms {
		wg.Add(1)
		go func(idx int, llmName string) {
			defer wg.Done()
			binaryName := llmBinaries[llmName]
			results[idx] = detectWithBinary(llmName, binaryName)
		}(i, name)
	}

	wg.Wait()
	return results
}

// Detect checks if a specific LLM CLI is installed and returns its metadata.
// Available is true only if the binary is found AND its version probe
// succeeded; see the LLM.Available field doc for the precise contract.
func Detect(name string) LLM {
	return detectWithBinary(name, name)
}

// detectWithBinary checks if a specific LLM CLI is installed using a custom binary name.
// This is useful when the LLM name differs from its binary name (e.g., "myLLM" uses "myLLM-cli").
func detectWithBinary(name, binaryName string) LLM {
	path, err := exec.LookPath(binaryName)
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
		Available: version != "unknown",
	}
}
