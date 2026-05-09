## Major Issues

- `store/save.go:19` — **Encoder error swallowed**. The new code wraps
  `Encode(p)` in `if err := ...; err != nil { return nil }`. Any
  encoder failure is converted into a successful return. The
  unconditional `return err` on the next line fires only when the
  encoder succeeded — at which point `err` is the outer nil. The
  function now reports success on every call.
  *Suggested fix:* `return json.NewEncoder(f).Encode(p)` (the original
  shape), or capture the error into the outer scope and return it.

## Warnings

*(None)*

## Info / Notes

*(None)*
