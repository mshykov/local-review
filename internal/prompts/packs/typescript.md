# TypeScript / JavaScript review pack

Apply the default review rules. Plus: language-specific patterns to look for.

## High-signal TypeScript pitfalls

- **`any` and `as` casts** — flag when they hide a real type issue. `as unknown as X` is a code smell.
- **Async without `await`** — promises returned but not awaited; missing error propagation.
- **`useEffect` deps** — missing or stale deps in React; forgotten cleanup; effects that should be `useMemo`/`useCallback`.
- **`==` vs `===`** — always prefer strict equality unless there's an explicit reason.
- **Floating promises** — `void` is fine to silence, but only when the caller really doesn't care.
- **Mutating function arguments** — especially default parameters or destructured options.
- **`JSON.parse` without try/catch** when input is untrusted.
- **Off-by-one in array slicing**; `splice` vs `slice` confusion.
- **Re-throwing without context** — `throw err` where `throw new Error('doing X', { cause: err })` would help.
- **Optional chaining hiding bugs** — `obj?.a?.b ?? 0` when `0` masks a real "missing" case.

## Node specifics

- Streams: forgetting `error` handlers; `pipe` without backpressure awareness.
- `process.env` reads scattered through code (should be centralised + validated).
- Synchronous fs in request paths.

## React / Next specifics

- Props drilling that should be context.
- Server vs client component boundaries (`'use client'` appearing where it shouldn't).
- Hydration mismatches (date/time formatting on server vs client).
- Suspense boundaries swallowing errors.

## Output

Same JSON shape as the default pack. Use `tag: "correctness"`, `"security"`, `"perf"`, `"maintainability"`, or `"style"`.
