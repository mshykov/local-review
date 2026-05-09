## Critical Issues

- src/ffi.rs:9 — `from_utf8_unchecked` skips validation; combined with
  the removed null check on `ptr`, any malformed input from C is UB.
  Use `from_utf8` (returns Result) and bail on the Err arm, and
  re-add the null guard before constructing the slice.

## Major Issues

*(None)*

## Warnings

*(None)*

## Info / Notes

- src/ffi.rs:5 — function signature looks fine; ptr-and-len pattern
  is appropriate for FFI here.
