# Contributing to local-review

Thanks for considering a contribution. local-review is small on purpose — please keep changes tight.

## Layout

```
cmd/local-review/        CLI entry point (cobra). Thin wrapper.
internal/
  config/          YAML cascade loader
  git/             Diff extraction (shells out to `git`)
  lang/            File-extension → language identifier
  llm/             OpenAI-compat HTTP client (no vendor SDKs)
  prompts/         Embedded prompt packs (markdown files in packs/)
  review/          Orchestration: diff → LLM → filtered findings
  output/          Terminal + JSON formatters
.github/workflows/ CI + release pipelines
examples/          Sample .local-review.yml + pre-commit hook
docs/              Internals docs (prompt-pack authoring, etc.)
install.sh         One-line installer
```

## Local dev

```sh
git clone https://github.com/mshykov/local-review
cd local-review
go test ./...
go build -o local-review ./cmd/local-review
./local-review staged
```

Required: Go 1.23+. No other tooling.

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
