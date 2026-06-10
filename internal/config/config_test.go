package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// v0 single-LLM TestDefaults removed in v0.15 — Defaults() no longer
// has a Provider struct to assert on. The multi-LLM equivalent below
// covers the same shape from the v0.1+ side.

func TestDefaults_MultiLLMModelsAreEmpty(t *testing.T) {
	// Multi-LLM defaults intentionally leave Model unset so each vendor
	// CLI uses its own current stable. Pinning model IDs in our defaults
	// (claude-3-5-sonnet-20241022, gemini-1.5-pro, gpt-4) was a v0.1
	// decision that aged into 12-24-month staleness by v0.6.x, with no
	// release cadence that justified the maintenance churn of bumping
	// them. If a future contributor adds a hardcoded model here, they
	// should make a deliberate call about how it'll be kept fresh; this
	// test fails loudly so the question gets asked.
	d := Defaults()
	for _, name := range []string{"claude", "gemini", "codex"} {
		llm, ok := d.LLMs[name]
		if !ok {
			t.Errorf("default LLM %q missing", name)
			continue
		}
		if llm.Model != "" {
			t.Errorf("default LLM %q has hardcoded Model=%q — should be empty so the CLI picks its own current default; if you really want a pinned ID, document the rotation cadence and update this test", name, llm.Model)
		}
	}
}

// TestMergeReplaces (v0 Provider variant) and TestLoadUsesRepoYAML
// (v0 single-LLM YAML shape) removed in v0.15. The Review-block
// merge contract is now covered by TestMergeReplaces_V01 below and
// the load + cascade tests by TestLoadUsesRepoYAML_V01.

func TestLoad_RejectsRemovedProviderBlock(t *testing.T) {
	// v0.15 removal: a YAML config that still contains the legacy
	// `provider:` block must surface an actionable migration error
	// rather than silently load (yaml.v3 ignores unknown fields).
	// The error text must point at the new shape so the fix is
	// obvious without grepping the CHANGELOG.
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
provider:
  base_url: http://localhost:11434/v1
  model: qwen
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(repoCfg)
	if err == nil {
		t.Fatal("expected Load to reject the legacy provider: block, got nil")
	}
	msg := err.Error()
	for _, must := range []string{"provider:", "removed in v0.15", "llms."} {
		if !strings.Contains(msg, must) {
			t.Errorf("error must mention %q so the migration path is obvious; got %q", must, msg)
		}
	}
}

func TestDetectRemovedProviderBlock(t *testing.T) {
	cases := map[string]bool{
		// Legacy shape — must trip the detector.
		"provider:\n  base_url: x\n":          true,
		"provider: {}\n":                      true,
		"provider:\n":                         true, // bare key, null value
		"review: {}\nprovider:\n  model: x\n": true,
		// New unified shape — must NOT trip.
		"llms:\n  ollama:\n    base_url: x\n": false,
		"review:\n  min_severity: warning\n":  false,
		"":                                    false,
	}
	for in, want := range cases {
		got, err := detectRemovedProviderBlock([]byte(in))
		if err != nil {
			t.Errorf("detectRemovedProviderBlock(%q) unexpected parse error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("detectRemovedProviderBlock(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDetectRemovedProviderBlock_YAMLMergeKeyDoesNotBypass(t *testing.T) {
	// v0.15 self-review (codex) caught the "hard-rejection bypass via
	// YAML merge keys" hole: a legacy `provider:` block reached
	// through `<<: *anchor` would slip past a node-tree walker that
	// only sees literal top-level keys. The detector must use a
	// merge-resolving decode (yaml.v3 does this for
	// map[string]interface{}) so the resulting top-level keyset
	// contains `provider` even when it arrived via an anchor.
	yamlWithMergeKey := `defaults: &legacy
  base_url: http://localhost:11434/v1
  model: qwen
provider:
  <<: *legacy
`
	got, err := detectRemovedProviderBlock([]byte(yamlWithMergeKey))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !got {
		t.Error("detector must catch provider: even when its body is supplied via a YAML merge key (`<<: *anchor`); silent bypass defeats the hard-rejection contract")
	}
}

func TestFindRepoConfig(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(cfg, []byte("llms: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindRepoConfig(deep)
	if got != cfg {
		t.Errorf("FindRepoConfig(deep) = %q, want %q", got, cfg)
	}
	if FindRepoConfig(t.TempDir()) != "" {
		t.Error("expected empty string when no config exists")
	}
}

// The v0 resolveAPIKey tests removed in v0.15 along with the function
// itself. The per-LLM resolveAPIKeys (multi-LLM path) is covered by
// TestResolveAPIKeys_EnvOverridesYAMLPerLLM and TestResolveAPIKeys_V01.

// TestResolveAPIKeys_EnvOverridesYAMLPerLLM is the multi-LLM
// counterpart of TestResolveAPIKey_EnvVarOverridesYAMLKey. The
// per-LLM resolver in resolveAPIKeys has the same precedence
// contract as the single-provider resolver.
func TestResolveAPIKeys_EnvOverridesYAMLPerLLM(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-claude")
	cfg := Defaults()
	// Simulate a YAML-loaded stale key on the claude LLM config.
	claudeCfg := cfg.LLMs["claude"]
	claudeCfg.APIKey = "sk-stale-yaml-claude"
	cfg.LLMs["claude"] = claudeCfg
	resolveAPIKeys(&cfg)
	if got := cfg.LLMs["claude"].APIKey; got != "sk-env-claude" {
		t.Errorf("claude api_key = %q, want sk-env-claude (env should win)", got)
	}
}

// v0.1 tests

func TestDefaults_V01(t *testing.T) {
	d := Defaults()

	// Check that LLMs are configured. copilot joined the defaults so
	// `merge.preferred_llm: copilot` validates without a hand-added
	// llms.copilot block (antigravity stays out — it's not a reviewer).
	if len(d.LLMs) != 4 {
		t.Errorf("expected 4 LLMs, got %d", len(d.LLMs))
	}

	// Check specific LLMs
	if _, ok := d.LLMs["claude"]; !ok {
		t.Error("claude LLM not in defaults")
	}
	if _, ok := d.LLMs["gemini"]; !ok {
		t.Error("gemini LLM not in defaults")
	}
	if _, ok := d.LLMs["codex"]; !ok {
		t.Error("codex LLM not in defaults")
	}
	if _, ok := d.LLMs["copilot"]; !ok {
		t.Error("copilot LLM not in defaults")
	}

	// Check merge config
	if d.Merge.PreferredLLM != "auto" {
		t.Errorf("merge.preferred_llm = %q, want auto", d.Merge.PreferredLLM)
	}
	if d.Merge.Deduplicate == nil || !*d.Merge.Deduplicate {
		t.Error("merge.deduplicate should be true by default")
	}

	// Check storage config
	if d.Storage.BasePath != ".local-review/reviews" {
		t.Errorf("storage.base_path = %q", d.Storage.BasePath)
	}
}

func TestMergeReplaces_V01(t *testing.T) {
	dst := Defaults()
	src := Config{
		LLMs: map[string]LLMConfig{
			"claude": {
				Enabled: boolPtr(false), // disable claude
				Model:   "claude-3-haiku",
			},
			"custom": {
				Enabled:    boolPtr(true),
				CLIPath:    "custom-llm",
				TimeoutSec: 60,
			},
		},
		Merge: MergeConfig{
			PreferredLLM: "gemini",
		},
		Storage: StorageConfig{
			BasePath: "/tmp/reviews",
		},
	}

	merge(&dst, src)

	// Check that claude was updated
	if dst.LLMs["claude"].Enabled != nil && *dst.LLMs["claude"].Enabled {
		t.Error("claude should be disabled after merge")
	}
	if dst.LLMs["claude"].Model != "claude-3-haiku" {
		t.Errorf("claude model = %q, want claude-3-haiku", dst.LLMs["claude"].Model)
	}

	// Check that custom LLM was added
	if _, ok := dst.LLMs["custom"]; !ok {
		t.Error("custom LLM should be added")
	}

	// Check merge config
	if dst.Merge.PreferredLLM != "gemini" {
		t.Errorf("merge.preferred_llm = %q", dst.Merge.PreferredLLM)
	}

	// Check storage config
	if dst.Storage.BasePath != "/tmp/reviews" {
		t.Errorf("storage.base_path = %q", dst.Storage.BasePath)
	}
}

func TestResolveAPIKeys_V01(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-claude-123")
	t.Setenv("OPENAI_API_KEY", "sk-openai-456")

	cfg := Defaults()
	resolveAPIKeys(&cfg)

	// Check that API keys were resolved
	if cfg.LLMs["claude"].APIKey != "sk-claude-123" {
		t.Errorf("claude api_key = %q, want sk-claude-123", cfg.LLMs["claude"].APIKey)
	}
	if cfg.LLMs["codex"].APIKey != "sk-openai-456" {
		t.Errorf("codex api_key = %q, want sk-openai-456", cfg.LLMs["codex"].APIKey)
	}
}

func TestLoadUsesRepoYAML_V01(t *testing.T) {
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
llms:
  claude:
    enabled: false
  gemini:
    model: gemini-1.5-flash
merge:
  preferred_llm: gemini
  deduplicate: false
storage:
  base_path: /custom/path
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Check that LLMs were merged
	if cfg.LLMs["claude"].Enabled != nil && *cfg.LLMs["claude"].Enabled {
		t.Error("claude should be disabled")
	}
	if cfg.LLMs["gemini"].Model != "gemini-1.5-flash" {
		t.Errorf("gemini model = %q", cfg.LLMs["gemini"].Model)
	}

	// Check merge config
	if cfg.Merge.PreferredLLM != "gemini" {
		t.Errorf("merge.preferred_llm = %q", cfg.Merge.PreferredLLM)
	}
	if cfg.Merge.Deduplicate != nil && *cfg.Merge.Deduplicate {
		t.Error("merge.deduplicate should be false")
	}

	// Check storage config
	if cfg.Storage.BasePath != "/custom/path" {
		t.Errorf("storage.base_path = %q", cfg.Storage.BasePath)
	}
}

func TestLoadUsesRepoYAML_PromptsPackDir_ResolvedRelativeToConfig(t *testing.T) {
	// Issue #55 / codex self-review iter-1: a relative `pack_dir`
	// in the YAML must resolve relative to the config file's
	// directory, not the user's CWD. Pre-fix, running
	// `local-review` from a subdirectory silently fell through to
	// embedded packs because the resolver looked at
	// <subdir>/.local-review/prompts instead of
	// <repo-root>/.local-review/prompts.
	repoRoot := t.TempDir()
	overrideDir := filepath.Join(repoRoot, ".local-review", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoCfg := filepath.Join(repoRoot, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: .local-review/prompts
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the user running from a subdirectory by chdir'ing
	// into one before Load. If the loader is correct, the
	// resolved PackDir should still point at <repoRoot>/.local-review/prompts.
	subdir := filepath.Join(repoRoot, "internal", "deep", "nested")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Restore CWD on test exit. Check the Chdir-back error: if we
	// can't get back to where we started, subsequent tests in the
	// same `go test` invocation could silently misbehave; better
	// to fail loudly here. (t.Chdir would be cleaner but it's
	// Go 1.24+; module is 1.23.)
	t.Cleanup(func() {
		if err := os.Chdir(origCwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(subdir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantAbs, _ := filepath.Abs(overrideDir)
	gotAbs, _ := filepath.Abs(cfg.Prompts.PackDir)
	if wantAbs != gotAbs {
		t.Errorf("PackDir = %q (abs %q), want abs %q\n— relative path was resolved against CWD instead of the config file directory",
			cfg.Prompts.PackDir, gotAbs, wantAbs)
	}
}

func TestLoadUsesRepoYAML_PromptsPackDir_AbsolutePathPreserved(t *testing.T) {
	// Absolute paths must pass through unchanged — only relative
	// paths get rewritten. A user with a corporate-shared prompt
	// dir at /etc/local-review/prompts should not have it
	// re-rooted.
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	// Use an OS-native absolute path (filepath.Abs of a temp
	// subdir) instead of hardcoding /etc/... — Windows CI runners
	// would fail on Unix-style absolute paths. Codex caught this
	// in PR #60 self-review iter 3.
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "corp-prompts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: `+abs+`
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Prompts.PackDir != abs {
		t.Errorf("PackDir = %q, want absolute %q (must pass through)", cfg.Prompts.PackDir, abs)
	}
}

// TestLoadUsesRepoYAML_PromptsPackDir_RejectsPathTraversal covers
// the v0.10.0 audit-dogfood finding: a YAML-supplied relative
// `pack_dir` that escapes the config directory via `..` segments
// MUST be rejected at load time. Previously the resolver happily
// joined `<config-dir>/../../../etc` and let the override-reader
// read whatever files happened to be in the resulting absolute
// location.
//
// Absolute paths still pass through (the TestLoadUsesRepoYAML_
// PromptsPackDir_AbsolutePathPreserved case directly above
// covers that explicit-opt-in shape).
func TestLoadUsesRepoYAML_PromptsPackDir_RejectsPathTraversal(t *testing.T) {
	for _, badPath := range []string{
		"../escape",
		"../../etc",
		"foo/../../escape",
		"a/b/c/../../../../etc",
	} {
		t.Run(badPath, func(t *testing.T) {
			repoCfg := filepath.Join(t.TempDir(), ".local-review.yml")
			if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: `+badPath+`
`), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(repoCfg)
			if err == nil {
				t.Fatalf("expected Load to reject pack_dir %q; got nil error", badPath)
			}
			if !strings.Contains(err.Error(), "escapes config directory") {
				t.Errorf("error should mention 'escapes config directory'; got %v", err)
			}
		})
	}
}

// TestLoadUsesRepoYAML_PromptsPackDir_AllowsRelativePathsInsideConfigDir
// pins the happy path: any relative path that stays under the
// config directory after Clean must pass through. Includes
// the `foo/../bar` shape that uses `..` segments but ultimately
// stays inside.
func TestLoadUsesRepoYAML_PromptsPackDir_AllowsRelativePathsInsideConfigDir(t *testing.T) {
	for _, goodPath := range []string{
		".local-review/prompts",
		"prompts",
		"a/b/c",
		"foo/../bar", // ultimately resolves to <baseDir>/bar
	} {
		t.Run(goodPath, func(t *testing.T) {
			baseDir := t.TempDir()
			repoCfg := filepath.Join(baseDir, ".local-review.yml")
			if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: `+goodPath+`
`), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(repoCfg)
			if err != nil {
				t.Fatalf("Load: %v (path %q should be allowed)", err, goodPath)
			}
			if cfg.Prompts.PackDir == "" {
				t.Errorf("PackDir should be set; got empty")
			}
		})
	}
}

// v0.10.5 hardening: a symlink inside the config dir pointing
// outside it must NOT pass the containment check, even though the
// lexical path stays inside. Pre-fix `pathInsideDir` was
// filepath.Rel-only and would have admitted this; with
// EvalSymlinks the real path is checked and rejected.
//
// Skip on Windows — symlink creation needs Developer Mode or
// admin privilege, neither of which CI runners reliably have.
// The hardening still applies on Windows; we just can't exercise
// it portably from a test.
func TestLoadUsesRepoYAML_PromptsPackDir_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin/Developer Mode on Windows; covered on linux+darwin")
	}
	baseDir := t.TempDir()
	// Target lives outside baseDir — this is what the symlink will
	// point at. EvalSymlinks must resolve through the link and
	// flag the escape.
	outside := t.TempDir()

	// Create a symlink INSIDE baseDir that points OUTSIDE. The
	// lexical containment check on `pack_dir: evil-link` will
	// pass (rel == "evil-link", no `..`). The symlink resolution
	// pass must catch it.
	linkPath := filepath.Join(baseDir, "evil-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	repoCfg := filepath.Join(baseDir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: evil-link
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(repoCfg)
	if err == nil {
		t.Fatalf("Load: expected symlink-escape error, got nil (the symlink pointed at %q, outside the config dir %q)", outside, baseDir)
	}
	if !strings.Contains(err.Error(), "escapes config directory") {
		t.Errorf("error message = %q, want it to mention 'escapes config directory'", err.Error())
	}
}

// Counter-test: a symlink INSIDE the config dir pointing to
// another directory INSIDE the config dir must still pass. This
// is the legitimate "I want to alias a deeper path" case (e.g.
// pack_dir: current → versions/v2).
func TestLoadUsesRepoYAML_PromptsPackDir_AllowsSymlinkInsideDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin/Developer Mode on Windows; covered on linux+darwin")
	}
	baseDir := t.TempDir()

	// Real target lives inside baseDir.
	realTarget := filepath.Join(baseDir, "versions", "v2")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	// Symlink at baseDir/current → baseDir/versions/v2
	linkPath := filepath.Join(baseDir, "current")
	if err := os.Symlink(realTarget, linkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	repoCfg := filepath.Join(baseDir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: current
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v (symlink staying inside config dir should be allowed)", err)
	}
	if cfg.Prompts.PackDir == "" {
		t.Errorf("PackDir should be set; got empty")
	}
}

// v0.10.5-RC had a real bypass: if `<base>/evil-link` symlinked
// to /etc and pack_dir was `evil-link/new-leaf`, EvalSymlinks on
// the full path failed (new-leaf doesn't exist), my fallback
// returned true. But the PARENT already resolved outside base —
// the leaf being missing didn't change that. Codex caught this
// on PR #90's own dogfood. The walk-up fix in deepestExistingAncestor
// closes the bypass: even when the final component doesn't exist,
// the deepest existing ancestor (the symlink) is what we check.
func TestLoadUsesRepoYAML_PromptsPackDir_RejectsSymlinkEscapeThroughMissingLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin/Developer Mode on Windows")
	}
	baseDir := t.TempDir()
	outside := t.TempDir() // /private/var/.../TestXxx/001 — exists, outside baseDir

	linkPath := filepath.Join(baseDir, "evil-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}
	// new-leaf does NOT exist. The bypass: lexical pass admits
	// `evil-link/new-leaf` (no `..` segments), and a naive
	// EvalSymlinks-on-full-path returns ENOENT, which the
	// first-pass fix treated as "fall through to allowed."
	// The walk-up fix must reject it.

	repoCfg := filepath.Join(baseDir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: evil-link/new-leaf
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(repoCfg)
	if err == nil {
		t.Fatalf("Load: expected escape-through-missing-leaf to be rejected, got nil (evil-link resolves to %q, outside %q)", outside, baseDir)
	}
	if !strings.Contains(err.Error(), "escapes config directory") {
		t.Errorf("error message = %q, want 'escapes config directory'", err.Error())
	}
}

// Counter-test: pack_dir points at a deep path under a legitimate
// inside-base symlink, with the leaf not yet existing. Walk-up
// must terminate at the symlink (which resolves inside base) and
// admit the path.
func TestLoadUsesRepoYAML_PromptsPackDir_AllowsMissingLeafUnderInsideSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin/Developer Mode on Windows")
	}
	baseDir := t.TempDir()

	realInside := filepath.Join(baseDir, "real-prompts")
	if err := os.MkdirAll(realInside, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(baseDir, "prompts")
	if err := os.Symlink(realInside, linkPath); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	repoCfg := filepath.Join(baseDir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: prompts/v3
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v (deep path under an inside-base symlink should be allowed even if the leaf doesn't exist yet)", err)
	}
	if cfg.Prompts.PackDir == "" {
		t.Errorf("PackDir should be set; got empty")
	}
}

// Edge case: pack_dir points at a path that doesn't exist yet
// (user plans to create it after first run). EvalSymlinks errors
// on missing paths, but the lexical check already passed, so we
// fall through to "allowed." The file-open path will surface its
// own "no such file" error if/when the user actually invokes
// review without creating the directory.
func TestLoadUsesRepoYAML_PromptsPackDir_AllowsNonExistentTarget(t *testing.T) {
	baseDir := t.TempDir()
	repoCfg := filepath.Join(baseDir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: not-yet-created
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v (non-existent target should be allowed; downstream file-open will catch missing)", err)
	}
	if cfg.Prompts.PackDir == "" {
		t.Errorf("PackDir should be set; got empty")
	}
}

func TestLoadUsesRepoYAML_PromptsCustomization(t *testing.T) {
	// Issue #55: v0.8 prompts customization layer must round-trip
	// through Load + the merge overlay. The maintenance-contract
	// test (TestMergeCoversAllExportedFields) catches a missing
	// overlay branch with reflection; this test pins the YAML
	// shape users actually write.
	dir := t.TempDir()
	repoCfg := filepath.Join(dir, ".local-review.yml")
	if err := os.WriteFile(repoCfg, []byte(`
prompts:
  pack_dir: .local-review/prompts
  prepend: |
    House rule: never approve commented-out code.
  append: |
    Output language: English only.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repoCfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Load resolves relative pack_dir against the config file's
	// directory (see TestLoadUsesRepoYAML_PromptsPackDir_ResolvedRelativeToConfig
	// for the why), so the value comes back absolute. Verify it
	// ends with the expected suffix so the test stays portable
	// across temp-dir paths.
	if !strings.HasSuffix(cfg.Prompts.PackDir, filepath.Join(".local-review", "prompts")) {
		t.Errorf("Prompts.PackDir = %q, want suffix .local-review/prompts", cfg.Prompts.PackDir)
	}
	if !filepath.IsAbs(cfg.Prompts.PackDir) {
		t.Errorf("Prompts.PackDir = %q, want absolute path", cfg.Prompts.PackDir)
	}
	if !strings.Contains(cfg.Prompts.Prepend, "House rule") {
		t.Errorf("Prompts.Prepend = %q, want it to contain 'House rule'", cfg.Prompts.Prepend)
	}
	if !strings.Contains(cfg.Prompts.Append, "English only") {
		t.Errorf("Prompts.Append = %q, want it to contain 'English only'", cfg.Prompts.Append)
	}
}

func TestValidate_Success(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

func TestValidate_NoLLMsEnabled(t *testing.T) {
	cfg := Defaults()
	// Disable all LLMs
	for name, llm := range cfg.LLMs {
		llm.Enabled = boolPtr(false)
		cfg.LLMs[name] = llm
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error when no LLMs enabled")
	}
	// Must be the sentinel so the runner can errors.Is it and tolerate
	// it specifically when --only explicitly selects agents (overriding
	// config enable/disable). A bare fmt.Errorf string would force the
	// runner to do a brittle string-match.
	if !errors.Is(err, ErrAllLLMsDisabled) {
		t.Errorf("expected ErrAllLLMsDisabled sentinel, got %v", err)
	}
}

// When all LLMs are disabled AND another config error exists (here a
// bogus merge.preferred_llm), Validate must return the OTHER error —
// NOT ErrAllLLMsDisabled. The runner tolerates ErrAllLLMsDisabled under
// --only, so if the all-disabled check short-circuited ahead of the
// merge check, a real misconfig could be silently masked. This pins the
// "all-disabled check runs last" ordering.
func TestValidate_AllDisabledDoesNotMaskOtherErrors(t *testing.T) {
	cfg := Defaults()
	for name, llm := range cfg.LLMs {
		llm.Enabled = boolPtr(false)
		cfg.LLMs[name] = llm
	}
	cfg.Merge.PreferredLLM = "nonexistent" // a real misconfig that must surface

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an error (bad preferred_llm)")
	}
	if errors.Is(err, ErrAllLLMsDisabled) {
		t.Errorf("all-disabled check masked the merge.preferred_llm error; got sentinel, want the preferred_llm error: %v", err)
	}
	if !strings.Contains(err.Error(), "preferred_llm") {
		t.Errorf("expected a merge.preferred_llm error, got %v", err)
	}
}

func TestValidate_InvalidPreferredLLM(t *testing.T) {
	cfg := Defaults()
	cfg.Merge.PreferredLLM = "nonexistent"

	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for invalid preferred_llm")
	}
}

func TestValidate_StrayModeFieldIsIgnored(t *testing.T) {
	// `mode:` was removed from LLMConfig in v0.5.x — it shipped in the
	// v0.1 example but was never wired through to the orchestrator.
	// Existing user YAML configs with `mode: cli|api|whatever` lines
	// must keep loading: yaml.v3 silently drops unknown fields, and
	// Validate must not refuse to start because of this legacy line.
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed on default config: %v", err)
	}
}

// TestMergeCoversAllExportedFields enforces the maintenance contract
// documented above merge(): every exported field on Config and its
// nested structs must be wired into the overlay logic.
//
// Strategy: synthesize a `src` Config where every exported field has
// a non-zero sentinel, merge onto a zeroed `dst`, then walk the result
// via reflection and fail loudly on any field that's still its zero
// value. Catches the "added field, forgot overlay branch" footgun the
// reviewer flagged — `LLMConfig.Mode` shipped that way for several
// minor releases until v0.5.x. The test costs nothing at runtime;
// drift is caught the moment a new field lands.
func TestMergeCoversAllExportedFields(t *testing.T) {
	src := nonZeroConfig()
	// Seed dst with a pre-existing entry under the SAME key ("claude") so
	// merge() takes the per-field OVERLAY branch — the normal cascade
	// (defaults populate every CLI LLM, then user YAML overlays an existing
	// key), not the wholesale "new LLM, copy the struct" branch. The overlay
	// branch is where a dropped field silently no-ops in production (the
	// documented LLMConfig.Mode-drifted-inert footgun), so coverage must
	// walk THAT branch. The entry starts zero-valued so every field has to
	// be copied in by an explicit overlay line.
	dst := Config{LLMs: map[string]LLMConfig{"claude": {}}}
	merge(&dst, src)

	missing := findZeroFields(reflect.ValueOf(dst), "")
	// merge() actually DOES copy the deprecated inline APIKey field
	// for v0.4.x backward compat — a user with `api_key:` in their
	// YAML still works, and warnDeprecatedAPIKeys nudges them toward
	// env vars via stderr instead of silently dropping their key.
	// So nothing is filtered out; if any exported field is missing,
	// merge() needs an overlay branch.
	if len(missing) > 0 {
		t.Errorf("merge() didn't propagate these fields:\n  %s\n\n"+
			"Add an overlay branch in merge() for each one. See the\n"+
			"MAINTENANCE CONTRACT comment above merge().",
			strings.Join(missing, "\n  "))
	}
}

// nonZeroConfig returns a Config where every exported (non-deprecated)
// field is set to a unique non-zero value. Sentinel values aren't
// meaningful — we only assert presence after merge.
//
// v0.15: the v0 single-LLM Provider block was removed; the test
// previously exercised Provider.BaseURL/Model/APIKey/APIKeyEnv/
// TimeoutSec — that surface is gone, the corresponding overlay
// branch in merge() is gone, and so is the entry here.
func nonZeroConfig() Config {
	enabled := true
	dedup := true
	return Config{
		Review: Review{
			MinSeverity:  "major",
			MaxFindings:  42,
			IncludeGlobs: []string{"**/*.go"},
			ExcludeGlobs: []string{"**/vendor/**"},
			PromptPack:   "go",
		},
		Org: Org{
			ConfigURL: "https://internal.test/lr.yml",
		},
		LLMs: map[string]LLMConfig{
			"claude": {
				Enabled:          &enabled,
				CLIPath:          "/opt/test/claude",
				BaseURL:          "http://test.invalid/v1", // not actually used for claude — present so the merge-coverage reflection sees the BaseURL field exercised
				Model:            "claude-test",
				APIKeyEnv:        "TEST_ANTHROPIC",
				APIKey:           "sk-test", // merge() copies for v0.4.x compat; deprecated, warnings nudge to env
				TimeoutSec:       240,
				ForceAfterSunset: &enabled, // v0.15 — opt-out for gemini auto-disable; exercised here so the merge-coverage reflector sees it
			},
		},
		Merge: MergeConfig{
			PreferredLLM:       "claude",
			Deduplicate:        &dedup,
			ConsensusThreshold: 7,
		},
		Storage: StorageConfig{
			BasePath: "/tmp/test-reviews",
		},
		Prompts: PromptsConfig{
			PackDir: "/etc/local-review/prompts",
			Prepend: "House rule sentinel.",
			Append:  "Output sentinel.",
		},
	}
}

// findZeroFields walks v's exported fields and returns dotted paths of
// any that are still their zero value. Used to detect merge() drift.
func findZeroFields(v reflect.Value, prefix string) []string {
	var out []string
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			fv := v.Field(i)
			ft := t.Field(i)
			if !ft.IsExported() {
				continue
			}
			path := prefix + ft.Name
			out = append(out, findZeroFields(fv, path+".")...)
		}
	case reflect.Map:
		if v.Len() == 0 {
			out = append(out, strings.TrimSuffix(prefix, "."))
			return out
		}
		// Recurse into one entry — sufficient to catch "the inner struct
		// type isn't fully merged"; we don't need to walk every key.
		for _, k := range v.MapKeys() {
			out = append(out, findZeroFields(v.MapIndex(k), prefix+k.String()+".")...)
			break
		}
	case reflect.Pointer:
		if v.IsNil() {
			out = append(out, strings.TrimSuffix(prefix, "."))
		}
	default:
		if v.IsZero() {
			out = append(out, strings.TrimSuffix(prefix, "."))
		}
	}
	return out
}

// --- v0.14 provider: deprecation -----------------------------------------
//
// The v0.14 deprecation-warning predicate (shouldWarnDeprecatedProvider)
// + its 8 unit tests were removed in v0.15 along with the warning
// itself — the `provider:` block is now hard-rejected at config-load
// time. The replacement guard is TestLoad_RejectsRemovedProviderBlock
// near the top of this file, plus the table-driven
// TestDetectRemovedProviderBlock just below it.
//
// The SanitizeBaseURLForDisplay function itself still survives, used
// by `local-review config` to mask credentials embedded in each
// `llms.<name>.base_url` value before printing. The tests below pin
// its sanitization contract independent of any caller.

func TestSanitizeBaseURLForDisplay_StripsBasicAuth(t *testing.T) {
	// The whole point: a URL with embedded `user:password@` must NOT
	// land in stderr / CI logs / terminal history. Lose userinfo;
	// keep scheme + host + path so the suggestion is still useful.
	got := SanitizeBaseURLForDisplay("https://user:s3cret@api.openai.com/v1")
	if strings.Contains(got, "s3cret") || strings.Contains(got, "user") {
		t.Errorf("basic-auth credentials must be stripped; got %q", got)
	}
	if !strings.Contains(got, "api.openai.com/v1") {
		t.Errorf("scheme+host+path must survive sanitization; got %q", got)
	}
}

func TestSanitizeBaseURLForDisplay_StripsQueryAndFragment(t *testing.T) {
	// Some providers accept the key on the query string (`?api_key=…`)
	// or a session id on the fragment. Both belong in env vars, not
	// stderr — drop them.
	got := SanitizeBaseURLForDisplay("https://example.test/v1?api_key=sk-leak#sid=abc")
	if strings.Contains(got, "api_key") || strings.Contains(got, "sk-leak") || strings.Contains(got, "sid=") {
		t.Errorf("query/fragment must be stripped; got %q", got)
	}
	if !strings.Contains(got, "example.test/v1") {
		t.Errorf("scheme+host+path must survive sanitization; got %q", got)
	}
}

func TestSanitizeBaseURLForDisplay_UnparseableReturnsPlaceholder(t *testing.T) {
	// Fail-closed: if url.Parse rejects the value or it has no host,
	// don't echo the raw string into the migration snippet — print a
	// neutral placeholder instead. Matches CLAUDE.md rule 4
	// ("refuse on invalid input rather than silently passing it
	// through") for the display path.
	for _, raw := range []string{"", "://bad", "not a url"} {
		got := SanitizeBaseURLForDisplay(raw)
		if got != "<your-provider-url>" {
			t.Errorf("expected placeholder for unparseable %q, got %q", raw, got)
		}
	}
}

func TestSanitizeBaseURLForDisplay_PlainURLRoundtrips(t *testing.T) {
	// The common case: an unauthenticated URL stays exactly itself.
	const in = "http://localhost:11434/v1"
	if got := SanitizeBaseURLForDisplay(in); got != in {
		t.Errorf("plain URL must roundtrip; got %q", got)
	}
}
