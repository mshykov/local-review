package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSplitNullBytes_EmptyInput covers the early-return path —
// an empty `git ls-files -z` stdout (no tracked files) should
// produce an empty slice, not a nil slice or a single empty
// string. Caller in audit.Walk relies on len() == 0 to detect
// the empty-repo case.
func TestSplitNullBytes_EmptyInput(t *testing.T) {
	got, err := splitNullBytes(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty input should produce empty slice; got %v", got)
	}
}

// TestSplitNullBytes_TrailingNullDropped covers the canonical
// `git ls-files -z` shape: tokens separated by NUL, with a
// trailing NUL at the end. The trailing NUL produces an empty
// final token which must be dropped — otherwise every TrackedFiles
// call would carry a phantom empty entry that downstream code
// would try to read as a path.
func TestSplitNullBytes_TrailingNullDropped(t *testing.T) {
	in := []byte("a\x00b\x00c\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("expected [a b c]; got %v", got)
	}
}

// TestSplitNullBytes_MissingFinalNull covers the defensive case:
// some git versions omit the final NUL on the last record. The
// scanner's at-EOF branch must yield the last token correctly.
func TestSplitNullBytes_MissingFinalNull(t *testing.T) {
	in := []byte("a\x00b\x00c") // no trailing NUL
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[2] != "c" {
		t.Errorf("expected last token preserved; got %v", got)
	}
}

// TestSplitNullBytes_PreservesPathsWithNewlines is the whole
// point of using -z mode. A path like "foo\nbar.go" must
// round-trip as one token, not split across two as it would with
// newline-separated parsing.
func TestSplitNullBytes_PreservesPathsWithNewlines(t *testing.T) {
	in := []byte("normal.go\x00foo\nbar.go\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens; got %d: %v", len(got), got)
	}
	if got[1] != "foo\nbar.go" {
		t.Errorf("newline-bearing path corrupted: got %q", got[1])
	}
}

// TestSplitNullBytes_MultipleConsecutiveNullsSkippedAsEmpty
// confirms that empty tokens (NUL\x00NUL\x00) don't sneak into
// the output as empty strings. `git ls-files -z` doesn't emit
// these in practice but the parser stays defensive.
func TestSplitNullBytes_MultipleConsecutiveNullsSkippedAsEmpty(t *testing.T) {
	in := []byte("a\x00\x00b\x00")
	got, err := splitNullBytes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected empty tokens dropped; got %v", got)
	}
}

// initTestRepo creates a temp git repo with two committed files (one in a
// subdirectory) and one uncommitted file, mirroring diff_test.go's inline
// setup. Returns the repo root (symlink-resolved, since macOS TempDir lives
// under /var → /private/var and RepoRoot returns git's resolved path).
func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@example.com")
	runGit("config", "user.name", "T")
	runGit("config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"top.go", "pkg/nested.go"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "init")
	// Untracked file: must NOT appear in TrackedFiles output.
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestTrackedFiles_ListsCommittedFilesRepoRootRelative pins the audit
// enumeration contract: repo-root-relative paths, tracked files only
// (untracked and gitignored files are exactly what audit must not scan),
// and repo-wide results even when invoked with an explicit root while the
// process CWD is elsewhere — the PR #73 CWD bug this function exists to
// prevent.
func TestTrackedFiles_ListsCommittedFilesRepoRootRelative(t *testing.T) {
	repo := initTestRepo(t)

	files, err := TrackedFiles(repo)
	if err != nil {
		t.Fatalf("TrackedFiles: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	if !got["top.go"] || !got["pkg/nested.go"] {
		t.Errorf("expected top.go and pkg/nested.go in tracked files, got %v", files)
	}
	if got["untracked.txt"] {
		t.Errorf("untracked.txt must not be listed, got %v", files)
	}
}

// TestTrackedFiles_EmptyRootResolvesViaRepoRoot covers the root==""
// fallback (resolve CWD's repo root once) and RepoRoot itself against a
// real repo. Chdir is process-global — same no-t.Parallel() caveat as
// TestExtract_StagedDiffAndSizeCap.
func TestTrackedFiles_EmptyRootResolvesViaRepoRoot(t *testing.T) {
	repo := initTestRepo(t)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Run from a SUBDIRECTORY: proves both RepoRoot's walk-up and
	// TrackedFiles' repo-wide `:/` pathspec behave from a non-root CWD.
	if err := os.Chdir(filepath.Join(repo, "pkg")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if root != repo {
		t.Errorf("RepoRoot = %q, want %q", root, repo)
	}

	files, err := TrackedFiles("")
	if err != nil {
		t.Fatalf("TrackedFiles(\"\"): %v", err)
	}
	found := false
	for _, f := range files {
		if f == "top.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected repo-wide listing (top.go) from subdir CWD, got %v", files)
	}
}

// TestRepoRoot_ErrorsOutsideWorkTree pins the fail-loud contract: outside
// any git repo, RepoRoot must surface git's non-zero exit as an error.
func TestRepoRoot_ErrorsOutsideWorkTree(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	// GIT_CEILING_DIRECTORIES can't help here since exec.Command inherits
	// our env; a plain TempDir is outside any repo on CI runners, but a
	// developer machine could nest tmp under a repo — guard against that.
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	if out, _ := cmd.CombinedOutput(); strings.TrimSpace(string(out)) == "true" {
		t.Skip("temp dir is inside a git work tree on this machine; cannot test the outside-repo path")
	}

	if _, err := RepoRoot(); err == nil {
		t.Error("expected an error outside a git work tree, got nil")
	}
}
