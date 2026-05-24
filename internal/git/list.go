package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// RepoRoot returns the absolute path to the repository's top
// level via `git rev-parse --show-toplevel`. Used by the audit
// subcommand to resolve git-relative paths (which `git ls-files`
// returns) against the actual repo root rather than the user's
// current working directory — running `local-review audit` from
// a subdirectory would otherwise try to read paths against the
// subdir and fail.
//
// Returns an error when not inside a git working tree, matching
// git's own exit-non-zero behaviour. Callers should treat that
// the same as TrackedFiles failing.
func RepoRoot() (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

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
func TrackedFiles(root string) ([]string, error) {
	if root == "" {
		// Caller didn't supply a root — resolve it ourselves
		// once. Callers that already have a root (e.g.
		// audit.Walk) should pass it in to skip this lookup;
		// see internal/audit/walker.go for the threading.
		// Copilot caught the redundant rev-parse on PR #73.
		r, err := RepoRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	var stdout, stderr bytes.Buffer
	// Three flags combine to make the output repo-wide and
	// repo-root-relative regardless of where the binary was
	// invoked: `-C <root>` runs git as if from the worktree top,
	// `--full-name` forces repo-root-relative paths, and the
	// `:/` pathspec at the end tells git "everything tracked,"
	// not just things under the current subdirectory.
	// Without all three, invoking `local-review audit` from
	// `internal/audit/` would only list that subdirectory's
	// files — Copilot caught the original CWD-bug on PR #73.
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--full-name", ":/")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	// `-z` separates paths with NUL instead of newline so filenames
	// containing newlines (rare, but they exist) round-trip cleanly.
	// Without -z, a file named "foo\nbar.go" would split across two
	// entries and the audit would fail to read either one.
	return splitNullBytes(stdout.Bytes())
}

// splitNullBytes returns the input split on NUL bytes, dropping the
// trailing empty token that follows the final NUL terminator. Used
// by TrackedFiles to consume `git ls-files -z` output.
//
// Returns (slice, error) — scan errors propagate verbatim. A token
// exceeding the 4 MiB scanner buffer (extreme but possible on
// pathological monorepo file names) used to be swallowed and the
// caller would silently audit a truncated file list. Now surfaces
// the bufio.ErrTooLong instead. Caught by Copilot on PR #73.
func splitNullBytes(b []byte) ([]string, error) {
	if len(b) == 0 {
		return []string{}, nil
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
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan git ls-files output: %w", err)
	}
	return out, nil
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
