# Contributing to local-review

Thanks for considering a contribution. local-review is small on purpose — please keep changes tight.

## Layout

```
cmd/local-review/  CLI entry point (cobra). Thin wrapper around internal/.
  main.go          Root command + shared flag plumbing
  staged|commit|branch  Defined inline in main.go
  multi.go         Parallel multi-LLM review (`local-review multi …`)
  doctor.go        Check LLM CLI installations + auth
  init.go          Interactive .local-review.yml scaffolding wizard
  config.go        Print resolved config (API keys masked)
  version.go       Print version (set via -ldflags at build time)

internal/
  config/          YAML cascade loader (defaults → ~/.yml → ./.yml → flags)
  git/             Diff extraction (shells out to `git`)
  lang/            File-extension → language identifier
  llm/             OpenAI-compat HTTP client (no vendor SDKs)
  cli/             LLM CLI detection + invocation (claude, gemini, codex)
  multi/           Multi-LLM orchestration, merging, storage
  prompts/         Embedded prompt packs (`go:embed packs/*.md`)
  review/          Orchestration: diff → LLM → filtered findings
  output/          Terminal + JSON formatters

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

# Single-LLM mode (uses provider API)
./local-review staged

# Multi-LLM mode (uses installed LLM CLIs in parallel)
./local-review multi staged
./local-review doctor
```

Required:
- Go 1.23+
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

## CI

`.github/workflows/ci.yml` runs `go vet`, `go test -race`, and a build on every push. PRs must be green.

## Releases

Tag `vX.Y.Z` on `main`. The release workflow cross-compiles binaries for darwin/linux/windows × amd64/arm64 and attaches them to a GitHub Release.

## License

By contributing, you agree your contributions are MIT-licensed.
