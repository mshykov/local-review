## Critical Issues

- server/cache.go:23 — concurrent write to s.entries without holding
  the mutex. Get() reads under lock; the goroutine here will race
  against any active read. Acquire s.mu inside the goroutine, or
  remove the goroutine and write synchronously.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

*(None)*
