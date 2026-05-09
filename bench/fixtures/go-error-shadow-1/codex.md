## Major Issues

- store/save.go:19 — replacing `return Encode(p)` with `if err := ...
  Encode(p); err != nil { return nil }` swallows encoder failures.
  Save() will now always report nil because the unconditional
  `return err` only fires on encoder success (where err is the outer
  nil). Restore the direct return.

## Warnings

*(None)*

## Info / Notes

*(None)*
