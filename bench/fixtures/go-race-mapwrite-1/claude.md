## Critical Issues

- `server/cache.go:22` — **Data race: write to `s.entries` from a
  goroutine without holding `s.mu`**. `Cache.Get` reads `s.entries`
  under the lock, but `Refresh` now spawns a goroutine that writes
  unsynchronised. Under `go test -race` this fails; in production
  the map can be torn or panic ("concurrent map writes").
  *Suggested fix:* take `s.mu.Lock()`/`Unlock()` inside the goroutine
  body, or do the write synchronously and let the caller decide
  whether to background it.

## Major Issues

*(None)*

## Warnings

- `server/cache.go:21` — `ctx` is accepted but never observed. If the
  caller cancels, the goroutine still races to completion. Consider
  honouring ctx, or drop the parameter.

## Info / Notes

*(None)*
