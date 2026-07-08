package cli

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// detectVersion runs the version command for the CLI at `path` and
// extracts a parseable version number. Returns "unknown" if the
// command fails or the output doesn't match any pattern.
//
// All supported CLIs use the `--version` flag; if a future CLI
// diverges (e.g. `--ver`, `version` subcommand), branch here on
// path's basename rather than the agent name — the path is what
// determines which binary's flag format we're calling.
func detectVersion(path string) string {
	return runVersionCmd(path, "--version")
}

// runVersionCmd executes the version command and parses the output.
// Extracts version strings matching common patterns: v1.2.3, 1.2.3, etc.
func runVersionCmd(path string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = subprocessWaitDelay
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown"
	}

	return parseVersion(string(output))
}

// parseVersion extracts a version number from command output.
// Matches patterns like: v1.2.3, 1.2.3, version 1.2.3, etc.
func parseVersion(output string) string {
	// Try common version patterns
	patterns := []string{
		`v?(\d+\.\d+\.\d+)`,              // v1.2.3 or 1.2.3
		`version[:\s]+v?(\d+\.\d+\.\d+)`, // version: 1.2.3
		`(\d+\.\d+)`,                     // 1.2 (fallback)
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(output)
		if len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	return "unknown"
}
