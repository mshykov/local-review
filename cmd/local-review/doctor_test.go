package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mshykov/local-review/internal/cli"
	"github.com/mshykov/local-review/internal/config"
)

// withFakeHome points all auth checks at a temp dir for the duration
// of the test by setting LOCAL_REVIEW_AUTH_HOME and clearing the
// provider-specific env vars so test cases stay isolated from the
// developer's real environment.
func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCAL_REVIEW_AUTH_HOME", dir)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- Claude --------------------------------------------------------

func TestCheckClaudeAuth_NotAuthenticated(t *testing.T) {
	withFakeHome(t)
	got := checkClaudeAuth("")
	if got.authenticated {
		t.Errorf("clean home + no env var: expected unauth, got %+v", got)
	}
	if got.hint == "" {
		t.Error("expected non-empty hint to guide the user")
	}
}

func TestCheckClaudeAuth_RecentSession(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".claude", "sessions", "12345.json"), `{}`)
	got := checkClaudeAuth("")
	if !got.authenticated {
		t.Fatalf("expected authenticated=true, got %+v", got)
	}
	if !strings.Contains(got.detail, "claude login") {
		t.Errorf("detail should mention claude login: %q", got.detail)
	}
}

func TestCheckClaudeAuth_StaleSessionDoesNotCount(t *testing.T) {
	// A session file modified before the freshness cutoff (e.g., from a
	// long-ago login the user has since logged out of) must not be
	// reported as authenticated.
	home := withFakeHome(t)
	stalePath := filepath.Join(home, ".claude", "sessions", "old.json")
	writeFile(t, stalePath, `{}`)
	old := time.Now().Add(-(claudeSessionFreshness + 24*time.Hour))
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}
	got := checkClaudeAuth("")
	if got.authenticated {
		t.Errorf("stale session should not authenticate, got %+v", got)
	}
}

func TestCheckClaudeAuth_APIKey(t *testing.T) {
	withFakeHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	got := checkClaudeAuth("")
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if !strings.Contains(got.detail, "ANTHROPIC_API_KEY") {
		t.Errorf("detail should mention env var: %q", got.detail)
	}
}

func TestCheckClaudeAuth_EnvVarWinsOverSession(t *testing.T) {
	// Uniform precedence: env var represents current shell intent,
	// must take priority over a possibly-stale OAuth session.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".claude", "sessions", "12345.json"), `{}`)
	t.Setenv("ANTHROPIC_API_KEY", "sk-fresh")
	got := checkClaudeAuth("")
	if !strings.Contains(got.detail, "ANTHROPIC_API_KEY") {
		t.Errorf("env var should win over session, got %q", got.detail)
	}
}

// --- Gemini --------------------------------------------------------

func TestCheckGeminiAuth_NotAuthenticated(t *testing.T) {
	withFakeHome(t)
	got := checkGeminiAuth("")
	if got.authenticated {
		t.Errorf("expected unauth, got %+v", got)
	}
}

func TestCheckGeminiAuth_NotAuthenticatedWithEmptyAccountsFile(t *testing.T) {
	// google_accounts.json with active=null is the post-install,
	// pre-login state. Must not be reported as authenticated.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"), `{"active": null, "old": []}`)
	got := checkGeminiAuth("")
	if got.authenticated {
		t.Errorf("active=null should not count as authenticated, got %+v", got)
	}
}

func TestCheckGeminiAuth_OAuthAccount(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"),
		`{"active": "user@example.com", "old": []}`)
	got := checkGeminiAuth("")
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if !strings.Contains(got.detail, "OAuth") {
		t.Errorf("detail should mention OAuth: %q", got.detail)
	}
}

func TestCheckGeminiAuth_APIKeyWinsOverOAuth(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"),
		`{"active": "user@example.com", "old": []}`)
	t.Setenv("GEMINI_API_KEY", "test-key")
	got := checkGeminiAuth("")
	if !strings.Contains(got.detail, "GEMINI_API_KEY") {
		t.Errorf("env var should win, got %q", got.detail)
	}
}

func TestCheckGeminiAuth_HonorsCustomEnvVar(t *testing.T) {
	// A user with `api_key_env: MY_GEMINI_KEY` in config and only that
	// var exported should see ✓ ready, not "not authenticated". Pre-fix
	// the auth check hardcoded GEMINI_API_KEY so the configured var was
	// silently ignored.
	withFakeHome(t)
	t.Setenv("MY_GEMINI_KEY", "secret-from-custom-env")
	got := checkGeminiAuth("MY_GEMINI_KEY")
	if !got.authenticated {
		t.Fatalf("expected authenticated via custom env var, got %+v", got)
	}
	if !strings.Contains(got.detail, "MY_GEMINI_KEY") {
		t.Errorf("detail should mention the configured env var, got %q", got.detail)
	}
}

func TestCheckGeminiAuth_HintMentionsCustomEnvVar(t *testing.T) {
	// When the user is unauthed AND has a custom api_key_env configured,
	// the "fix" hint must point at THEIR env var, not the canonical one
	// — otherwise the user fixes the wrong knob.
	withFakeHome(t)
	got := checkGeminiAuth("MY_GEMINI_KEY")
	if got.authenticated {
		t.Fatal("expected unauth, got authenticated")
	}
	if !strings.Contains(got.hint, "MY_GEMINI_KEY") {
		t.Errorf("hint should reference the configured env var, got %q", got.hint)
	}
	if strings.Contains(got.hint, "GEMINI_API_KEY") {
		t.Errorf("hint should NOT reference the canonical default when a custom env is set, got %q", got.hint)
	}
}

// --- Codex ---------------------------------------------------------

func TestCheckCodexAuth_NotAuthenticated(t *testing.T) {
	withFakeHome(t)
	got := checkCodexAuth("")
	if got.authenticated {
		t.Errorf("expected unauth, got %+v", got)
	}
	if got.hint == "" {
		t.Error("expected hint")
	}
}

func TestCheckCodexAuth_ChatGPTSubscription(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"auth_mode": "chatgpt", "OPENAI_API_KEY": null, "tokens": {"id_token": "x"}}`)
	got := checkCodexAuth("")
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if !strings.Contains(got.detail, "ChatGPT") {
		t.Errorf("detail should mention ChatGPT: %q", got.detail)
	}
}

func TestCheckCodexAuth_ExplicitAPIKeyMode(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"auth_mode": "api_key", "OPENAI_API_KEY": "sk-stored"}`)
	got := checkCodexAuth("")
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if !strings.Contains(got.detail, "codex login --api-key") {
		t.Errorf("detail should mention --api-key flow: %q", got.detail)
	}
}

func TestCheckCodexAuth_APIKeyModeWithoutStoredKey(t *testing.T) {
	// A partial/corrupted auth.json where auth_mode is "api_key" but
	// OPENAI_API_KEY is null/empty must not produce a false-positive
	// authenticated result.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"auth_mode": "api_key", "OPENAI_API_KEY": null}`)
	got := checkCodexAuth("")
	if got.authenticated {
		t.Errorf("api_key mode with null key should not authenticate, got %+v", got)
	}
}

func TestCheckCodexAuth_LegacyAuthFile(t *testing.T) {
	// Older codex versions / hand-edited files may lack an explicit
	// auth_mode but have a stored OPENAI_API_KEY. Honor that.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"OPENAI_API_KEY": "sk-stored"}`)
	got := checkCodexAuth("")
	if !got.authenticated {
		t.Errorf("legacy auth file with stored key should authenticate, got %+v", got)
	}
}

func TestCheckCodexAuth_APIKeyEnvWinsOverFile(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"auth_mode": "chatgpt", "OPENAI_API_KEY": null}`)
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	got := checkCodexAuth("")
	if !strings.Contains(got.detail, "OPENAI_API_KEY") {
		t.Errorf("env var should win, got %q", got.detail)
	}
}

func TestCheckCodexAuth_GarbageAuthFile(t *testing.T) {
	// A corrupted auth.json must not crash the doctor or report
	// false-positive auth.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `not json`)
	got := checkCodexAuth("")
	if got.authenticated {
		t.Errorf("garbage auth file should not count as authenticated, got %+v", got)
	}
}

// --- classify ------------------------------------------------------

func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		llm     cli.LLM
		want    llmStatus
		wantSub string // substring expected in the returned authStatus.detail (when ready)
	}{
		{
			name:    "installed + version + auth → ready",
			setup:   func(t *testing.T) { withFakeHome(t); t.Setenv("ANTHROPIC_API_KEY", "sk-test") },
			llm:     cli.LLM{Available: true, Path: "/x/claude", Version: "1.0", Name: "claude"},
			want:    statusReady,
			wantSub: "ANTHROPIC_API_KEY",
		},
		{
			name:  "installed + version + no auth → not authenticated",
			setup: func(t *testing.T) { withFakeHome(t) },
			llm:   cli.LLM{Available: true, Path: "/x/claude", Version: "1.0", Name: "claude"},
			want:  statusNotAuthed,
		},
		{
			name:  "path empty → not installed",
			setup: func(t *testing.T) { withFakeHome(t) },
			llm:   cli.LLM{Available: false, Path: "", Name: "claude"},
			want:  statusNotInstalled,
		},
		{
			name:  "path set but Available=false → broken install",
			setup: func(t *testing.T) { withFakeHome(t) },
			llm:   cli.LLM{Available: false, Path: "/x/claude", Name: "claude"},
			want:  statusBrokenInstall,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			gotStatus, gotAuth := classify(tc.llm, "")
			if gotStatus != tc.want {
				t.Errorf("status: got %v, want %v", gotStatus, tc.want)
			}
			if tc.wantSub != "" && !strings.Contains(gotAuth.detail, tc.wantSub) {
				t.Errorf("auth detail %q missing substring %q", gotAuth.detail, tc.wantSub)
			}
		})
	}
}

// ----------------------------------------------------------------
// v0.8 / issue #55: doctor warns on missing/empty pack_dir.
// ----------------------------------------------------------------

func TestCheckPromptOverride_Quiet_WhenUnset(t *testing.T) {
	// pack_dir empty → no warning. Most users land here.
	var buf bytes.Buffer
	checkPromptOverride(&buf, config.Config{})
	if buf.Len() != 0 {
		t.Errorf("expected silent output when pack_dir is unset, got: %q", buf.String())
	}
}

func TestCheckPromptOverride_WarnsOnMissingDir(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: "/nope/does/not/exist"}}
	checkPromptOverride(&buf, cfg)
	got := buf.String()
	for _, want := range []string{"⚠", "pack_dir", "does not exist"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q\n%s", want, got)
		}
	}
}

func TestCheckPromptOverride_WarnsWhenDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oops.md")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: path}}
	checkPromptOverride(&buf, cfg)
	got := buf.String()
	if !strings.Contains(got, "is a file") {
		t.Errorf("expected 'is a file' warning, got: %q", got)
	}
}

func TestCheckPromptOverride_WarnsOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: dir}}
	checkPromptOverride(&buf, cfg)
	got := buf.String()
	if !strings.Contains(got, "no <language>.md") {
		t.Errorf("expected 'no <language>.md' warning on empty dir, got: %q", got)
	}
}

func TestCheckPromptOverride_QuietWhenDirHasOverrides(t *testing.T) {
	// Happy path: pack_dir points at a directory with at least one
	// override file → no warning, the user knows what they're doing.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.md"), []byte("# override"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: dir}}
	checkPromptOverride(&buf, cfg)
	if buf.Len() != 0 {
		t.Errorf("expected silence when override dir is populated, got: %q", buf.String())
	}
}

func TestCheckPromptOverride_WarnsOnUnreadableOverride(t *testing.T) {
	// Self-review iter 2 (claude+codex consensus): a known-language
	// override file that exists but isn't readable (perms drift, NFS
	// hiccup, broken symlink) was silently skipped by the resolver
	// AND passed the count check in doctor — so the user saw "all
	// good" but no customization actually applied. Now: doctor
	// actively probes readability and warns.
	//
	// Skips on platforms where chmod 0 doesn't actually deny read:
	// Windows (different ACL model) and Unix-as-root (CAP_DAC_READ_
	// SEARCH bypasses mode bits). Codex flagged the prior shape as
	// environment-flaky in iter-3 review.
	if isWindows() {
		t.Skip("chmod 0 doesn't deny read on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode-bit permissions")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "default.md")
	bad := filepath.Join(dir, "go.md")
	if err := os.WriteFile(good, []byte("# default override"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("# go override"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o644) })

	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: dir}}
	checkPromptOverride(&buf, cfg)
	out := buf.String()
	if !strings.Contains(out, "unreadable") {
		t.Errorf("expected 'unreadable' warning, got: %q", out)
	}
	if !strings.Contains(out, "go.md") {
		t.Errorf("expected the unreadable filename in the warning, got: %q", out)
	}
	if strings.Contains(out, "default.md") {
		t.Errorf("readable file should not appear in the unreadable warning, got: %q", out)
	}
}

// isWindows is a tiny helper to skip permission tests that don't
// work the same way on Windows (chmod 000 doesn't deny read).
// runtime.GOOS would be more idiomatic but pulling in the stdlib
// runtime package for a single-use check is overkill; checking the
// path separator is just as reliable for our cross-platform CI.
func isWindows() bool {
	return os.PathSeparator == '\\'
}

// TestCheckPromptOverride_FlagsEmptyFile verifies the probe treats
// an empty / whitespace-only override file as unreadable. The
// resolver's TrimSpace check at internal/prompts/prompts.go falls
// through silently on those files (defending against an
// accidentally-truncated pack neutering the entire system prompt);
// the pre-fix doctor probe used os.Open + Close which considered
// a zero-byte file "readable" and reported ✓ — so the user saw
// "everything good" while reviews silently used the embedded pack.
func TestCheckPromptOverride_FlagsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.md"), []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: dir}}
	checkPromptOverride(&buf, cfg)
	out := buf.String()
	if !strings.Contains(out, "unreadable") || !strings.Contains(out, "default.md") || !strings.Contains(out, "empty") {
		t.Errorf("expected empty-file warning naming default.md, got: %q", out)
	}
}

func TestCheckPromptOverride_StrayMarkdownDoesNotCount(t *testing.T) {
	// Codex caught this in self-review: pre-fix, ANY *.md file
	// counted as an override, so a stray README.md silenced the
	// "no overrides" warning even though no real customization
	// applied. The check now matches against known language ids.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte("- write go.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cfg := config.Config{Prompts: config.PromptsConfig{PackDir: dir}}
	checkPromptOverride(&buf, cfg)
	if !strings.Contains(buf.String(), "no <language>.md") {
		t.Errorf("README.md / TODO.md should not count as override files; expected warning, got: %q", buf.String())
	}
}

// A detected-but-review-incapable CLI (antigravity) must classify as
// statusExperimental — never statusReady — regardless of auth state,
// so doctor surfaces it as "detected, experimental" and the runner
// keeps it out of the fan-out. This pins the dogfood decision: agy's
// agentic `--print` mode can't serve as a stateless reviewer backend.
func TestClassify_AntigravityIsExperimentalNotReady(t *testing.T) {
	llm := cli.LLM{Name: "antigravity", Path: "/usr/bin/agy", Version: "1.0.2", Available: true}
	status, _ := classify(llm, "")
	if status != statusExperimental {
		t.Fatalf("antigravity should classify as statusExperimental, got %v", status)
	}
}

// An experimental CLI that isn't even installed should still report
// "not installed" (classify checks Availability first), so users get
// install instructions rather than a confusing experimental notice.
func TestClassify_AntigravityNotInstalledStillReportsNotInstalled(t *testing.T) {
	llm := cli.LLM{Name: "antigravity", Available: false}
	status, _ := classify(llm, "")
	if status != statusNotInstalled {
		t.Fatalf("uninstalled antigravity should be statusNotInstalled, got %v", status)
	}
}

// --- Copilot -------------------------------------------------------

// clearCopilotEnv neutralises the token env vars + COPILOT_HOME so the
// dir-based path is exercised deterministically (CI often exports
// GITHUB_TOKEN, which would otherwise short-circuit every case).
func clearCopilotEnv(t *testing.T) {
	t.Helper()
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("COPILOT_HOME", "")
}

// The Copilot-specific token auto-enables; the generic GH_TOKEN does
// NOT (it's commonly set for `gh`/CI, and auto-enabling a PAID reviewer
// on it is a surprise-cost footgun — see checkCopilotAuth).
func TestCheckCopilotAuth_CopilotTokenAuthenticates(t *testing.T) {
	withFakeHome(t) // empty fake home → no config dir, so the env is the only signal
	clearCopilotEnv(t)
	t.Setenv("COPILOT_GITHUB_TOKEN", "ghp_example")
	got := checkCopilotAuth("")
	if !got.authenticated {
		t.Fatalf("COPILOT_GITHUB_TOKEN set should authenticate, got %+v", got)
	}
	if !strings.Contains(got.detail, "COPILOT_GITHUB_TOKEN") {
		t.Errorf("detail should name the env var that satisfied auth: %q", got.detail)
	}
}

// A bare GH_TOKEN (no Copilot-specific token, no login) must NOT
// auto-enable Copilot — otherwise CI environments that export
// GITHUB_TOKEN for `gh` would silently fire paid Premium requests.
func TestCheckCopilotAuth_GenericGitHubTokenDoesNotAutoEnable(t *testing.T) {
	withFakeHome(t) // empty fake home → no config dir
	clearCopilotEnv(t)
	t.Setenv("GH_TOKEN", "ghp_generic")
	got := checkCopilotAuth("")
	if got.authenticated {
		t.Fatalf("a bare GH_TOKEN must NOT auto-enable the paid Copilot reviewer, got %+v", got)
	}
}

// A bare/empty ~/.copilot must NOT read as authenticated — only a
// populated dir (a real login leaves artifacts) counts.
func TestCheckCopilotAuth_EmptyConfigDirNotAuthenticated(t *testing.T) {
	home := withFakeHome(t)
	clearCopilotEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := checkCopilotAuth("")
	if got.authenticated {
		t.Fatalf("empty ~/.copilot should NOT authenticate, got %+v", got)
	}
	if got.hint == "" {
		t.Error("not-authenticated result should carry a login hint")
	}
}

// A populated ~/.copilot (a real login leaves state files) is treated
// as logged in, with the probe verifying at review time.
func TestCheckCopilotAuth_PopulatedConfigDirAuthenticates(t *testing.T) {
	home := withFakeHome(t)
	clearCopilotEnv(t)
	writeFile(t, filepath.Join(home, ".copilot", "config.json"), `{"x":1}`)
	got := checkCopilotAuth("")
	if !got.authenticated {
		t.Fatalf("populated ~/.copilot should authenticate, got %+v", got)
	}
}

// --- Gemini sunset banner (v0.15) ---------------------------------------

// The sunset banner is a pure function (geminiSunsetBanner) so its
// three modes — pre-sunset countdown, post-sunset auto-disabled,
// post-sunset force-overridden — can be exercised against an
// injected clock instead of waiting for the wall clock to cross the
// real 2026-06-18 cutoff. Higher-layer doctor wiring is covered
// indirectly through TestRunDoctor smoke tests above.

func TestGeminiSunsetBanner_PreSunsetShowsCountdown(t *testing.T) {
	var buf bytes.Buffer
	preSunset := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) // ~18 days before
	geminiSunsetBanner(&buf, preSunset, false /* force */)
	out := buf.String()
	for _, must := range []string{
		"days until Gemini CLI sunset",
		"2026-06-18",
		"Antigravity",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("pre-sunset banner missing %q\nfull output:\n%s", must, out)
		}
	}
	// Must NOT show the post-sunset wording yet.
	for _, illegal := range []string{"auto-disabled", "force_after_sunset is set"} {
		if strings.Contains(out, illegal) {
			t.Errorf("pre-sunset banner leaked post-sunset wording %q:\n%s", illegal, out)
		}
	}
}

func TestGeminiSunsetBanner_PostSunsetDefaultAutoDisabled(t *testing.T) {
	var buf bytes.Buffer
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // ~2 weeks after
	geminiSunsetBanner(&buf, postSunset, false /* force */)
	out := buf.String()
	for _, must := range []string{
		"auto-disabled",
		"2026-06-18",
		"force_after_sunset",
		"override",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("post-sunset (default) banner missing %q\nfull output:\n%s", must, out)
		}
	}
	// Pre-sunset countdown phrasing must be gone.
	if strings.Contains(out, "days until") {
		t.Errorf("post-sunset banner must not show the countdown:\n%s", out)
	}
}

func TestGeminiSunsetBanner_PostSunsetForceShowsOverride(t *testing.T) {
	var buf bytes.Buffer
	postSunset := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	geminiSunsetBanner(&buf, postSunset, true /* force */)
	out := buf.String()
	for _, must := range []string{
		"force_after_sunset is set",
		"running anyway",
		"401",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("post-sunset force-overridden banner missing %q\nfull output:\n%s", must, out)
		}
	}
	// Must NOT show auto-disabled wording when the user has opted in.
	if strings.Contains(out, "auto-disabled") {
		t.Errorf("force-overridden banner must not say 'auto-disabled':\n%s", out)
	}
}

// TestRunDoctor_Smoke exercises runDoctor end-to-end — it had NO direct
// test before the cognitive-complexity refactor split it into named
// helpers, which is itself the point: this fills a real, pre-existing gap
// (SonarCloud's new-code coverage gate caught it) rather than just
// asserting against a mock. Hermetic per runConfigCmdIn's established
// pattern in this package: fake $HOME (no repo/user config, no auth
// files) + a real, empty cwd, so loadConfig() falls through to compiled-in
// defaults deterministically regardless of the machine running the test.
//
// Doesn't assert exact ready/review-capable counts — cli.DetectAllWithOverrides
// does real PATH lookups, so which LLM CLIs are actually installed on the
// machine running this test is out of this test's control. Asserts only
// what runDoctor itself guarantees: no error, and the summary line it
// always prints.
func TestRunDoctor_Smoke(t *testing.T) {
	withFakeHome(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origCwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runDoctor(&buf); err != nil {
		t.Fatalf("runDoctor returned an error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "LLMs ready for multi-review.") {
		t.Errorf("expected the ready-count summary line, got:\n%s", out)
	}
}

// --- doctorLLMOverrides / doctorProviderSpecs / printLLMRows ---
// (extracted from runDoctor to cut its cognitive complexity — SonarCloud)

func TestDoctorLLMOverrides_CfgErrReturnsEmptyMaps(t *testing.T) {
	overrides, customEnvVars, models := doctorLLMOverrides(config.Config{
		LLMs: map[string]config.LLMConfig{"claude": {CLIPath: "/x/claude"}},
	}, errFakeConfigLoad)
	if len(overrides) != 0 || len(customEnvVars) != 0 || len(models) != 0 {
		t.Errorf("cfgErr != nil must yield empty maps, got overrides=%v customEnvVars=%v models=%v", overrides, customEnvVars, models)
	}
}

func TestDoctorLLMOverrides_PopulatesOnlySetFields(t *testing.T) {
	cfg := config.Config{LLMs: map[string]config.LLMConfig{
		"claude": {CLIPath: "/usr/local/bin/claude", APIKeyEnv: "MY_KEY", Model: "opus"},
		"gemini": {}, // no overrides set — must not appear in any map
	}}
	overrides, customEnvVars, models := doctorLLMOverrides(cfg, nil)
	if overrides["claude"] != "/usr/local/bin/claude" {
		t.Errorf("overrides[claude] = %q, want the configured CLIPath", overrides["claude"])
	}
	if customEnvVars["claude"] != "MY_KEY" {
		t.Errorf("customEnvVars[claude] = %q, want MY_KEY", customEnvVars["claude"])
	}
	if models["claude"] != "opus" {
		t.Errorf("models[claude] = %q, want opus", models["claude"])
	}
	if _, ok := overrides["gemini"]; ok {
		t.Error("gemini has no CLIPath set; must not appear in overrides")
	}
	if _, ok := customEnvVars["gemini"]; ok {
		t.Error("gemini has no APIKeyEnv set; must not appear in customEnvVars")
	}
	if _, ok := models["gemini"]; ok {
		t.Error("gemini has no Model set; must not appear in models")
	}
}

func TestDoctorProviderSpecs_CfgErrReturnsNil(t *testing.T) {
	specs := doctorProviderSpecs(config.Config{
		LLMs: map[string]config.LLMConfig{"local": {BaseURL: "http://x"}},
	}, errFakeConfigLoad)
	if specs != nil {
		t.Errorf("cfgErr != nil must yield nil specs, got %v", specs)
	}
}

func TestDoctorProviderSpecs_OnlyBaseURLEntriesSortedByName(t *testing.T) {
	cfg := config.Config{LLMs: map[string]config.LLMConfig{
		"zeta":  {BaseURL: "http://z", Model: "m-z"},
		"alpha": {BaseURL: "http://a", Model: "m-a", APIKey: "k", TimeoutSec: 30},
		"claude": {
			// No BaseURL — a plain CLI agent, must be excluded.
			CLIPath: "/usr/local/bin/claude",
		},
	}}
	specs := doctorProviderSpecs(cfg, nil)
	if len(specs) != 2 {
		t.Fatalf("expected 2 provider specs (BaseURL entries only), got %d: %+v", len(specs), specs)
	}
	if specs[0].Name != "alpha" || specs[1].Name != "zeta" {
		t.Errorf("expected sorted [alpha, zeta], got [%s, %s]", specs[0].Name, specs[1].Name)
	}
	if specs[0].BaseURL != "http://a" || specs[0].Model != "m-a" || specs[0].APIKey != "k" || specs[0].TimeoutSec != 30 {
		t.Errorf("alpha spec fields not carried through: %+v", specs[0])
	}
}

// errFakeConfigLoad is a non-nil sentinel error for exercising the
// cfgErr != nil branch of doctorLLMOverrides / doctorProviderSpecs — the
// functions only check err != nil, never its value.
var errFakeConfigLoad = fmt.Errorf("fake config load failure")

func TestPrintLLMRows_CountsReadyAndReviewCapable(t *testing.T) {
	// A fixed pre-2026 `now` keeps every CLI agent's sunset gate off
	// regardless of real-world sunset dates, so this test doesn't
	// depend on when it's run.
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	llms := []cli.LLM{
		// Provider agent, BaseURL set + Available=true: classify()
		// returns statusReady with zero filesystem/env dependency —
		// the simplest deterministic "ready" case.
		{Name: "myprovider", BaseURL: "http://x", Available: true},
		// A real CLI agent that isn't installed: statusNotInstalled,
		// still review-capable (not experimental), just not ready.
		{Name: "claude", Available: false},
		// Experimental (review-incapable): must NOT count toward
		// reviewCapable even though Available=true.
		{Name: "antigravity", Available: true},
	}
	var buf bytes.Buffer
	readyCount, reviewCapable := printLLMRows(&buf, llms, config.Config{}, map[string]string{}, map[string]string{}, now)
	if reviewCapable != 2 {
		t.Errorf("reviewCapable = %d, want 2 (myprovider + claude; antigravity excluded)", reviewCapable)
	}
	if readyCount != 1 {
		t.Errorf("readyCount = %d, want 1 (myprovider only)", readyCount)
	}
	out := buf.String()
	for _, name := range []string{"myprovider", "Claude", "Antigravity"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected a printed row mentioning %q, got:\n%s", name, out)
		}
	}
}

func TestGeminiSunsetBanner_AtSunsetMidnightIsPostSunset(t *testing.T) {
	// The at-or-after semantics: a banner rendered at exactly the
	// sunset moment must show the post-sunset wording, not the "0
	// days until" countdown — both because cli.IsAgentSunset returns
	// true on the boundary, and because rendering "0 days until
	// sunset" at the precise sunset instant would read as a bug.
	var buf bytes.Buffer
	geminiSunsetBanner(&buf, time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC), false)
	out := buf.String()
	if !strings.Contains(out, "auto-disabled") {
		t.Errorf("banner at sunset midnight must show post-sunset wording, got:\n%s", out)
	}
	if strings.Contains(out, "days until") {
		t.Errorf("banner at sunset midnight must not still show the countdown:\n%s", out)
	}
}
