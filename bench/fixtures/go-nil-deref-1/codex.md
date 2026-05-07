## Major Issues

- store/user_store.go:24 — `u` may be nil here because the caller
  removed the early return after `fetchUser` errors. Accessing
  `u.ID` and `u.Email` will panic on nil. Restore the early return.

## Warnings

*(None)*

## Info / Notes

- store/user_store.go:30 — consider returning the wrapped error
  instead of just logging — silent failures will accumulate.
