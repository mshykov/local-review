# TypeScript / JavaScript review pack

Apply the default review rules. Plus: language-specific patterns to look for.

## High-signal TypeScript pitfalls

### Type safety
- **`any` and `as` casts** — flag when they hide a real type issue. `as unknown as X` is a code smell.
- **Non-null assertions (`!`)** — risky unless you've proven the value exists.
- **Missing return types** — explicit return types catch mistakes and serve as documentation.
- **Type assertions ignoring errors** — `as` shouldn't silence type incompatibilities.
- **`unknown` vs `any`** — prefer `unknown` for truly unknown types (forces type checking).
- **Generic constraints too loose** — `<T>` when `<T extends SomeType>` is more accurate.

### Async & promises
- **Async without `await`** — promises returned but not awaited; missing error propagation.
- **Floating promises** — `void` is fine to silence intentional fire-and-forget, but risky otherwise.
- **Missing error handling** — `.catch()` or `try/catch` for all promises.
- **`Promise.all` without error handling** — one rejection fails all; consider `Promise.allSettled`.
- **Async function in `useEffect`** (React) — can't return async function; use IIFE or separate function.
- **Race conditions** — multiple async calls updating same state without coordination.

### Equality & comparisons
- **`==` vs `===`** — always prefer strict equality unless there's an explicit reason.
- **NaN comparisons** — use `Number.isNaN()`, not `=== NaN`.
- **Object comparison** — `obj1 === obj2` only checks reference, not deep equality.

### Data handling
- **Mutating function arguments** — especially default parameters or destructured options.
- **`JSON.parse` without try/catch** when input is untrusted.
- **Off-by-one in array slicing**; `splice` vs `slice` confusion.
- **Array.prototype methods on nullable** — `.map/.filter` on potentially undefined array.
- **Optional chaining hiding bugs** — `obj?.a?.b ?? 0` when `0` masks a real "missing" case.
- **Destructuring defaults with null** — `const { x = 0 } = obj` won't apply if `obj.x` is `null` (only `undefined`); use `?? 0` after destructuring if `null` is possible.

### Error handling
- **Re-throwing without context** — `throw err` where `throw new Error('doing X', { cause: err })` adds context.
- **Empty catch blocks** — at minimum, log the error.
- **Generic catch-all** — `catch (e)` should narrow type before use (`e instanceof Error`).
- **Errors in callbacks** — forgotten error parameter in Node callbacks.

## Node.js specifics

### Core patterns
- **Streams: missing `error` handlers** — unhandled stream errors crash the process.
- **`pipe` without backpressure awareness** — can cause memory issues.
- **`process.env` reads scattered** — should be centralized and validated at startup.
- **Synchronous fs in request paths** — `fs.readFileSync` blocks the event loop.
- **Missing `process.on('unhandledRejection')`** — uncaught promise rejections.
- **Callbacks without error-first convention** — `(err, data) => {}` pattern expected.

### Performance
- **Blocking operations in event loop** — CPU-intensive tasks should use worker threads.
- **Memory leaks** — event listeners not removed, growing caches, circular references.
- **Missing connection pooling** — creating new DB connections per request.

## React / Next.js specifics

### Component design
- **Props drilling** — passing props through 3+ levels; should use context or composition.
- **Missing `key` prop** — in lists, or using index as key (anti-pattern).
- **Inline function definitions** — in JSX creates new reference every render (unless needed).
- **Mutating state directly** — `state.x = y` instead of `setState`.
- **Derived state** — storing computed values in state instead of using `useMemo`.

### Hooks
- **`useEffect` dependency issues**:
  - Missing dependencies (stale closures)
  - Unnecessary dependencies causing infinite loops
  - Missing cleanup functions (subscriptions, timers, event listeners)
  - Effects that should be `useMemo` or `useCallback`
- **`useState` vs `useRef`** — using state when ref is appropriate (no re-render needed).
- **Custom hooks not starting with `use`** — breaks React linting rules.
- **Hooks called conditionally** — violates Rules of Hooks.

### Next.js patterns
- **Server vs client boundaries** — `'use client'` appearing where it shouldn't.
- **Hydration mismatches** — date/time formatting differs between server and client.
- **Suspense boundaries swallowing errors** — need error boundaries too.
- **Missing `metadata` exports** — SEO metadata should be exported from pages.
- **Data fetching in client components** — should use server components when possible.
- **Image optimization** — using `<img>` instead of `<Image>` from `next/image`.
- **Route handlers not using Web API** — should return `Response` objects.

### Performance
- **Unnecessary re-renders** — missing `React.memo`, `useMemo`, `useCallback`.
- **Large bundle sizes** — missing dynamic imports / code splitting.
- **Un-optimized images** — missing width/height, wrong format (use WebP/AVIF).
- **Excessive client-side JS** — logic that should run on server.
- **Missing lazy loading** — components/images not using `React.lazy()` or `loading="lazy"`.

### Security
- **Dangerously set inner HTML** — XSS risk; validate/sanitize content.
- **Client-side secrets** — API keys exposed in client bundle.
- **CORS misconfiguration** — overly permissive origins.

## Output

Same JSON shape as the default pack. Use `tag: "correctness"`, `"security"`, `"perf"`, `"maintainability"`, or `"style"`.
