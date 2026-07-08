// Package git wraps the user's local `git` CLI for diff extraction.
//
// We deliberately shell out instead of using a Go git library:
//   - The user's repo state already works with their git binary
//     (config, hooks, signatures, etc.).
//   - Pulling in go-git would balloon the binary by ~10MB.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
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

// gitDiffTimeout bounds a single diff extraction so a wedged git (a lock,
// a slow clean/smudge filter, a pathological repo) can't hang the tool
// forever even without a user Ctrl+C. Generous — local diffs are fast; this
// is a backstop, not a tuning knob. The caller's ctx (which carries the
// signal-handler cancellation) still interrupts sooner on Ctrl+C.
const gitDiffTimeout = 2 * time.Minute

// maxDiffBytes caps the diff we buffer and parse. Past this, the review is
// almost certainly noise (vendored-blob churn, a generated-file rewrite) and
// parsing risks a multi-hundred-MB memory peak. var (not const) so tests can
// shrink it. Fail-closed: over the cap returns an actionable error rather
// than OOMing or silently truncating.
var maxDiffBytes int64 = 64 << 20 // 64 MiB

// Extract runs git and returns one Diff per changed file.
//
// args:
//
//	ctx:  cancellation / deadline (the runner threads its signal-trapped ctx)
//	mode: which slice of history
//	ref:  for ModeCommit, the revspec; for ModeBranch, the *base* ref to diff against
func Extract(ctx context.Context, mode Mode, ref string) ([]Diff, error) {
	args, err := argsFor(mode, ref)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
	defer cancel()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	// Bound the buffered diff. LimitReader stops at maxDiffBytes+1 so we can
	// tell "exactly at cap" from "over". On overflow, cancel() first to
	// unblock git (now blocked writing to a full pipe) so Wait doesn't hang.
	var stdout bytes.Buffer
	n, copyErr := io.Copy(&stdout, io.LimitReader(pipe, maxDiffBytes+1))
	if n > maxDiffBytes {
		cancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("diff exceeds the %d MiB cap — narrow it with include/exclude globs or review a smaller commit range", maxDiffBytes>>20)
	}
	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		return nil, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, stderr.String())
	}
	// Fail closed on a read error: a partial diff + nil err would let the
	// gate exit 0 on a materially incomplete review.
	if copyErr != nil {
		return nil, fmt.Errorf("read diff: %w", copyErr)
	}

	// Stream-parse stdout via bytes.NewReader → bufio.Scanner so we don't
	// double-buffer the diff (string copy + strings.Split slice).
	diffs, err := parseUnifiedDiff(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		// Same fail-closed reasoning: a scanner error means we have only the
		// prefix, and the unscanned tail might carry blocking changes.
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	return diffs, nil
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
		if err := ValidateRef(ref); err != nil {
			return nil, err
		}
		// `show` by itself includes the commit message. We just want the diff.
		return []string{"show", ctx, "--format=", ref}, nil
	case ModeBranch:
		if ref == "" {
			ref = "main"
		}
		if err := ValidateRef(ref); err != nil {
			return nil, err
		}
		// Three-dot: diff from common ancestor to current HEAD.
		// Note: we can't use `--` to separate the ref from positional
		// args here because `<ref>...HEAD` is itself a single ref-spec
		// argument, not a path. ValidateRef above is the defense.
		return []string{"diff", ctx, ref + "...HEAD"}, nil
	default:
		return nil, fmt.Errorf("unknown mode %d", mode)
	}
}

// ValidateRef rejects user-supplied git refs that could be parsed by
// git as command-line flags rather than refs. A ref starting with `-`
// (e.g., `--output=/tmp/xyz`, `-c core.editor=...`) would be treated
// as a flag in `git diff <ref>...HEAD` or `git show <ref>` despite
// looking like a positional argument, so we refuse anything that
// shape rather than relying on a `--` separator that doesn't apply
// uniformly across git subcommands.
//
// We also reject refs containing newlines or NUL bytes because they
// can't survive a shell pipeline correctly and almost always indicate
// caller bugs (or attempted injection from a wrapping script).
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("ref is empty")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid ref %q: refs starting with '-' are rejected to prevent flag injection", ref)
	}
	if strings.ContainsAny(ref, "\x00\n\r") {
		return fmt.Errorf("invalid ref %q: contains control characters", ref)
	}
	return nil
}

// parseUnifiedDiff walks `git diff` output and groups hunks by file.
// We don't need a fully-featured patch library — just enough to feed
// content + line numbers to the LLM.
//
// Streams via bufio.Reader so a large diff doesn't double-buffer
// (legacy: stdout.String() + strings.Split slice). Each hunk's body
// builds in a strings.Builder so even a single huge hunk doesn't go
// quadratic on the content concatenation.
//
// We use Reader.ReadString('\n') rather than Scanner because Scanner
// caps line size (we'd previously bumped to 4 MB, but a single
// minified-bundle line in a vendored blob can easily exceed that and
// would trip ErrTooLong, aborting the entire review even if globs
// would have excluded that file). Reader has no per-line limit —
// memory grows with the longest single line, which is the same
// memory the input string already used in the legacy path.
//
// Returns an error when read itself fails (underlying I/O error).
// Caller MUST treat that as fail-closed: a partial diff would let the
// review gate exit 0 on what's materially an incomplete read of the
// change set.
func parseUnifiedDiff(r io.Reader) ([]Diff, error) {
	br := bufio.NewReader(r)
	var s diffParseState

	for {
		raw, readErr := br.ReadString('\n')
		// ReadString returns the partial line + io.EOF when input
		// ends mid-line (no trailing newline). Process the line we
		// have, then break on EOF; only non-EOF errors are fatal.
		if len(raw) > 0 {
			// Strip trailing \n then \r so CRLF input doesn't leak
			// \r into paths or hunk content. (Same job the old
			// strings.ReplaceAll did, done per-line during streaming.)
			line := strings.TrimRight(strings.TrimSuffix(raw, "\n"), "\r")
			s.processLine(line)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, fmt.Errorf("scan diff: %w", readErr)
		}
	}
	s.flushFile()

	// Drop any malformed entries (no path means we never saw a file header)
	out := s.diffs[:0]
	for _, d := range s.diffs {
		if d.Path != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

// diffParseState is parseUnifiedDiff's streaming accumulator: the diffs
// built so far, the file/hunk currently being accumulated, and the current
// hunk's body text.
type diffParseState struct {
	diffs    []Diff
	cur      *Diff
	hunk     *Hunk
	hunkBody strings.Builder
}

func (s *diffParseState) flushHunk() {
	if s.cur != nil && s.hunk != nil {
		s.hunk.Content = s.hunkBody.String()
		s.cur.Hunks = append(s.cur.Hunks, *s.hunk)
	}
	s.hunk = nil
	s.hunkBody.Reset()
}

func (s *diffParseState) flushFile() {
	s.flushHunk()
	if s.cur != nil {
		s.diffs = append(s.diffs, *s.cur)
	}
	s.cur = nil
}

// processLine classifies one already-newline-stripped diff line and
// updates state accordingly.
//
// Case order matters here — once we're inside a hunk (post-@@), every
// line is content even if it happens to start with `--- a/` or `+++ b/`.
// A deleted SQL comment `-- a/users` renders as `--- a/users` in the diff
// (the leading `-` is the diff prefix), and the previous case order
// matched that as a file header, silently overwriting cur.Path AND
// swallowing the line from the hunk content. Putting `hunk != nil` ahead
// of the header cases makes header recognition pre-@@ only.
func (s *diffParseState) processLine(line string) {
	switch {
	case strings.HasPrefix(line, "diff --git"):
		s.flushFile()
		s.cur = &Diff{}
	case strings.HasPrefix(line, "@@"):
		s.flushHunk()
		s.hunk = &Hunk{Header: line, NewFrom: parseNewFrom(line)}
	case s.hunk != nil:
		// Inside a hunk → all lines are content, regardless of
		// what they look like. See the "Case order matters" comment
		// above for why this can't move below the header cases.
		s.hunkBody.WriteString(line)
		s.hunkBody.WriteByte('\n')
	case strings.HasPrefix(line, "--- a/"):
		// Capture the original path as a fallback for deletions —
		// `+++ /dev/null` is the standard `git diff` shape for a
		// deleted file, so reading +++ alone would attribute every
		// deletion to "/dev/null" and silently break filtering,
		// finding attribution, and any path-based downstream logic.
		if s.cur != nil {
			s.cur.Path = strings.TrimPrefix(line, "--- a/")
		}
	case strings.HasPrefix(line, "+++ b/"):
		if s.cur != nil {
			s.cur.Path = strings.TrimPrefix(line, "+++ b/")
		}
	case strings.HasPrefix(line, "+++ ") && s.cur != nil && s.cur.Path == "":
		// Fallback for unusual paths (e.g. patches with non-standard
		// prefixes). Skip the "+++ /dev/null" deletion shape — we
		// already captured the original path from --- above.
		suffix := strings.TrimPrefix(line, "+++ ")
		if suffix != "/dev/null" {
			s.cur.Path = suffix
		}
	}
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
// gitRevParse is the git subcommand used by the short-lived ref helpers below.
const gitRevParse = "rev-parse"

func CurrentCommit() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", gitRevParse, "--short", "HEAD")
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

	cmd := exec.CommandContext(ctx, "git", gitRevParse, "--abbrev-ref", "HEAD")
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
// Returns the first 7 characters of the commit hash, or empty string
// if resolution fails / times out / the ref is rejected by ValidateRef
// (refs starting with `-` would otherwise be parsed by git as flags).
func ResolveRef(ref string) string {
	if err := ValidateRef(ref); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// `--verify` (not `--`) is the right flag-injection defense for
	// rev-parse: after `--`, rev-parse treats the argument as a
	// PATHSPEC, so it failed with "Needed a single revision" for
	// EVERY input — the v0.6.0 `--` hardening broke `local-review
	// commit <rev>` for all refs (including HEAD) and it survived
	// ~11 releases because nothing exercised this function against
	// real git. `--verify` requires the argument to resolve to a
	// single object, and the `^{commit}` suffix requires that object
	// to peel to a commit — annotated tags resolve to their target,
	// a tree/blob ref fails cleanly. ValidateRef stays as the first
	// line of defense against `-`-prefixed arguments.
	cmd := exec.CommandContext(ctx, "git", gitRevParse, "--short", "--verify", ref+"^{commit}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
