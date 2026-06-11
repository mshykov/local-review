package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInsideDir_Lexical(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(base, "b.md"), true},
		{filepath.Join(base, "sub", "b.md"), true},
		{base, true},
		{filepath.Join(base, "..", "b.md"), false},         // walks out via ..
		{filepath.Join(filepath.Dir(base), "b.md"), false}, // sibling of base
	}
	for _, tc := range cases {
		if got := InsideDir(tc.path, base); got != tc.want {
			t.Errorf("InsideDir(%q, %q) = %v, want %v", tc.path, base, got, tc.want)
		}
	}
}

// TestInsideDir_RejectsSymlinkEscape pins the security-critical behavior: a
// path lexically inside dir but resolving (via symlink) OUTSIDE it must be
// rejected — a lexical-only check leaked arbitrary files (v0.17.1).
func TestInsideDir_RejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(base, "go.md")
	if err := os.Symlink(secret, escape); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if InsideDir(escape, base) {
		t.Error("InsideDir admitted a symlink escaping the dir — arbitrary-file-disclosure vector")
	}

	// A symlink that stays inside the dir is legitimate.
	target := filepath.Join(base, "real.md")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(base, "py.md")
	if err := os.Symlink(target, inner); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !InsideDir(inner, base) {
		t.Error("InsideDir wrongly rejected a symlink that stays inside the dir")
	}
}
