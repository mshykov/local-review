//go:build e2e

// End-to-end tests: build the real binary, point it at a fake
// OpenAI-compatible LLM, run real CLI commands against a real git repo, and
// assert on stdout/stderr + exit code.
//
// Why this works without any real LLM or network: local-review's
// provider-agent model treats any OpenAI-compatible HTTP endpoint as a
// first-class review agent. An in-process httptest server that speaks
// `GET /v1/models` (readiness probe) and `POST /v1/chat/completions`
// (review + merge) IS a fully legitimate agent — so the binary's entire
// pipeline (config cascade → detect → probe → review → merge → exit gate)
// runs for real, deterministically, offline.
//
// Hermeticity: each run gets an empty $HOME and a minimal env (no inherited
// API-key vars), which neutralizes every real CLI agent (claude/codex/
// gemini/copilot find no auth), and `--only fake` pins the active set. So
// these tests never touch a real LLM or cost anything, even on a dev machine
// with CLIs logged in.
package e2e

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binPath is the compiled binary, built once in TestMain.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lr-e2e-bin")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "local-review")

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		panic(err)
	}
	build := exec.Command("go", "build", "-o", binPath, "./cmd/local-review")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		panic("build failed: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeLLM starts an in-process OpenAI-compatible server that answers the
// readiness probe and returns reviewContent verbatim as the model's reply
// for every chat-completions call (review and, in solo mode, the report).
func fakeLLM(t *testing.T, reviewContent string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"fake-model","object":"model"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-fake",
			"model": "fake-model",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": reviewContent}, "finish_reason": "stop"},
			},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newRepoWithStagedChange creates a temp git repo with one commit plus a
// staged modification, so `local-review staged` has a diff to review.
func newRepoWithStagedChange(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		// Commit signing is on globally for this user; disable it locally
		// so the fixture commit doesn't try to invoke an SSH/GPG signer.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	runGit("config", "commit.gpgsign", "false")

	src := filepath.Join(repo, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("commit", "-q", "-m", "init")

	// Staged change for `staged` to pick up.
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() { _ = 1 / 0 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	return repo
}

// writeUserConfig writes ~/.local-review.yml (the trusted user-home layer)
// pointing a provider agent named "fake" at the server. 127.0.0.1 is a
// local URL, so no API key is required.
func writeUserConfig(t *testing.T, home, serverURL string) {
	t.Helper()
	cfg := "llms:\n  fake:\n    base_url: " + serverURL + "/v1\n    model: fake-model\n"
	if err := os.WriteFile(filepath.Join(home, ".local-review.yml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runLR runs the binary in repoDir with an isolated empty home and a minimal
// env (no inherited API-key vars), returning combined output and exit code.
func runLR(t *testing.T, repoDir, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = repoDir
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("running %v: %v\n%s", args, err, out)
	return "", -1
}

func TestE2E_StagedBlockingFindingExitsTwo(t *testing.T) {
	srv := fakeLLM(t, "## Critical Issues\n\n- **main.go:3** — division by zero will panic at runtime.\n  Fix: guard the divisor.\n\n## Major Issues\n*(None)*\n")
	home := t.TempDir()
	writeUserConfig(t, home, srv.URL)
	repo := newRepoWithStagedChange(t)

	out, code := runLR(t, repo, home, "staged", "--only", "fake")
	if code != 2 {
		t.Fatalf("expected exit 2 (blocking findings), got %d\noutput:\n%s", code, out)
	}
	if !strings.Contains(out, "Critical") {
		t.Errorf("expected the critical finding in output, got:\n%s", out)
	}
}

func TestE2E_StagedCleanReviewExitsZero(t *testing.T) {
	// Trailing prose must NOT sit under a severity header, or the gate's
	// section heuristic attributes it to that section and treats the report
	// as non-empty (this is exactly the kind of thing the e2e caught).
	srv := fakeLLM(t, "## Summary\nLooks good — no blocking issues.\n\n## Critical Issues\n*(None)*\n\n## Major Issues\n*(None)*\n")
	home := t.TempDir()
	writeUserConfig(t, home, srv.URL)
	repo := newRepoWithStagedChange(t)

	out, code := runLR(t, repo, home, "staged", "--only", "fake")
	if code != 0 {
		t.Fatalf("expected exit 0 (clean review), got %d\noutput:\n%s", code, out)
	}
}

func TestE2E_VersionSmoke(t *testing.T) {
	out, code := runLR(t, t.TempDir(), t.TempDir(), "version")
	if code != 0 {
		t.Fatalf("version exited %d:\n%s", code, out)
	}
	if !strings.Contains(strings.ToLower(out), "local-review") {
		t.Errorf("version output missing tool name:\n%s", out)
	}
}

func TestE2E_DoctorShowsConfiguredProvider(t *testing.T) {
	srv := fakeLLM(t, "")
	home := t.TempDir()
	writeUserConfig(t, home, srv.URL)

	// doctor HTTP-probes the configured provider against the fake /v1/models.
	out, _ := runLR(t, t.TempDir(), home, "doctor")
	if !strings.Contains(out, "fake") {
		t.Errorf("doctor should list the configured 'fake' provider, got:\n%s", out)
	}
}
