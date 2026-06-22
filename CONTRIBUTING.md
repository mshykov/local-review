# Contributing to local-review

Thanks for considering a contribution. local-review is small on purpose — please keep changes tight.

## Layout

```
cmd/local-review/  CLI entry point (cobra). Thin wrapper around internal/.
  main.go          Root command + shared flag plumbing + ASCII banner
  runner.go        Multi-agent fan-out dispatcher (CLI agents + provider agents)
  doctor.go        Check LLM CLI installations + auth + provider reachability
  init.go          Interactive .local-review.yml scaffolding wizard
  config.go        Print resolved config (API keys + URL credentials masked)
  audit.go         `audit` subcommand (single-LLM, --with <agent>)
  bench.go         `bench` subcommand (regression dataset, contributor tooling)
  version.go       Print version (set via -ldflags at build time)

internal/
  config/          YAML cascade loader (defaults → ~/.yml → ./.yml → flags); rejects the removed v0 `provider:` block with a migration error
  git/             Diff extraction (shells out to `git`)
  lang/            File-extension → language identifier
  llm/             OpenAI-compat HTTP client (no vendor SDKs); used by internal/agents/provider
  agents/          Invoker contract + TokenUsage type — shared by CLI and provider invokers
  agents/provider/ HTTP provider agent (Ollama / vLLM / OpenAI-compat) — implements agents.Invoker via internal/llm
  cli/             LLM CLI detection + invocation (claude / codex / copilot / gemini); GeminiSunsetDate constant lives here
  multi/           Multi-agent orchestration, merging, on-disk storage
  prompts/         Embedded prompt packs (`go:embed packs/*.md`) + audit packs
  review/          Diff-filter helpers (FilterDiffs, glob matchers) + structured Severity/Finding/Report types
  output/          Terminal + JSON formatters
  audit/           Walker (git ls-files), chunk packer, run orchestrator, report renderer

.github/workflows/ CI + the consolidated release pipeline (release.yml)
examples/          Sample configs (one per provider) + pre-commit hook
docs/              Public docs (served via GitHub Pages)
install.sh         One-line installer
```

## Local dev

```sh
git clone https://github.com/mshykov/local-review
cd local-review
go test ./...
go build -o local-review ./cmd/local-review

# Multi-LLM is the default — every authenticated LLM CLI runs in parallel
./local-review review

# Or use a specific scope
./local-review staged
./local-review doctor
```

Required:
- Go 1.26+
- Node.js 20+ and npm — only if you want to test the multi-LLM mode locally

### Multi-LLM development setup

Install LLM CLIs for testing multi-LLM features:

```sh
# Install Node.js via Homebrew (macOS)
brew install node

# Install LLM CLIs
npm install -g @google/gemini-cli
npm install -g @openai/codex
npm install -g @anthropic-ai/claude-code
```

Note: You don't need all 3 LLMs installed to develop. The code gracefully handles missing CLIs.

## Adding a prompt pack

Each pack is a markdown file under `internal/prompts/packs/`. The filename is the language id (matching `internal/lang/detect.go`), e.g. `rust.md`.

A pack is a system prompt. Keep it focused — high-signal language pitfalls, severity tiers, and the JSON output shape.

After adding a pack:

```sh
go test ./internal/prompts/...
```

The test suite verifies every pack file loads. Add a case to `internal/lang/detect.go` if you're handling a new file extension.

## Style

- Standard Go: `gofmt -s` + `go vet`.
- Keep `internal/llm/client.go` SDK-free. local-review is one binary because it doesn't pull provider SDKs.
- Comments explain *why* (intent, constraint, trade-off) — never restate what the code does.
- One-line JSDoc-style header comments on exported functions/types only.

## Tests

- Pure logic gets a unit test. We won't merge new behaviour without one.
- `internal/llm/` is intentionally not mocked in tests yet — that's a gap, contributions welcome.

## Secrets & personal data — install the pre-commit hook

Before your first commit, install the local guard:

```sh
./scripts/install-hooks.sh
```

This sets up a `pre-commit` hook that blocks committing:

- **Secrets** — tokens, API keys, passwords, private keys — via [`gitleaks`](https://github.com/gitleaks/gitleaks) over your staged diff (honors `.gitleaks.toml`). Install gitleaks so the local half runs: `go install github.com/zricethezav/gitleaks/v8@v8.30.1` (or `brew install gitleaks`).
- **Personal data** — your own IPs, hostnames, emails, names — via a **gitignored** `.git-personal-denylist` (seeded from `.git-personal-denylist.example`). Add your real values there; they stay on your machine, and the hook blocks any commit that contains them.

**Use neutral examples in tests and docs** — `192.0.2.x` / `198.51.100.x` (RFC 5737 documentation ranges), `test@example.com`, `ghp_example`. Never paste a real personal value, even in a test fixture; git history is forever. The secret-scan CI job enforces the gitleaks half on every PR regardless, but the hook catches it before it leaves your machine (and CI can't hold your personal denylist — that's local-only).

## CI

`.github/workflows/ci.yml` runs `go vet`, `go test -race`, and a build on every push. `.github/workflows/secret-scan.yml` runs `gitleaks` over the full git history. PRs must be green.

## Releases

Tag `vX.Y.Z` on `main`. The release workflow cross-compiles binaries for darwin/linux/windows × amd64/arm64 and attaches them to a GitHub Release.

## License

By contributing, you agree your contributions are MIT-licensed.
