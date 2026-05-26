// Package cli handles detection, invocation, and version extraction for LLM CLI tools.
package cli

import (
	"os/exec"
	"sync"
)

// LLM represents a detected CLI tool with its metadata.
type LLM struct {
	Name    string // "claude", "gemini", "codex", "antigravity"
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

// experimentalLLMs are detected (and surfaced in `doctor`) but kept
// OUT of the review fan-out. antigravity (`agy`) is here because its
// `--print` mode runs an autonomous agent loop — it explores the repo,
// reconstructs its own diff instead of using the one it's handed, and
// emits tool-step narration rather than a clean review — so it can't
// yet serve as a stateless reviewer backend the way claude / gemini /
// codex do. Confirmed by the 2026-05 authenticated dogfood (see
// CLAUDE.md "Multi-LLM model — non-obvious facts"). Remove the entry
// once a structured-output invocation contract for agy is found.
var experimentalLLMs = map[string]bool{
	"antigravity": true,
}

// IsReviewCapable reports whether a detected LLM may join the review
// fan-out. Detection (binary present + authenticated) is necessary but
// not sufficient: an experimental CLI can be detected yet excluded —
// see experimentalLLMs.
func IsReviewCapable(name string) bool {
	return !experimentalLLMs[name]
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
	results := make([]LLM, len(supportedLLMs))
	var wg sync.WaitGroup

	for i, name := range supportedLLMs {
		wg.Add(1)
		go func(idx int, llmName string) {
			defer wg.Done()
			binaryName := binaryFor(llmName)
			if override, ok := overrides[llmName]; ok && override != "" {
				binaryName = override
			}
			results[idx] = detectWithBinary(llmName, binaryName)
		}(i, name)
	}

	wg.Wait()
	return results
}

// supportedLLMs is the canonical detection order. Mirrored by
// defaultBinaries below — keep the two in sync when adding a CLI.
var supportedLLMs = []string{"claude", "gemini", "codex", "antigravity"}

// defaultBinaries maps an LLM key to the executable name to probe.
// Most are identical; antigravity is the exception (Google's
// Gemini-CLI successor ships as `agy`). Single source of truth so
// Detect() and DetectAllWithOverrides() can't drift on binary names.
var defaultBinaries = map[string]string{
	"claude":      "claude",
	"gemini":      "gemini",
	"codex":       "codex",
	"antigravity": "agy",
}

// binaryFor returns the executable name to probe for an LLM key,
// falling back to the key itself for unmapped names (custom agents).
func binaryFor(name string) string {
	if b, ok := defaultBinaries[name]; ok {
		return b
	}
	return name
}

// Detect checks if a specific LLM CLI is installed and returns its metadata.
// Available is true only if the binary is found AND its version probe
// succeeded; see the LLM.Available field doc for the precise contract.
func Detect(name string) LLM {
	// Use the binary-name map (not the raw key) so Detect("antigravity")
	// probes `agy`, matching DetectAllWithOverrides. Unmapped names fall
	// back to the key itself, preserving the old behaviour for custom
	// agents whose binary == name.
	return detectWithBinary(name, binaryFor(name))
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
