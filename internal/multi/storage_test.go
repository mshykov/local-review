package multi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFilenameComponent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Trusted-source happy paths.
		{"plain claude", "claude", "claude"},
		{"plain version", "2.1.132", "2.1.132"},
		{"semver pre-release", "2.1.132-rc.1", "2.1.132-rc.1"},
		{"semver with build metadata", "2.1.132+build.42", "2.1.132+build.42"},

		// Defensive against future vendor weirdness — the actual
		// release-blocker the v0.7.2 audit caught.
		{"path separator forward", "claude/staging", "claude-staging"},
		{"path separator back", "codex\\rc", "codex-rc"},
		{"colon", "v2:rc", "v2-rc"},
		{"asterisk", "claude*", "claude"},
		{"NUL byte", "claude\x00bad", "claude-bad"},
		{"whitespace", "claude rc", "claude-rc"},
		{"multiple dangerous chars collapse", "claude//rc", "claude-rc"},
		{"leading dangerous trimmed", "/claude", "claude"},
		{"trailing dangerous trimmed", "claude/", "claude"},

		// Empty / fully-stripped inputs land on a stable placeholder
		// so filenames don't collapse to `<commit>__.md` (double
		// underscore) which is harder to glob/grep for than the
		// explicit "unknown" sentinel.
		{"empty", "", "unknown"},
		{"only dangerous chars", "///", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeFilenameComponent(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeFilenameComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSaveReview_SanitizesLLMNameAndVersion(t *testing.T) {
	// Defense-in-depth integration test: a vendor returning a
	// dangerous version string (path separators, NUL bytes, etc.)
	// must not produce a file outside the per-branch directory.
	dir := t.TempDir()
	storage := NewStorage(dir)

	// Pretend a vendor returned a banner with embedded forward
	// slashes — which previously would have created
	// `<commit>_codex_v0/staging.md` (a *subdirectory* relative
	// to the branch dir) and tried to write through nonexistent
	// dirs.
	path, err := storage.SaveReview("main", "abc123", "codex", "v0/staging", "content")
	if err != nil {
		t.Fatalf("SaveReview returned err: %v", err)
	}

	// Confirm path stays inside the branch directory and does
	// not contain any path-segment-introducing characters.
	wantParent := filepath.Join(dir, "main")
	if filepath.Dir(path) != wantParent {
		t.Errorf("review escaped branch dir: got parent %q, want %q", filepath.Dir(path), wantParent)
	}
	base := filepath.Base(path)
	if strings.ContainsAny(base, `/\:<>"|?*`) {
		t.Errorf("filename still contains dangerous chars: %q", base)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected saved file to exist: %v", err)
	}
}

func TestSaveReview_EmptyVersionDoesNotCollapseUnderscores(t *testing.T) {
	// If a version probe failed and we passed empty string through,
	// the filename should still be navigable. Pin "<commit>_<llm>_unknown.md"
	// rather than "<commit>_<llm>_.md" (which globs poorly and reads
	// like a typo).
	dir := t.TempDir()
	storage := NewStorage(dir)
	path, err := storage.SaveReview("main", "abc123", "claude", "", "content")
	if err != nil {
		t.Fatalf("SaveReview returned err: %v", err)
	}
	want := filepath.Join(dir, "main", "abc123_claude_unknown.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}
