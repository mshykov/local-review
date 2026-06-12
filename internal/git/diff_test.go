package git

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtract_StagedDiffAndSizeCap exercises the real git path: a staged
// change is extracted, and a shrunk maxDiffBytes trips the fail-closed cap
// (which also proves the overflow path doesn't hang on the full pipe).
func TestExtract_StagedDiffAndSizeCap(t *testing.T) {
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
	f := filepath.Join(repo, "f.txt")
	if err := os.WriteFile(f, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "f.txt")
	runGit("commit", "-q", "-m", "init")
	if err := os.WriteFile(f, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "f.txt")

	// Extract shells out to git in CWD; chdir into the repo (Go 1.23 has
	// no t.Chdir, so restore manually).
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	diffs, err := Extract(context.Background(), ModeStaged, "")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(diffs) == 0 {
		t.Fatal("expected at least one staged diff")
	}

	// Shrink the cap so any real diff overflows; must fail closed, not hang.
	old := maxDiffBytes
	maxDiffBytes = 1
	defer func() { maxDiffBytes = old }()
	if _, err := Extract(context.Background(), ModeStaged, ""); err == nil {
		t.Error("expected a size-cap error with maxDiffBytes=1, got nil")
	} else if !strings.Contains(err.Error(), "cap") {
		t.Errorf("expected a cap error, got: %v", err)
	}
}

// errReader returns r.pre on the first read, then r.err on every
// subsequent read. Used to simulate a real I/O failure mid-stream so
// the parser's fail-closed contract is exercised end-to-end.
type errReader struct {
	pre []byte
	err error
}

func (r *errReader) Read(p []byte) (int, error) {
	if len(r.pre) > 0 {
		n := copy(p, r.pre)
		r.pre = r.pre[n:]
		return n, nil
	}
	return 0, r.err
}

const sample = `diff --git a/src/foo.ts b/src/foo.ts
index 1234567..abcdefg 100644
--- a/src/foo.ts
+++ b/src/foo.ts
@@ -10,5 +10,7 @@
 const a = 1;
-const b = 2;
+const b = 3;
+const c = 4;
 const d = 5;
diff --git a/main.go b/main.go
index 222..333 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
`

func TestParseUnifiedDiff(t *testing.T) {
	diffs, err := parseUnifiedDiff(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parseUnifiedDiff(sample): %v", err)
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 files, got %d", len(diffs))
	}
	if diffs[0].Path != "src/foo.ts" {
		t.Errorf("file 0 path = %q", diffs[0].Path)
	}
	if len(diffs[0].Hunks) != 1 {
		t.Errorf("file 0 hunks = %d, want 1", len(diffs[0].Hunks))
	}
	if diffs[0].Hunks[0].NewFrom != 10 {
		t.Errorf("file 0 hunk NewFrom = %d, want 10", diffs[0].Hunks[0].NewFrom)
	}
	if diffs[1].Path != "main.go" {
		t.Errorf("file 1 path = %q", diffs[1].Path)
	}
}

func TestParseUnifiedDiff_CRLF(t *testing.T) {
	// Same diff but with CRLF line endings — paths must come back without \r.
	crlf := "diff --git a/foo.txt b/foo.txt\r\n" +
		"index 1..2 100644\r\n" +
		"--- a/foo.txt\r\n" +
		"+++ b/foo.txt\r\n" +
		"@@ -1,1 +1,2 @@\r\n" +
		" hello\r\n" +
		"+world\r\n"
	diffs, err := parseUnifiedDiff(strings.NewReader(crlf))
	if err != nil {
		t.Fatalf("parseUnifiedDiff(crlf): %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 file, got %d", len(diffs))
	}
	if diffs[0].Path != "foo.txt" {
		t.Errorf("path = %q, want %q", diffs[0].Path, "foo.txt")
	}
	if len(diffs[0].Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(diffs[0].Hunks))
	}
	if strings.Contains(diffs[0].Hunks[0].Content, "\r") {
		t.Errorf("hunk content contains \\r: %q", diffs[0].Hunks[0].Content)
	}
}

func TestParseUnifiedDiff_DeletedFileKeepsOriginalPath(t *testing.T) {
	// `git diff` emits `+++ /dev/null` when a file is deleted. Pre-fix
	// the parser took the path from +++, so deletions were attributed
	// to "/dev/null" — silently broke include/exclude filtering, the
	// per-file finding output, and any path-based downstream logic.
	// Now we capture --- a/<path> first and only let +++ b/<path>
	// overwrite it for non-deletion diffs.
	deleted := `diff --git a/legacy/old.go b/legacy/old.go
deleted file mode 100644
index abc..0000000
--- a/legacy/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package legacy
-
-func Old() {}
`
	diffs, err := parseUnifiedDiff(strings.NewReader(deleted))
	if err != nil {
		t.Fatalf("parseUnifiedDiff(deleted): %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d", len(diffs))
	}
	if diffs[0].Path != "legacy/old.go" {
		t.Errorf("deleted-file path = %q, want %q (was '/dev/null' pre-fix)", diffs[0].Path, "legacy/old.go")
	}
}

func TestParseUnifiedDiff_HunkContentLooksLikeHeader(t *testing.T) {
	// A deleted SQL comment `-- a/users` (or any code where a deleted
	// line happens to start with `-- a/` or an added line with `++ b/`)
	// renders inside a hunk as `--- a/users` / `+++ b/users` after the
	// diff's leading `-` / `+`. Pre-fix, the parser matched those as
	// file headers and BOTH overwrote cur.Path AND lost the line from
	// the hunk content. Pin the corrected behavior:
	//   - cur.Path stays attributed to the real file (migrations.sql)
	//   - the deleted/added lines remain in the hunk content verbatim
	sql := `diff --git a/migrations.sql b/migrations.sql
index 1..2 100644
--- a/migrations.sql
+++ b/migrations.sql
@@ -1,4 +1,4 @@
 SELECT 1;
--- a/users is the legacy table
-DROP TABLE users;
+++ b/customers replaces it
+DROP TABLE customers;
`
	diffs, err := parseUnifiedDiff(strings.NewReader(sql))
	if err != nil {
		t.Fatalf("parseUnifiedDiff(sql): %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d", len(diffs))
	}
	if diffs[0].Path != "migrations.sql" {
		t.Errorf("path = %q, want migrations.sql (header-look-alike inside hunk leaked into Path)", diffs[0].Path)
	}
	if len(diffs[0].Hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(diffs[0].Hunks))
	}
	body := diffs[0].Hunks[0].Content
	for _, want := range []string{"-- a/users is the legacy table", "++ b/customers replaces it"} {
		if !strings.Contains(body, want) {
			t.Errorf("hunk content missing %q (was eaten by header recognition)\n--- got ---\n%s", want, body)
		}
	}
}

func TestParseUnifiedDiff_RenameUsesNewPath(t *testing.T) {
	// Renames should attribute to the new path — that's what reviewers
	// will navigate to. This pins the existing behavior so the
	// deleted-file fix doesn't accidentally invert it.
	renamed := `diff --git a/old.go b/new.go
similarity index 80%
rename from old.go
rename to new.go
index aaa..bbb 100644
--- a/old.go
+++ b/new.go
@@ -1,3 +1,4 @@
 package x
+// new comment
 func F() {}
`
	diffs, err := parseUnifiedDiff(strings.NewReader(renamed))
	if err != nil {
		t.Fatalf("parseUnifiedDiff(renamed): %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d", len(diffs))
	}
	if diffs[0].Path != "new.go" {
		t.Errorf("renamed-file path = %q, want %q", diffs[0].Path, "new.go")
	}
}

func TestParseUnifiedDiff_FailsClosedOnScannerError(t *testing.T) {
	// A partial diff must NOT silently produce "no findings". The gate
	// downstream relies on having seen the whole change set; a truncated
	// read with the unscanned tail dropped would let a blocking change
	// slip through. Pin: scanner error → returned err, not partial slice.
	r := &errReader{
		pre: []byte("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n"),
		err: io.ErrUnexpectedEOF,
	}
	_, err := parseUnifiedDiff(r)
	if err == nil {
		t.Fatal("want error from parseUnifiedDiff on truncated input, got nil")
	}
	if !strings.Contains(err.Error(), "scan diff") {
		t.Errorf("error should mention scan diff, got: %v", err)
	}
}

func TestParseNewFrom(t *testing.T) {
	cases := map[string]int{
		"@@ -1,2 +37,5 @@":  37,
		"@@ -0,0 +1 @@":     1,
		"@@ -10,5 +10,7 @@": 10,
		"garbage":           0,
	}
	for header, want := range cases {
		if got := parseNewFrom(header); got != want {
			t.Errorf("parseNewFrom(%q) = %d, want %d", header, got, want)
		}
	}
}

func TestArgsFor(t *testing.T) {
	tests := []struct {
		mode Mode
		ref  string
		want []string
	}{
		{ModeStaged, "", []string{"diff", "--cached", "-U10"}},
		{ModeCommit, "abc123", []string{"show", "-U10", "--format=", "abc123"}},
		{ModeCommit, "", []string{"show", "-U10", "--format=", "HEAD"}},
		{ModeBranch, "main", []string{"diff", "-U10", "main...HEAD"}},
		{ModeBranch, "", []string{"diff", "-U10", "main...HEAD"}},
	}
	for _, tc := range tests {
		got, err := argsFor(tc.mode, tc.ref)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != len(tc.want) {
			t.Errorf("argsFor(%d, %q) = %v, want %v", tc.mode, tc.ref, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("argsFor(%d, %q)[%d] = %q, want %q", tc.mode, tc.ref, i, got[i], tc.want[i])
			}
		}
	}
}

func TestCurrentCommit(t *testing.T) {
	// This test runs in a git repo, so it should return a commit hash
	commit := CurrentCommit()
	if commit == "" {
		t.Error("CurrentCommit() returned empty string")
	}
	// Commit hash should be reasonable length (short form is typically 7-8 chars)
	if len(commit) < 7 || len(commit) > 40 {
		t.Errorf("CurrentCommit() = %q, unexpected length", commit)
	}
}

func TestCurrentBranch(t *testing.T) {
	// This test runs in a git repo, so it should return a branch name
	branch := CurrentBranch()
	if branch == "" {
		t.Error("CurrentBranch() returned empty string")
	}
}

func TestValidateRef(t *testing.T) {
	cases := []struct {
		ref     string
		wantErr bool
	}{
		{"main", false},
		{"feature/auth-fix", false},
		{"v1.2.3", false},
		{"abc1234", false},
		{"HEAD", false},
		{"", true},
		{"--output=/tmp/xyz", true},    // flag injection
		{"-c", true},                   // flag injection
		{"--upload-pack=/tmp/x", true}, // git-specific flag injection
		{"main\nmalice", true},         // newline injection
		{"main\x00main", true},         // NUL injection
	}
	for _, tc := range cases {
		err := ValidateRef(tc.ref)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateRef(%q) err=%v, wantErr=%v", tc.ref, err, tc.wantErr)
		}
	}
}

func TestArgsFor_RejectsFlagShapedRef(t *testing.T) {
	// argsFor must refuse to construct a `git diff` / `git show` that
	// would interpret a user-controlled ref as a flag. Defends against
	// `git diff --output=/tmp/x...HEAD` — `...HEAD` makes it look
	// positional but git still parses the leading `--` as a flag.
	if _, err := argsFor(ModeBranch, "--output=/tmp/x"); err == nil {
		t.Error("ModeBranch with flag-shaped ref: want error, got nil")
	}
	if _, err := argsFor(ModeCommit, "-c"); err == nil {
		t.Error("ModeCommit with flag-shaped ref: want error, got nil")
	}
	// Sanity: legitimate refs still pass through.
	if _, err := argsFor(ModeBranch, "main"); err != nil {
		t.Errorf("ModeBranch with 'main': want no error, got %v", err)
	}
	if _, err := argsFor(ModeCommit, "v1.2.3"); err != nil {
		t.Errorf("ModeCommit with 'v1.2.3': want no error, got %v", err)
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"main", "main"},
		{"feature/auth-fix", "feature-auth-fix"},
		{"bugfix/issue-123", "bugfix-issue-123"},
		{"release/v1.0.0", "release-v1.0.0"},
		{"feat/multi/level/branch", "feat-multi-level-branch"},
		{"windows\\path", "windows-path"},
	}

	for _, tt := range tests {
		got := SanitizeBranchName(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
