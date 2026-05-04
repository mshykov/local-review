package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/cli"
)

// claudeSessionFreshness caps how stale a session file can be before
// we stop counting it as "logged in". Claude Code stores tokens in the
// OS keychain (not a file we can read), so we use session activity as
// a proxy. A 30-day window allows infrequent users while still
// reflecting an explicit logout that wipes recent activity.
const claudeSessionFreshness = 30 * 24 * time.Hour

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check LLM CLI installations and authentication status",
		Long: `Doctor checks each LLM CLI's installation, version, and authentication state.

It detects:
  - Claude CLI (claude) — auth via 'claude login' or ANTHROPIC_API_KEY
  - Gemini CLI (gemini) — auth via GEMINI_API_KEY (preferred) or 'gemini /auth' (Google OAuth)
  - OpenAI Codex CLI (codex) — auth via 'codex login' (ChatGPT Plus) or OPENAI_API_KEY

For each CLI, doctor prints one of:
  ✓ ready                — installed, version detected, authenticated
  ⚠ install broken       — binary in PATH but version probe failed
  ⚠ not authenticated    — installed and working, but no credentials/key found
  ✗ not installed        — binary not in PATH

Exit code is always 0; this is a diagnostic command, not a gate.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.OutOrStdout())
		},
	}
}

// runDoctor prints a diagnostic table of each LLM CLI's state. Output
// is structured so a user reading it knows exactly what's needed for
// each CLI: install it, log in, set an env var, or nothing.
//
// All writes go through an errWriter so the first I/O error (broken
// pipe, disk full, etc.) is preserved and returned at the end —
// otherwise `local-review doctor > /dev/full` would silently exit 0.
func runDoctor(out io.Writer) error {
	w := &errWriter{w: out}

	fmt.Fprintln(w, "Checking LLM installations and authentication...")
	fmt.Fprintln(w)

	llms := cli.DetectAll()

	readyCount := 0
	for _, llm := range llms {
		status, auth := classify(llm)
		if status == statusReady {
			readyCount++
		}
		printLLMRow(w, llm, status, auth)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%d/%d LLMs ready for multi-review.\n", readyCount, len(llms))
	return w.err
}

// errWriter is an io.Writer that captures the first error from the
// underlying writer and short-circuits subsequent writes. Lets the
// caller string together many fmt.Fprintln calls and then check one
// error at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) Write(p []byte) (int, error) {
	if ew.err != nil {
		return 0, ew.err
	}
	n, err := ew.w.Write(p)
	if err != nil {
		ew.err = err
	}
	return n, err
}

// llmStatus is the consolidated state of an LLM CLI as we want to
// surface it to the user. It collapses the install + version + auth
// signals into one of four buckets so the doctor output is unambiguous.
type llmStatus int

const (
	statusReady          llmStatus = iota // installed, version-detected, authenticated
	statusBrokenInstall                   // binary found, version probe failed
	statusNotAuthed                       // installed + version-detected, but no credentials
	statusNotInstalled                    // binary not in PATH
)

// classify returns both the bucket and the underlying authStatus so
// the caller can render either without re-running auth checks.
func classify(llm cli.LLM) (llmStatus, authStatus) {
	if !llm.Available {
		if llm.Path != "" {
			return statusBrokenInstall, authStatus{}
		}
		return statusNotInstalled, authStatus{}
	}
	auth := checkAuth(llm.Name)
	if !auth.authenticated {
		return statusNotAuthed, auth
	}
	return statusReady, auth
}

// printLLMRow emits one CLI's full diagnostic block.
func printLLMRow(out io.Writer, llm cli.LLM, status llmStatus, auth authStatus) {
	displayName := getDisplayName(llm.Name)

	switch status {
	case statusReady:
		fmt.Fprintf(out, "✓ %-15s v%-10s ready\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed:     %s\n", llm.Path)
		fmt.Fprintf(out, "    authenticated: %s\n", auth.detail)

	case statusNotAuthed:
		fmt.Fprintf(out, "⚠ %-15s v%-10s not authenticated\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed: %s\n", llm.Path)
		fmt.Fprintf(out, "    fix:       %s\n", auth.hint)

	case statusBrokenInstall:
		fmt.Fprintf(out, "⚠ %-15s install broken\n", displayName)
		fmt.Fprintf(out, "    found at:  %s\n", llm.Path)
		fmt.Fprintln(out, "    note:      version probe failed; reinstall the CLI")
		printInstallInstructions(out, llm.Name)

	case statusNotInstalled:
		fmt.Fprintf(out, "✗ %-15s not installed\n", displayName)
		printInstallInstructions(out, llm.Name)
	}
	fmt.Fprintln(out)
}

func getDisplayName(name string) string {
	switch name {
	case "claude":
		return "Claude CLI"
	case "gemini":
		return "Gemini CLI"
	case "codex":
		return "Codex CLI"
	default:
		return name
	}
}

func printInstallInstructions(out io.Writer, name string) {
	switch name {
	case "claude":
		fmt.Fprintln(out, "    install:   npm install -g @anthropic-ai/claude-code")
		fmt.Fprintln(out, "    then:      claude login   (free tier — uses your claude.ai account)")
	case "gemini":
		fmt.Fprintln(out, "    install:   npm install -g @google/gemini-cli  (requires Node.js 20+)")
		fmt.Fprintln(out, "    then:      export GEMINI_API_KEY=...   (free at https://aistudio.google.com/apikey)")
		fmt.Fprintln(out, "    or:        gemini /auth   (Google OAuth, free tier)")
	case "codex":
		fmt.Fprintln(out, "    install:   npm install -g @openai/codex")
		fmt.Fprintln(out, "    then:      codex login   (ChatGPT Plus subscription, $20/mo)")
		fmt.Fprintln(out, "    or:        export OPENAI_API_KEY=...   (pay-per-token; usually cheaper for occasional use)")
	}
}

// authStatus describes how an LLM CLI is authenticated, or what the
// user needs to do if it isn't.
type authStatus struct {
	authenticated bool
	detail        string // user-facing description, e.g. "logged in (claude login)"
	hint          string // shown when not authenticated, e.g. "run: claude login"
}

// checkAuth returns the authentication state for a given LLM.
//
// Uniform precedence rule across all three providers: **env-var auth
// is checked first**, then file-based auth. The reasoning:
//   - An exported env var represents the user's *current shell intent*
//     ("use this key right now"), which should win over a persisted
//     OAuth login they may have forgotten about.
//   - Most CLIs themselves apply the same rule when picking credentials.
//   - Without this, doctor would lie when a user with a stale OAuth
//     login exports a fresh API key for testing.
func checkAuth(name string) authStatus {
	switch name {
	case "claude":
		return checkClaudeAuth()
	case "gemini":
		return checkGeminiAuth()
	case "codex":
		return checkCodexAuth()
	default:
		return authStatus{}
	}
}

// authHomeDir returns the directory where each CLI stores its auth.
// Production: $HOME. Tests: set LOCAL_REVIEW_AUTH_HOME to a t.TempDir()
// so the auth checks read fixture files instead of the developer's
// real auth state.
func authHomeDir() string {
	if h := os.Getenv("LOCAL_REVIEW_AUTH_HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func checkClaudeAuth() authStatus {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			detail:        "ANTHROPIC_API_KEY env var set",
		}
	}
	// Claude Code stores tokens in macOS Keychain (or equivalent on
	// other OSes), so there's no credentials file we can read directly.
	// Heuristic: any file under ~/.claude/sessions/ modified within
	// claudeSessionFreshness implies a recent successful authentication.
	// Old-only files (after explicit logout, after a token rotation,
	// etc.) don't count.
	if home := authHomeDir(); home != "" {
		if hasRecentClaudeSession(filepath.Join(home, ".claude", "sessions")) {
			return authStatus{
				authenticated: true,
				detail:        "logged in via 'claude login'",
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          "run 'claude login' (free, uses your claude.ai account) — or export ANTHROPIC_API_KEY=...",
	}
}

// hasRecentClaudeSession returns true when sessionsDir contains any
// regular file modified within claudeSessionFreshness. Used as a proxy
// for "the user has a working Claude login," since the actual token is
// stored in the OS keychain and isn't readable from a file.
func hasRecentClaudeSession(sessionsDir string) bool {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return false
	}
	cutoff := time.Now().Add(-claudeSessionFreshness)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

func checkGeminiAuth() authStatus {
	if os.Getenv("GEMINI_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			detail:        "GEMINI_API_KEY env var set",
		}
	}
	// Gemini CLI stores OAuth state in ~/.gemini/google_accounts.json
	// with an "active" field that's null until the user logs in.
	if home := authHomeDir(); home != "" {
		b, err := os.ReadFile(filepath.Join(home, ".gemini", "google_accounts.json"))
		if err == nil {
			var ga struct {
				Active any `json:"active"`
			}
			if err := json.Unmarshal(b, &ga); err == nil && ga.Active != nil {
				return authStatus{
					authenticated: true,
					detail:        "logged in via Google OAuth",
				}
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          "export GEMINI_API_KEY=... (free at https://aistudio.google.com/apikey) — or run 'gemini /auth' for Google OAuth",
	}
}

func checkCodexAuth() authStatus {
	if os.Getenv("OPENAI_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			detail:        "OPENAI_API_KEY env var set",
		}
	}
	// Codex stores an explicit auth_mode field in ~/.codex/auth.json.
	// Newer versions write "chatgpt" or "api_key"; older versions or
	// hand-edited files may have an empty auth_mode but a non-null
	// OPENAI_API_KEY field — also treat that as authenticated.
	if home := authHomeDir(); home != "" {
		b, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
		if err == nil {
			var a struct {
				AuthMode     string  `json:"auth_mode"`
				OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
			}
			if err := json.Unmarshal(b, &a); err == nil {
				switch a.AuthMode {
				case "chatgpt":
					return authStatus{
						authenticated: true,
						detail:        "logged in via 'codex login' (ChatGPT subscription)",
					}
				case "api_key":
					// auth_mode: "api_key" only counts when the stored
					// key is actually non-empty. A partial / corrupted
					// auth.json with the mode set but the key cleared
					// must not produce a false "authenticated" result.
					if a.OpenAIAPIKey != nil && *a.OpenAIAPIKey != "" {
						return authStatus{
							authenticated: true,
							detail:        "API key configured via 'codex login --api-key'",
						}
					}
				default:
					// Older / hand-edited auth.json files may lack
					// auth_mode but have a stored key. Honor that.
					if a.OpenAIAPIKey != nil && *a.OpenAIAPIKey != "" {
						return authStatus{
							authenticated: true,
							detail:        "API key stored in ~/.codex/auth.json",
						}
					}
				}
			}
		}
	}
	return authStatus{
		authenticated: false,
		hint:          "run 'codex login' (ChatGPT Plus, $20/mo) — or export OPENAI_API_KEY=... (pay-per-token, usually cheaper)",
	}
}
