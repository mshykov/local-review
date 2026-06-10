# Testing

How testing works for `local-review`.

## Strategy

- **Unit tests are the bulk of coverage** — fast, isolated, table-driven.
- **Bug fixes ship with a regression test** that would have caught the bug.
- **Test behavior, not implementation.** A refactor that preserves behavior must not
  break the test; if a test can't fail when the business rule changes, it isn't
  testing the rule. Name tests `Test<Thing>_<Scenario>_<Outcome>`
  (e.g. `TestSelectAuditLLM_EmptyActive_ReturnsHintError`), not `Test_Foo_2`.
- **Deterministic.** No real network, time, or randomness in unit tests; no `time.Sleep`.
  A flaky test is fixed or quarantined, never ignored. Tests that read host state
  (e.g. `$HOME`) isolate it to a temp dir.
- **Mock at the boundary** (the LLM CLI invoker / HTTP client), never hit a real LLM
  or paid API in a unit test.
- New logic needs a test; coverage shouldn't decrease. Green `-race` is required
  before merge.

## Running tests

```sh
go test ./...            # local
go test -race ./...      # CI standard (run before pushing)
```

CI (`.github/workflows/ci.yml`) runs `go test -race ./...` on every push.

## End-to-end tests

The `e2e/` package drives the **real compiled binary** as a subprocess against a
fake OpenAI-compatible LLM and asserts on CLI output + exit code. It's gated
behind the `e2e` build tag (so the default `go test ./...` stays fast and
doesn't shell out), and CI runs it on every push/PR — which also gates the
release PR, giving every release an end-to-end smoke.

```sh
go test -tags e2e ./e2e/...
```

**How it stays hermetic and offline:** local-review's provider-agent model
treats any OpenAI-compatible HTTP endpoint as a first-class review agent, so an
in-process `httptest` server speaking `GET /v1/models` (readiness probe) and
`POST /v1/chat/completions` (review + merge) *is* a real agent — the whole
pipeline (config cascade → detect → probe → review → merge → exit gate) runs for
real, deterministically, with no LLM call. Each run uses an **empty `$HOME` and a
minimal env** (no inherited API-key vars), which neutralizes every real CLI agent
(claude/codex/gemini/copilot find no auth), and `--only fake` pins the active set
— so the tests never touch a real LLM or cost anything, even on a developer
machine with CLIs logged in.

Covered today: blocking review → exit 2, clean review → exit 0, `version`, and
`doctor` listing the configured provider. Adding a case = add a `fakeLLM(...)`
response + a `runLR(...)` assertion (see `e2e/e2e_test.go`).

## Known gap

`internal/llm/`'s raw-HTTP client has limited **unit**-level coverage (no mock
transport for the error/retry branches). The e2e suite now exercises its
happy-path round-trip (request shape, response parse, usage) against the fake
server, but unit tests for the failure modes (non-2xx bodies, malformed JSON,
truncation) are still welcome.
