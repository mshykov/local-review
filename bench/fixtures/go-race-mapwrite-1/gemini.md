## Warnings

- server/cache.go:21 — Refresh accepts ctx but doesn't use it.
- server/cache.go:1 — package comment is missing.

## Info / Notes

- server/cache.go:13 — `Get` returns (string, bool) which is fine but
  consider returning (value, error) for richer call sites.
