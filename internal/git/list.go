package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// TrackedFiles returns every git-tracked file under the current
// working tree, one path per element, relative to the repo root.
// Used by the audit subcommand to enumerate source the LLM should
// scan — git's view (excludes .gitignore'd build artifacts, vendor
// caches, .env, etc.) is exactly the "code we ship" surface the
// audit cares about.
//
// Output ordering is git's: paths in tree order, deterministic
// across invocations on the same commit. Caller groups by directory
// for chunking; see internal/audit/walker.go.
//
// Returns an empty slice (not nil) when the repo has no tracked
// files — same shape as `git ls-files` with empty stdout. An empty
// repo is a real edge case (just-`git init`'d project); the audit
// runner reports "nothing to scan" rather than crashing.
//
// Shells out via `os/exec` for the same reason internal/git/diff.go
// does: keeps the binary go-git-free per the project's hard
// constraints (CLAUDE.md "What this project is" section).
func TrackedFiles() ([]string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	// `-z` separates paths with NUL instead of newline so filenames
	// containing newlines (rare, but they exist) round-trip cleanly.
	// Without -z, a file named "foo\nbar.go" would split across two
	// entries and the audit would fail to read either one.
	return splitNullBytes(stdout.Bytes()), nil
}

// splitNullBytes returns the input split on NUL bytes, dropping the
// trailing empty token that follows the final NUL terminator. Used
// by TrackedFiles to consume `git ls-files -z` output. Exported
// behavior matches strings.Split with an explicit "drop trailing
// empty" rule.
func splitNullBytes(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	out := []string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // up to 4 MiB per token (long paths in monorepos)
	sc.Split(scanNull)
	for sc.Scan() {
		t := sc.Text()
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// scanNull is a bufio.SplitFunc that yields one token per NUL-
// separated record. Mirrors bufio.ScanLines but for '\x00'.
func scanNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	// At EOF with leftover non-NUL bytes — last token wasn't
	// NUL-terminated; return what we have.
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
