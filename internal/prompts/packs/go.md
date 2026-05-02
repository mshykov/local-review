# Go review pack

Apply the default review rules. Plus: Go-specific patterns to look for.

## High-signal Go pitfalls

- **Unchecked errors** — `_ = f()` or assignment-only without inspection. Always flag for non-trivial calls.
- **`defer` in loops** — accumulates resources until function exit; usually wrong.
- **Goroutine leaks** — channels/contexts that prevent goroutines from ever returning.
- **`context.Background()` in request paths** — should propagate the caller's ctx.
- **Mutex copied** — value receiver on a method whose struct embeds `sync.Mutex`.
- **Range loop variable captured** — closure over loop var prior to Go 1.22 semantics; still worth noting on shared codebases.
- **Time without `time.Time.UTC()`** when persisting; timezone confusion.
- **`fmt.Errorf` without `%w`** when wrapping errors that should be unwrappable.
- **Slice aliasing** — appending to a slice param can mutate the caller's underlying array.
- **`map` access without ok-comma** when zero value is a meaningful key value.
- **HTTP handlers writing after error** — returning without `return` after `http.Error`.
- **`panic` for control flow** — should be `error` unless truly unrecoverable.

## Idioms

- Receiver naming (one or two letters, consistent across methods on the same type).
- Exported identifiers without doc comments.
- `interface{}` (use `any` post Go 1.18) — but flag when the constraint should be more specific.
- `sql.DB` usage: `*sql.Rows` not closed, transaction not rolled back on error path.

## Output

Same JSON shape as the default pack.
