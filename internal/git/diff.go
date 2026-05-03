// Package git wraps the user's local `git` CLI for diff extraction.
//
// We deliberately shell out instead of using a Go git library:
//   - The user's repo state already works with their git binary
//     (config, hooks, signatures, etc.).
//   - Pulling in go-git would balloon the binary by ~10MB.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Mode says which slice of history we want to review.
type Mode int

const (
	// ModeStaged: `git diff --cached` — what would be committed next.
	ModeStaged Mode = iota
	// ModeCommit: a single commit (revspec like HEAD or a SHA).
	ModeCommit
	// ModeBranch: full diff of a branch against a base ref.
	ModeBranch
)

// Diff is a single diffed file with its hunks.
type Diff struct {
	Path  string
	Hunks []Hunk
}

type Hunk struct {
	Header  string // raw "@@ -1,2 +1,3 @@" line
	Content string // body of the hunk (lines prefixed with +, -, space)
	NewFrom int    // first line number in new file
}

// Extract runs git and returns one Diff per changed file.
//
// args:
//
//	mode: which slice of history
//	ref:  for ModeCommit, the revspec; for ModeBranch, the *base* ref to diff against
func Extract(mode Mode, ref string) ([]Diff, error) {
	args, err := argsFor(mode, ref)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, stderr.String())
	}

	return parseUnifiedDiff(stdout.String()), nil
}

func argsFor(mode Mode, ref string) ([]string, error) {
	// -U10 gives 10 lines of context around each hunk so the LLM has
	// enough surrounding code to reason about. Larger windows = more
	// tokens; this is the sweet spot.
	const ctx = "-U10"
	switch mode {
	case ModeStaged:
		return []string{"diff", "--cached", ctx}, nil
	case ModeCommit:
		if ref == "" {
			ref = "HEAD"
		}
		// `show` by itself includes the commit message. We just want the diff.
		return []string{"show", ctx, "--format=", ref}, nil
	case ModeBranch:
		if ref == "" {
			ref = "main"
		}
		// Three-dot: diff from common ancestor to current HEAD.
		return []string{"diff", ctx, ref + "...HEAD"}, nil
	default:
		return nil, fmt.Errorf("unknown mode %d", mode)
	}
}

// parseUnifiedDiff walks `git diff` output and groups hunks by file.
// We don't need a fully-featured patch library — just enough to feed
// content + line numbers to the LLM.
func parseUnifiedDiff(s string) []Diff {
	var diffs []Diff
	var cur *Diff
	var hunk *Hunk

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			diffs = append(diffs, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			flushFile()
			cur = &Diff{}
		case strings.HasPrefix(line, "+++ b/"):
			if cur != nil {
				cur.Path = strings.TrimPrefix(line, "+++ b/")
			}
		case strings.HasPrefix(line, "+++ ") && cur != nil && cur.Path == "":
			// Fallback for unusual paths
			cur.Path = strings.TrimPrefix(line, "+++ ")
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			hunk = &Hunk{Header: line, NewFrom: parseNewFrom(line)}
		case hunk != nil:
			hunk.Content += line + "\n"
		}
	}
	flushFile()

	// Drop any malformed entries (no path means we never saw a file header)
	out := diffs[:0]
	for _, d := range diffs {
		if d.Path != "" {
			out = append(out, d)
		}
	}
	return out
}

// parseNewFrom extracts the "+N" line number from a hunk header
// like "@@ -1,2 +37,5 @@". Returns 0 on parse failure.
func parseNewFrom(header string) int {
	i := strings.Index(header, "+")
	if i < 0 {
		return 0
	}
	rest := header[i+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		end = len(rest)
	}
	n := 0
	for _, c := range rest[:end] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// CurrentCommit returns the current HEAD commit hash (short form).
// Returns "HEAD" if git command fails or times out.
func CurrentCommit() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}

// CurrentBranch returns the current branch name.
// Returns "unknown" if git command fails or times out (e.g., detached HEAD).
func CurrentBranch() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// SanitizeBranchName replaces characters that are unsafe for filesystem paths.
// Replaces / with - to handle branch names like "feature/auth-fix".
func SanitizeBranchName(branch string) string {
	// Prevent path traversal
	s := strings.ReplaceAll(branch, "..", "--")

	// Replace filesystem-unsafe characters (Windows + Unix)
	unsafe := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", " ", "\x00"}
	for _, char := range unsafe {
		s = strings.ReplaceAll(s, char, "-")
	}

	// Remove control characters
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '-'
		}
		return r
	}, s)

	return s
}

// SanitizeCommit removes any characters that aren't valid in git commit hashes.
// Only allows [a-fA-F0-9-] to prevent path traversal via commit parameters.
func SanitizeCommit(commit string) string {
	var result strings.Builder
	for _, r := range commit {
		if (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// ResolveRef resolves a git ref (branch name, tag, short hash) to a full commit hash.
// Returns the first 7 characters of the commit hash, or empty string if resolution fails or times out.
func ResolveRef(ref string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", ref)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
