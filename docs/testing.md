# Testing

How testing works for `local-review`.

## Strategy

- **Unit tests are the bulk of coverage** — fast, isolated, table-driven.
- **Bug fixes ship with a regression test** that would have caught the bug.
- **Test behavior, not implementation.** A refactor that preserves behavior must not
  break the test; if a test can't fail when the business rule changes, it isn't
  testing the rule. Name tests as scenarios (`Test_FooReturnsErrorOnEmptyInput`), not
  `Test_Foo_2`.
- **Deterministic.** No real network, time, or randomness in unit tests; no `sleep()`.
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

## Known gap

`internal/llm/` is intentionally **not mocked yet** — its raw-HTTP client has no
test double, so its behavior isn't exercised in unit tests. This is a known gap,
not an oversight. Contributions adding a mock transport (or an httptest-based
harness) are welcome.
