## Warnings

- store/user_store.go:30 — `log.Printf` does not include the request
  context. Consider passing `ctx` so logs are correlated.
- store/user_store.go:14 — the SQL string is a literal but you may
  want to use a constant for the query for testability.

## Info / Notes

- store/user_store.go:1 — package-level docstring would help.
