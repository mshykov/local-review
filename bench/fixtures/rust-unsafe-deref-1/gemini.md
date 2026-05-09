## Warnings

- src/ffi.rs:5 — `extern "C"` functions returning a pointer should
  document lifetime expectations.
- src/ffi.rs:1 — `c_uchar` import is unused if you switch to `u8` —
  consider consolidating.

## Info / Notes

- src/ffi.rs:9 — `unsafe` block is slightly larger than necessary;
  could narrow to just the `from_raw_parts` call.
