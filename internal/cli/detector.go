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
	Available bool
	// Model is the agent-specific model id (e.g., "claude-opus-4-7",
	// "gemini-2.0-flash", "gpt-5"). Threaded through from cfg.LLMs[*]
	// .Model so per-agent model overrides on the runner actually reach
	// the invoker — pre-fix the field was set in config and printed in
	// the roster but the invoker only got Path, so users got false
	// confirmation that the requested model ran.
	Model string
	// APIKey is the resolved API-key value sourced from
	// cfg.LLMs[name].APIKeyEnv (or the legacy hard-coded env var when
	// APIKeyEnv is empty). The invoker injects it into the subprocess
	// env under the *canonical* variable name each CLI expects
	// (ANTHROPIC_API_KEY / GEMINI_API_KEY / OPENAI_API_KEY) so users
	// can stash the key under any env name they like and the agent
	// still authenticates.
	APIKey     string
	TimeoutSec int // timeout in seconds for this LLM (from config)
}

// CanonicalAPIKeyEnv is the env var each CLI itself reads to find its
// API key. Doctor uses these as the default check when the user hasn't
// configured a custom APIKeyEnv; invokers use them as the injection
// target when the user *has* (so a key in $MY_GEMINI_KEY still ends
// up as $GEMINI_API_KEY for the gemini subprocess).
var CanonicalAPIKeyEnv = map[string]string{
	"claude": "ANTHROPIC_API_KEY",
	"gemini": "GEMINI_API_KEY",
	"codex":  "OPENAI_API_KEY",
}

// DetectAll checks for all supported LLM CLIs and returns their status.
// Returns a slice of LLM structs, one per supported CLI (even if not
// found). Runs detections concurrently to avoid sequential timeouts.
//
// Equivalent to DetectAllWithOverrides(nil) — kept for callers that
// don't need cli_path overrides (e.g. tests).
func DetectAll() []LLM {
	return DetectAllWithOverrides(nil)
}

// DetectAllWithOverrides is DetectAll plus per-LLM cli_path overrides
// from config (cfg.LLMs[name].CLIPath). An override may be an absolute
// path (`/opt/corporate/bin/claude`) or a bare binary name; exec.LookPath
// handles both — anything with a slash bypasses $PATH per Go's docs.
//
// Pre-fix the orchestrator detected only the hardcoded `claude` /
// `gemini` / `codex` binary names, so the cli_path field shipped in
// every example config was inert. Users with corporate installs at
// non-standard paths got "✗ not installed" with no path-override
// escape hatch.
func DetectAllWithOverrides(overrides map[string]string) []LLM {
	defaults := map[string]string{
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
			binaryName := defaults[llmName]
			if override, ok := overrides[llmName]; ok && override != "" {
				binaryName = override
			}
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
	version := detectVersion(path)

	return LLM{
		Name:      name,
		Path:      path,
		Version:   version,
		Available: version != "unknown",
	}
}
