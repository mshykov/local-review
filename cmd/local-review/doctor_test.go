package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mshykov/local-review/internal/cli"
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
	got := checkClaudeAuth()
	if got.authenticated {
		t.Errorf("clean home + no env var: expected unauth, got %+v", got)
	}
	if got.hint == "" {
		t.Error("expected non-empty hint to guide the user")
	}
}

func TestCheckClaudeAuth_OAuthFromSessions(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".claude", "sessions", "12345.json"), `{}`)
	got := checkClaudeAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated=true, got %+v", got)
	}
	if got.method != "oauth" {
		t.Errorf("method: want oauth, got %q", got.method)
	}
}

func TestCheckClaudeAuth_APIKey(t *testing.T) {
	withFakeHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	got := checkClaudeAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if got.method != "api_key" {
		t.Errorf("method: want api_key, got %q", got.method)
	}
}

// --- Gemini --------------------------------------------------------

func TestCheckGeminiAuth_NotAuthenticated(t *testing.T) {
	withFakeHome(t)
	got := checkGeminiAuth()
	if got.authenticated {
		t.Errorf("expected unauth, got %+v", got)
	}
}

func TestCheckGeminiAuth_NotAuthenticatedWithEmptyAccountsFile(t *testing.T) {
	// google_accounts.json with active=null is the post-install,
	// pre-login state. Must not be reported as authenticated.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"), `{"active": null, "old": []}`)
	got := checkGeminiAuth()
	if got.authenticated {
		t.Errorf("active=null should not count as authenticated, got %+v", got)
	}
}

func TestCheckGeminiAuth_OAuthAccount(t *testing.T) {
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"),
		`{"active": "user@example.com", "old": []}`)
	got := checkGeminiAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if got.method != "oauth" {
		t.Errorf("method: want oauth, got %q", got.method)
	}
}

func TestCheckGeminiAuth_APIKeyOverridesOAuth(t *testing.T) {
	// Env var takes precedence (GEMINI_API_KEY wins over OAuth state).
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"),
		`{"active": "user@example.com", "old": []}`)
	t.Setenv("GEMINI_API_KEY", "test-key")
	got := checkGeminiAuth()
	if got.method != "api_key" {
		t.Errorf("env var should win, got method=%q", got.method)
	}
}

// --- Codex ---------------------------------------------------------

func TestCheckCodexAuth_NotAuthenticated(t *testing.T) {
	withFakeHome(t)
	got := checkCodexAuth()
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
	got := checkCodexAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if got.method != "oauth" {
		t.Errorf("method: want oauth (chatgpt), got %q", got.method)
	}
}

func TestCheckCodexAuth_APIKeyFromAuthFile(t *testing.T) {
	// Codex stores api_mode in the auth file when the user runs
	// `codex login --api-key`. Must report this as authenticated even
	// without the env var being set in the current shell.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"auth_mode": "api_key", "OPENAI_API_KEY": "sk-stored"}`)
	got := checkCodexAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if got.method != "api_key" {
		t.Errorf("method: want api_key, got %q", got.method)
	}
}

func TestCheckCodexAuth_APIKeyFromEnv(t *testing.T) {
	withFakeHome(t)
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	got := checkCodexAuth()
	if !got.authenticated {
		t.Fatalf("expected authenticated, got %+v", got)
	}
	if got.method != "api_key" {
		t.Errorf("method: want api_key, got %q", got.method)
	}
}

func TestCheckCodexAuth_GarbageAuthFile(t *testing.T) {
	// A corrupted auth.json must not crash the doctor or report
	// false-positive auth.
	home := withFakeHome(t)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `not json`)
	got := checkCodexAuth()
	if got.authenticated {
		t.Errorf("garbage auth file should not count as authenticated, got %+v", got)
	}
}

// --- classify ------------------------------------------------------

func TestClassify(t *testing.T) {
	withFakeHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test") // makes Claude authenticated

	cases := []struct {
		name string
		llm  cli.LLM
		want llmStatus
	}{
		{
			"installed + version + auth → ready",
			cli.LLM{Available: true, Path: "/x/claude", Version: "1.0", Name: "claude"},
			statusReady,
		},
		{
			"path empty → not installed",
			cli.LLM{Available: false, Path: "", Name: "claude"},
			statusNotInstalled,
		},
		{
			"path set but Available=false → broken install",
			cli.LLM{Available: false, Path: "/x/claude", Name: "claude"},
			statusBrokenInstall,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.llm)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
