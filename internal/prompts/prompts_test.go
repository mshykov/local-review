package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetKnown(t *testing.T) {
	cases := []string{"default", "typescript", "go", "python", "rust"}
	for _, lang := range cases {
		body, err := Get(lang)
		if err != nil {
			t.Errorf("Get(%q) error: %v", lang, err)
			continue
		}
		if !strings.Contains(body, "review") && !strings.Contains(body, "Review") {
			t.Errorf("Get(%q) doesn't mention review — empty pack?", lang)
		}
	}
}

func TestGetUnknownFallsBack(t *testing.T) {
	body, err := Get("haskell")
	if err != nil {
		t.Fatalf("Get(unknown) should fall back, got error: %v", err)
	}
	def, _ := Get("default")
	if body != def {
		t.Error("unknown language did not fall back to default pack")
	}
}

func TestAvailable(t *testing.T) {
	ids, err := Available()
	if err != nil {
		t.Fatal(err)
	}
	// Floor matches the current pack count (default + typescript + go +
	// python + rust). Bump this when adding a pack so a regression that
	// silently drops one is caught.
	if len(ids) < 5 {
		t.Errorf("expected ≥5 packs, got %d: %v", len(ids), ids)
	}
}

// --- v0.8 / issue #55: Resolve with PackDir / Prepend / Append ----

func TestResolve_NoOpts_MatchesGet(t *testing.T) {
	// Empty ResolveOptions must produce the same content as Get().
	// Backward-compat anchor: existing callers that switched from
	// Get to Resolve(_, ResolveOptions{}) should not see any
	// behavioural drift.
	for _, lang := range []string{"default", "go", "typescript", "python", "rust"} {
		want, err := Get(lang)
		if err != nil {
			t.Fatalf("Get(%q): %v", lang, err)
		}
		got, err := Resolve(lang, ResolveOptions{})
		if err != nil {
			t.Fatalf("Resolve(%q, {}): %v", lang, err)
		}
		if got.Content != want {
			t.Errorf("Resolve(%q, {}) drifted from Get", lang)
		}
		if got.Source != "embedded" {
			t.Errorf("Resolve(%q, {}).Source = %q, want %q", lang, got.Source, "embedded")
		}
	}
}

func TestResolve_PackDirOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	override := "OVERRIDE PACK BODY — review with the team's house rules.\n"
	if err := os.WriteFile(filepath.Join(dir, "go.md"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve("go", ResolveOptions{PackDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Content != override {
		t.Errorf("override file should fully replace embedded pack; got first 60 chars: %q", got.Content[:min(60, len(got.Content))])
	}
	// Source label should carry the absolute path so users can see
	// which file is in effect (issue #55 acceptance criteria).
	if !strings.HasSuffix(got.Source, "go.md") {
		t.Errorf("Source = %q, want path ending in go.md", got.Source)
	}
}

func TestResolve_PackDirMissingFileFallsThrough(t *testing.T) {
	// PackDir is set but doesn't have go.md → fall through to
	// embedded. This is the documented behaviour: missing files
	// don't error, they fall back. (Per-language overrides are
	// opt-in by file.)
	dir := t.TempDir()
	got, err := Resolve("go", ResolveOptions{PackDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	embedded, _ := Get("go")
	if got.Content != embedded {
		t.Error("missing override file should fall through to embedded pack")
	}
	if got.Source != "embedded" {
		t.Errorf("Source = %q, want %q (no override file present)", got.Source, "embedded")
	}
}

func TestResolve_EmptyOverrideFileFallsThrough(t *testing.T) {
	// Empty (or whitespace-only) override file falls through to
	// embedded. Without this guard, an accidentally-empty go.md
	// would silently neuter the entire system prompt — the worst
	// possible failure mode for a review tool.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.md"), []byte("   \n\n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("go", ResolveOptions{PackDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Source != "embedded" {
		t.Errorf("empty override should fall through; Source = %q", got.Source)
	}
	if !strings.Contains(got.Content, "review") && !strings.Contains(got.Content, "Review") {
		t.Error("fallback content doesn't look like the embedded pack")
	}
}

func TestResolve_Prepend(t *testing.T) {
	got, err := Resolve("go", ResolveOptions{Prepend: "House rule: always reject TODO comments."})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasPrefix(got.Content, "House rule:") {
		t.Errorf("prepend text not at the start; first 60 chars: %q", got.Content[:min(60, len(got.Content))])
	}
	embedded, _ := Get("go")
	if !strings.Contains(got.Content, embedded) {
		t.Error("embedded pack body should still be present after prepend")
	}
	if !strings.Contains(got.Source, "prepend") {
		t.Errorf("Source should reflect prepend; got %q", got.Source)
	}
}

func TestResolve_Append(t *testing.T) {
	got, err := Resolve("go", ResolveOptions{Append: "Output language: English only."})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(got.Content), "Output language: English only.") {
		t.Errorf("append text not at the end; last 80 chars: %q", got.Content[max(0, len(got.Content)-80):])
	}
	if !strings.Contains(got.Source, "append") {
		t.Errorf("Source should reflect append; got %q", got.Source)
	}
}

func TestResolve_PackDirPlusPrependPlusAppend(t *testing.T) {
	// All three layers compose: override file body, with house-rule
	// prepend before and output-shape append after.
	dir := t.TempDir()
	override := "OVERRIDE BODY"
	if err := os.WriteFile(filepath.Join(dir, "go.md"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve("go", ResolveOptions{
		PackDir: dir,
		Prepend: "BEFORE",
		Append:  "AFTER",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Order must be BEFORE → override → AFTER.
	idxBefore := strings.Index(got.Content, "BEFORE")
	idxBody := strings.Index(got.Content, "OVERRIDE BODY")
	idxAfter := strings.Index(got.Content, "AFTER")
	if idxBefore < 0 || idxBody < 0 || idxAfter < 0 {
		t.Fatalf("missing one of the three sections: %q", got.Content)
	}
	if !(idxBefore < idxBody && idxBody < idxAfter) {
		t.Errorf("section order wrong: BEFORE=%d BODY=%d AFTER=%d", idxBefore, idxBody, idxAfter)
	}
	for _, want := range []string{"go.md", "prepend", "append"} {
		if !strings.Contains(got.Source, want) {
			t.Errorf("Source missing %q: %q", want, got.Source)
		}
	}
}

func TestResolve_RejectsLanguageWithPathTraversal(t *testing.T) {
	// Critical security check: a hostile repo config like
	// `review.prompt_pack: ../../etc/passwd` must be refused
	// outright. Without this gate, the resolver would build
	// <pack_dir>/../../etc/passwd.md and load whatever was at
	// that location into the system prompt — which then flows to
	// the LLM and can come back in the model's review output.
	// Threat model: CI runner checking out an attacker-controlled
	// commit. (Codex flagged this in PR #60 self-review iter 3.)
	bad := []string{
		"../../etc/passwd",
		"../escape",
		"/absolute/path",
		"with/slash",
		`with\backslash`,
		"..",
		".",
		".hidden",
		"UPPERCASE",
		"with space",
		"with.dot", // dot in middle would let `lang.md` become `lang.md.md` resolution variants
		"",
	}
	for _, lang := range bad {
		_, err := Resolve(lang, ResolveOptions{})
		if err == nil {
			t.Errorf("Resolve(%q) should be refused as invalid language id", lang)
		}
	}
}

func TestResolve_AcceptsValidLanguageIDs(t *testing.T) {
	// Every shipped pack id (from Available()) must pass the
	// language-id validation gate. Adding a new pack must keep
	// this happy.
	ids, err := Available()
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range ids {
		if _, err := Resolve(lang, ResolveOptions{}); err != nil {
			t.Errorf("Resolve(%q) on a shipped pack should not be refused: %v", lang, err)
		}
	}
	// Also a few user-style custom ids that the validation should
	// permit (people do invent house-style packs).
	for _, lang := range []string{"acme", "acme-house", "house_v2", "a1"} {
		if _, err := Resolve(lang, ResolveOptions{}); err != nil {
			t.Errorf("Resolve(%q) on a custom-but-valid id should not be refused: %v", lang, err)
		}
	}
}

func TestPathInsideDir(t *testing.T) {
	// Build OS-native paths via filepath.Join + a temp-dir base so
	// the test passes on Windows too — codex flagged the prior
	// hardcoded `/a` paths as Unix-only. The temp-dir base is
	// real (so filepath.Clean has something canonical to work
	// with) but doesn't need to contain real files; pathInsideDir
	// is a pure lexical check.
	base := t.TempDir()
	cases := []struct {
		dir, path string
		want      bool
	}{
		{base, filepath.Join(base, "b.md"), true},
		{base, filepath.Join(base, "sub", "b.md"), true},
		{base, base, true},
		{base, filepath.Join(base, "..", "b.md"), false},         // walks out via ..
		{base, filepath.Join(filepath.Dir(base), "b.md"), false}, // sibling-of-base, outside
	}
	for _, tc := range cases {
		if got := pathInsideDir(tc.path, tc.dir); got != tc.want {
			t.Errorf("pathInsideDir(%q, %q) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}

func TestResolve_PackDirRejectsNonExistent(t *testing.T) {
	// Resolve does NOT error on a missing PackDir — that's
	// surfaced by `local-review doctor` instead. The resolver's
	// contract is "fall through cleanly so a typo'd path doesn't
	// kill every review."
	got, err := Resolve("go", ResolveOptions{PackDir: "/this/path/definitely/does/not/exist"})
	if err != nil {
		t.Fatalf("Resolve should fall through silently on missing dir, got error: %v", err)
	}
	if got.Source != "embedded" {
		t.Errorf("missing PackDir should resolve to embedded; got %q", got.Source)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
