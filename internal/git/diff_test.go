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
