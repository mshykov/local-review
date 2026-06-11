package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RequireJSON must inject the canonical findings schema for EVERY
// shipped pack — the single-LLM path parses JSON and the language
// packs no longer carry the schema themselves (pre-v0.12.1 they
// deferred to default.md, which Resolve never sent — the Ollama
// dogfood bug). The language set is derived from Available() so a
// newly added pack is automatically covered (a hardcoded list would
// silently skip it — the exact regression this test guards against).
func TestResolve_RequireJSON_InjectsSchemaForEveryLanguage(t *testing.T) {
	langs, err := Available()
	if err != nil {
		t.Fatalf("Available(): %v", err)
	}
	if len(langs) == 0 {
		t.Fatal("Available() returned no packs — embed broken?")
	}
	for _, lang := range langs {
		pack, err := Resolve(lang, ResolveOptions{RequireJSON: true})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", lang, err)
		}
		if !strings.Contains(pack.Content, FindingsJSONSchema) {
			t.Errorf("Resolve(%q, RequireJSON) missing the canonical findings schema", lang)
		}
	}
}

// Without RequireJSON (multi-LLM path), the schema must NOT be present
// — those invokers append a "respond in markdown, NOT JSON" override,
// and a competing JSON schema risks a stray JSON reply the merger
// can't consolidate. Asserts against the schema constant itself, not
// incidental wording, so unrelated pack edits don't flip this.
func TestResolve_NoRequireJSON_OmitsSchema(t *testing.T) {
	for _, lang := range []string{"default", "go"} {
		pack, err := Resolve(lang, ResolveOptions{})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", lang, err)
		}
		if strings.Contains(pack.Content, FindingsJSONSchema) {
			t.Errorf("Resolve(%q) without RequireJSON must not carry the JSON schema", lang)
		}
	}
}

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

// TestPathInsideDir_RejectsSymlinkEscape pins the security fix: a path that
// is lexically inside the pack dir but resolves (via symlink) OUTSIDE it
// must be rejected. A lexical-only check admitted `pack_dir/go.md ->
// /etc/passwd`, which os.ReadFile then leaked into the LLM prompt.
func TestPathInsideDir_RejectsSymlinkEscape(t *testing.T) {
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
	if pathInsideDir(escape, base) {
		t.Error("pathInsideDir admitted a symlink escaping the pack dir — arbitrary-file-disclosure vector")
	}

	// A symlink that stays inside the dir is legitimate and must pass.
	target := filepath.Join(base, "real.md")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(base, "py.md")
	if err := os.Symlink(target, inner); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !pathInsideDir(inner, base) {
		t.Error("pathInsideDir wrongly rejected a symlink that stays inside the pack dir")
	}
}

// TestResolve_SymlinkOverrideDoesNotLeak is the end-to-end proof: an
// override file that is a symlink to an out-of-tree file is NOT read into
// the prompt; Resolve falls through to the embedded pack.
func TestResolve_SymlinkOverrideDoesNotLeak(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "passwd")
	const sentinel = "SENTINEL-SECRET-MUST-NOT-LEAK"
	if err := os.WriteFile(secret, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(base, "go.md")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	got, err := Resolve("go", ResolveOptions{PackDir: base})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Contains(got.Content, sentinel) {
		t.Fatal("symlink override leaked an out-of-tree file into the prompt")
	}
	if got.Source != "embedded" {
		t.Errorf("escaping symlink override must fall through to embedded; Source = %q", got.Source)
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

// TestGetAuditPack_HappyPath verifies the two day-1 audit topics
// (security, tech-debt) load via embed.FS and contain the
// telltale section header. Locks the embed pathing in place so a
// future refactor that moves the audit packs accidentally surfaces
// here, not at runtime when a user tries `local-review audit`.
func TestGetAuditPack_HappyPath(t *testing.T) {
	for topic, mustContain := range map[string]string{
		"security":  "Security audit pack",
		"tech-debt": "Technical-debt audit pack",
	} {
		got, err := GetAuditPack(topic)
		if err != nil {
			t.Errorf("GetAuditPack(%q): unexpected error: %v", topic, err)
			continue
		}
		if !strings.Contains(got, mustContain) {
			t.Errorf("GetAuditPack(%q) missing %q in body (first 80 chars: %q)", topic, mustContain, got[:min(80, len(got))])
		}
	}
}

// TestGetAuditPack_EmptyTopicIsAnActionableError covers the
// required-argument contract: passing "" must produce an error
// that names the available topics so a CLI user sees the next step.
func TestGetAuditPack_EmptyTopicIsAnActionableError(t *testing.T) {
	_, err := GetAuditPack("")
	if err == nil {
		t.Fatal("expected error on empty topic; got nil")
	}
	if !strings.Contains(err.Error(), "audit topic is required") {
		t.Errorf("error should mention 'audit topic is required'; got %v", err)
	}
}

// TestGetAuditPack_UnknownTopicListsAvailable verifies the
// unknown-topic error path includes the available topics so users
// hitting a typo can self-correct without `--help`. Also pins the
// listing-fails fallback message structure since that was the
// subject of an earlier CLAUDE.md-rule-4 round of review.
func TestGetAuditPack_UnknownTopicListsAvailable(t *testing.T) {
	_, err := GetAuditPack("not-a-real-topic")
	if err == nil {
		t.Fatal("expected error on unknown topic; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should mention 'not found'; got %v", err)
	}
	for _, want := range []string{"security", "tech-debt"} {
		if !strings.Contains(msg, want) {
			t.Errorf("available-topics list should include %q; got %v", want, err)
		}
	}
}

// TestGetAuditPack_RejectsUnsafeTopicIDs covers the path-traversal
// guard (same languageIDRE the language packs use). A topic id
// like "../packs/python" would otherwise let a curious caller
// read sibling embed entries through the audit-pack API.
func TestGetAuditPack_RejectsUnsafeTopicIDs(t *testing.T) {
	for _, bad := range []string{"../packs/python", "foo/bar", ".secret", "with space"} {
		_, err := GetAuditPack(bad)
		if err == nil {
			t.Errorf("expected error on unsafe topic %q; got nil", bad)
		}
	}
}

// TestAvailableAuditTopics_ReturnsBothShippedTopics pins the
// runtime-discovery API: the loader walks the embedded `audit/`
// directory and surfaces every `<topic>.md` stem, sorted
// alphabetically. v1 ships {security, tech-debt}; future
// additions (drop-in `audit/architecture.md`) appear
// automatically.
func TestAvailableAuditTopics_ReturnsBothShippedTopics(t *testing.T) {
	topics, err := AvailableAuditTopics()
	if err != nil {
		t.Fatalf("AvailableAuditTopics: %v", err)
	}
	got := map[string]bool{}
	for _, tt := range topics {
		got[tt] = true
	}
	for _, want := range []string{"security", "tech-debt"} {
		if !got[want] {
			t.Errorf("AvailableAuditTopics missing %q; got %v", want, topics)
		}
	}
}
