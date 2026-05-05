package git

import (
	"strings"
	"testing"
)

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
	diffs := parseUnifiedDiff(sample)
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
	diffs := parseUnifiedDiff(crlf)
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
	diffs := parseUnifiedDiff(deleted)
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d", len(diffs))
	}
	if diffs[0].Path != "legacy/old.go" {
		t.Errorf("deleted-file path = %q, want %q (was '/dev/null' pre-fix)", diffs[0].Path, "legacy/old.go")
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
	diffs := parseUnifiedDiff(renamed)
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff, got %d", len(diffs))
	}
	if diffs[0].Path != "new.go" {
		t.Errorf("renamed-file path = %q, want %q", diffs[0].Path, "new.go")
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
