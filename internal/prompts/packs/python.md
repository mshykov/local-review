# Python review pack

Apply the default review rules. Plus: Python-specific patterns to look for.

## High-signal Python pitfalls

- **Mutable default arguments** — `def f(x=[]):` is the classic bug.
- **Bare `except:`** — swallows `SystemExit` / `KeyboardInterrupt`; almost always wrong.
- **Catching `Exception` too broadly** when a specific subclass would do.
- **`==` with `None`** — should be `is None` / `is not None`.
- **`open()` without context manager** — files leaking on error paths.
- **f-string injection** in SQL or shell.
- **`subprocess` with `shell=True`** on user-controlled input.
- **`pickle.loads` on untrusted data**.
- **Async without `await`** in `async def` functions.
- **`asyncio.run()` inside a function called from already-running loop**.
- **Typos in `__init__` / dunder methods** — silently doesn't override.
- **`for x in dict:` then mutating dict** — RuntimeError waiting to happen.
- **Type hints lie** — `Optional[X]` declared but None never handled.

## Frameworks

- Django: N+1 in views (lack of `select_related` / `prefetch_related`); raw queries with f-strings; `.save()` inside loops.
- FastAPI / Flask: missing dependency injection on auth; sync routes blocking the loop in async frameworks.
- Pandas: chained assignment (`df[mask][col] = ...`); `inplace=True` returning `None` and being reassigned.

## Output

Same JSON shape as the default pack.
