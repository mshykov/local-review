package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mshykov/local-review/internal/cli"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check LLM CLI installations and authentication status",
		Long: `Doctor checks each LLM CLI's installation, version, and authentication state.

It detects:
  - Claude CLI (claude) — auth via 'claude login' or ANTHROPIC_API_KEY
  - Gemini CLI (gemini) — auth via 'gemini /auth' (Google OAuth) or GEMINI_API_KEY
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
func runDoctor(out io.Writer) error {
	fmt.Fprintln(out, "Checking LLM installations and authentication...")
	fmt.Fprintln(out)

	llms := cli.DetectAll()

	readyCount := 0
	for _, llm := range llms {
		status := classify(llm)
		if status == statusReady {
			readyCount++
		}
		printLLMRow(out, llm, status)
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%d/%d LLMs ready for multi-review.\n", readyCount, len(llms))
	return nil
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

func classify(llm cli.LLM) llmStatus {
	if !llm.Available {
		if llm.Path != "" {
			return statusBrokenInstall
		}
		return statusNotInstalled
	}
	if !checkAuth(llm.Name).authenticated {
		return statusNotAuthed
	}
	return statusReady
}

// printLLMRow emits one CLI's full diagnostic block.
func printLLMRow(out io.Writer, llm cli.LLM, status llmStatus) {
	displayName := getDisplayName(llm.Name)

	switch status {
	case statusReady:
		auth := checkAuth(llm.Name)
		fmt.Fprintf(out, "✓ %-15s v%-10s ready\n", displayName, llm.Version)
		fmt.Fprintf(out, "    installed:     %s\n", llm.Path)
		fmt.Fprintf(out, "    authenticated: %s\n", auth.detail)

	case statusNotAuthed:
		auth := checkAuth(llm.Name)
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
		fmt.Fprintln(out, "    then:      gemini /auth   (Google OAuth, free tier)")
		fmt.Fprintln(out, "    or:        export GEMINI_API_KEY=...   (free at https://aistudio.google.com/apikey)")
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
	method        string // "oauth", "api_key", or empty
	detail        string // user-facing description, e.g. "logged in (claude login)"
	hint          string // shown when not authenticated, e.g. "run: claude login"
}

// checkAuth returns the authentication state for a given LLM. The
// checks are file-based heuristics where possible (fast, offline) and
// fall back to env-var presence. False negatives are possible but
// false positives shouldn't happen — we never claim auth without
// concrete evidence.
//
// authPathOverride is set in tests (via $LOCAL_REVIEW_AUTH_HOME) to
// point checks at a temp dir instead of the real $HOME.
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
// In production this is $HOME; tests override via $LOCAL_REVIEW_AUTH_HOME
// so we can put a fake auth file under a t.TempDir() and assert behavior.
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
	// Claude Code stores tokens in macOS Keychain (or equivalent on
	// other OSes), so there's no credentials file we can read directly.
	// Heuristic: if ~/.claude/sessions/ has any file, the user has
	// successfully authenticated at some point. Not perfect (can be
	// stale after manual logout), but better than the previous
	// "binary found = ready" claim.
	if home := authHomeDir(); home != "" {
		sessions := filepath.Join(home, ".claude", "sessions")
		if entries, err := os.ReadDir(sessions); err == nil && len(entries) > 0 {
			return authStatus{
				authenticated: true,
				method:        "oauth",
				detail:        "logged in via 'claude login'",
			}
		}
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			method:        "api_key",
			detail:        "ANTHROPIC_API_KEY env var set",
		}
	}
	return authStatus{
		authenticated: false,
		hint:          "run 'claude login' (free, uses your claude.ai account) — or export ANTHROPIC_API_KEY=...",
	}
}

func checkGeminiAuth() authStatus {
	if os.Getenv("GEMINI_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			method:        "api_key",
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
					method:        "oauth",
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
	// Codex stores an explicit auth_mode field in ~/.codex/auth.json.
	// "chatgpt" = OAuth via codex login; "api_key" = API key configured.
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
						method:        "oauth",
						detail:        "logged in via 'codex login' (ChatGPT subscription)",
					}
				case "api_key":
					return authStatus{
						authenticated: true,
						method:        "api_key",
						detail:        "API key configured via 'codex login --api-key'",
					}
				}
			}
		}
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return authStatus{
			authenticated: true,
			method:        "api_key",
			detail:        "OPENAI_API_KEY env var set",
		}
	}
	return authStatus{
		authenticated: false,
		hint:          "run 'codex login' (ChatGPT Plus, $20/mo) — or export OPENAI_API_KEY=... (pay-per-token, usually cheaper)",
	}
}
