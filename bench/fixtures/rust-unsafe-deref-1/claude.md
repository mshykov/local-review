## Critical Issues

- `src/ffi.rs:9` — **Unchecked null deref + UTF-8 invariant violation**.
  The diff drops the `ptr.is_null()` guard, so `slice::from_raw_parts(ptr, len)`
  builds a slice from a possibly-null pointer (UB). It also swaps
  `from_utf8` (validating) for `from_utf8_unchecked` (assumes valid UTF-8) —
  any non-UTF-8 byte sequence from the C caller is now undefined behaviour.
  *Suggested fix:* restore the null check; keep `from_utf8`; on validation
  error return `std::ptr::null()` rather than panicking.

## Major Issues

*(None)*

## Warnings

- `src/ffi.rs:12` — `s.as_ptr()` returns a pointer with the lifetime of
  the temporary `s`, which is dropped at function return. Even with valid
  inputs this is a use-after-free for the C caller. Worth flagging as a
  separate bug.

## Info / Notes

*(None)*
