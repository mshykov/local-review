# Go review pack

Apply the default review rules. Plus: Go-specific patterns to look for.

## High-signal Go pitfalls

### Error handling
- **Unchecked errors** — `_ = f()` or assignment-only without inspection. Always flag for non-trivial calls.
- **`fmt.Errorf` without `%w`** when wrapping errors that should be unwrappable.
- **Error swallowing** — logging errors but not propagating them when callers need to know.
- **`errors.Is/As` misuse** — comparing error types incorrectly.
- **`panic` for control flow** — should be `error` unless truly unrecoverable.
- **Missing error context** — wrapped errors should explain *where* the failure occurred.

### Concurrency
- **Goroutine leaks** — channels/contexts that prevent goroutines from ever returning.
- **`defer` in loops** — accumulates resources until function exit; usually wrong.
- **Mutex copied** — value receiver on a method whose struct embeds `sync.Mutex` or `sync.RWMutex`.
- **Race conditions** — shared state without synchronization (mutexes or channels).
- **WaitGroup misuse** — calling `Add` inside goroutine instead of before launch.
- **Channel without counterpart** — send/receive without guaranteed receiver/sender; goroutine waits forever (common when goroutine exits before partner).
- **Select without default** — can deadlock if no case is ready.

### Context handling
- **`context.Background()` in request paths** — should propagate the caller's ctx.
- **Context not passed as first param** — violates Go convention.
- **Context stored in struct** — contexts should be passed explicitly, not stored.
- **Ignoring context cancellation** — loops/operations not checking `ctx.Done()`.

### Memory & performance
- **Slice aliasing** — appending to a slice param can mutate the caller's underlying array.
- **Slice/map pre-allocation** — `make([]T, 0, knownSize)` for known sizes prevents reallocation.
- **String concatenation in loops** — use `strings.Builder` for efficiency.
- **`defer` in tight loops** — performance overhead; consider manual cleanup.
- **Pointer vs value receivers** — large structs should use pointer receivers; small immutable types use values.

### Data structures
- **`map` access without ok-comma** when zero value is meaningful or when checking existence.
- **Map concurrency** — maps are not safe for concurrent read+write without `sync.RWMutex` or `sync.Map`.
- **Range loop variable captured** — closure over loop var prior to Go 1.22 semantics; still worth noting.

### HTTP & network
- **HTTP handlers writing after error** — missing `return` after `http.Error`.
- **Response body not closed** — for outbound HTTP calls, always `defer resp.Body.Close()` on the response returned by `http.Client.Do`; the server's `r.Body` in handlers is managed by the framework.
- **Context not passed to HTTP requests** — use `req.WithContext(ctx)`.
- **Timeouts missing** — HTTP clients should set timeouts to prevent hanging.

### Time handling
- **Time without `time.Time.UTC()`** when persisting; timezone confusion.
- **`time.After` in loops** — creates timer leak; use `time.NewTimer` with `defer timer.Stop()`.
- **Duration parsing** — prefer `time.ParseDuration` over manual calculations.

## Idioms & style

- **Receiver naming** — one or two letters, consistent across methods on the same type.
- **Exported identifiers without doc comments** — all public APIs should have docs.
- **`interface{}` / `any`** — prefer `any` (Go 1.18+), but flag when constraints should be more specific.
- **Interface design** — prefer small interfaces (1-3 methods); accept interfaces, return concrete types.
- **Variable shadowing** — `:=` accidentally shadowing outer scope variables (especially `err`).
- **Unnecessary else** — after `return`, don't use `else` (Go idiom: early return).

## Database patterns

- **`*sql.Rows` not closed** — missing `defer rows.Close()`.
- **Transaction not rolled back** — missing `defer tx.Rollback()` on error path (commit clears it).
- **SQL injection** — string concatenation in queries instead of parameterization.
- **Connection pool exhaustion** — not returning connections (close rows/statements).
- **NULL handling** — use `sql.Null*` types for nullable columns.

## Testing patterns

- **Table-driven tests** — multiple similar test cases should use `[]struct` pattern.
- **Test helpers without `t.Helper()`** — line numbers point to helper instead of caller.
- **Parallel tests** — `t.Parallel()` should be called early, and tests must be independent.
- **Cleanup not deferred** — use `t.Cleanup()` for test teardown.
- **Global state in tests** — causes flaky tests when run in parallel.

