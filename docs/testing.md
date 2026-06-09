> Baseline: `MSH/docs/testing.md` (org common rules). Below: local-review-specific rules.

# Testing

local-review-specific testing rules. The org baseline covers general testing
philosophy; this file holds what's particular to this Go CLI.

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
