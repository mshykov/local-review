# Python review pack

Apply the default review rules. Plus: Python-specific patterns to look for.

## High-signal Python pitfalls

### Common bugs
- **Mutable default arguments** — `def f(x=[]):` is the classic bug; use `None` and reassign.
- **`==` with `None`** — should be `is None` / `is not None` (identity check).
- **Typos in dunder methods** — `__init__`, `__str__`, etc.; silently doesn't override.
- **`for x in dict:` then mutating dict** — RuntimeError; copy keys first.
- **Late binding in closures** — `lambda x: x*i` in loop captures reference, not value.
- **String concatenation in loops** — inefficient; use `''.join()` or f-strings outside loop.
- **Global variable shadows builtin** — `list = []`, `dict = {}`, `id = 1`.

### Error handling
- **Bare `except:`** — swallows `SystemExit` / `KeyboardInterrupt`; almost always wrong.
- **Catching `Exception` too broadly** when a specific subclass would do.
- **Re-raising without traceback** — use `raise` alone to preserve stack trace.
- **Ignoring context in exception chains** — use `raise NewError() from original_error`.
- **Empty except blocks** — at minimum, log the error.

### Resource management
- **`open()` without context manager** — files leaking on error paths; use `with`.
- **Database connections not closed** — use context managers or `try/finally`.
- **Thread/process not joined** — zombie processes or leaked resources.
- **`__del__` for cleanup** — unreliable; use context managers instead.

### Security
- **f-string / format injection** in SQL or shell commands.
- **`subprocess` with `shell=True`** on user-controlled input — command injection risk.
- **`pickle.loads` on untrusted data** — arbitrary code execution.
- **`eval()` / `exec()` on user input** — extreme security risk.
- **Path traversal** — `os.path.join(base, user_input)` without validation.
- **Hardcoded secrets** — API keys, passwords in source code.
- **SQL injection** — string interpolation instead of parameterized queries.

### Async patterns
- **Async without `await`** in `async def` functions — creates unawaited coroutine.
- **`asyncio.run()` in already-running loop** — raises RuntimeError.
- **Blocking I/O in async** — `requests.get()` instead of `aiohttp`; blocks event loop.
- **Missing `asyncio.gather` error handling** — one failure kills all tasks.
- **Sync and async mixed** — `async def` calling sync blocking functions.

### Type hints
- **Type hints lie** — `Optional[X]` declared but `None` never handled.
- **Missing return type** — makes refactoring harder.
- **`typing.Any` overused** — defeats purpose of type checking.
- **Concrete types in params** — prefer `Sequence[int]`/`Iterable[int]` over `list[int]` when function only reads; accepts more input types (tuples, ranges) and signals non-mutation intent (not enforced at runtime).
- **Runtime isinstance with generics** — `isinstance(x, list[int])` fails; use `isinstance(x, list)`.

## Frameworks

### Django
- **N+1 queries** — missing `select_related()` / `prefetch_related()` in views.
- **Raw SQL with f-strings** — SQL injection; use parameterized queries.
- **`.save()` inside loops** — slow; use `bulk_create()` / `bulk_update()`.
- **Missing `get_object_or_404`** — raw `.get()` raises exception instead of 404.
- **Queryset evaluation in templates** — causes duplicate queries.
- **Signals for business logic** — hard to debug; prefer explicit calls.
- **Missing database indexes** — on frequently filtered/joined fields.

### FastAPI / Flask
- **Missing dependency injection** — auth/DB connections passed manually.
- **Sync routes in async frameworks** — blocks event loop; use `async def`.
- **Missing request validation** — Pydantic models should validate all inputs.
- **CORS misconfiguration** — overly permissive origins.
- **Missing rate limiting** — on public endpoints.
- **Secret keys in code** — should use environment variables.

### Pandas / NumPy
- **Chained assignment** — `df[mask][col] = ...` doesn't work; use `.loc[]`.
- **`inplace=True` misused** — returns `None`; don't reassign result.
- **Iterating rows with `.iterrows()`** — slow; use vectorized operations.
- **Not copying DataFrames** — `df2 = df1` is just another reference (no copy); mutations to either variable affect the same object; use `.copy()` when you need an independent DataFrame.
- **Missing null checks** — operations on `NaN` propagate silently.
- **Wrong dtype** — numeric data stored as object/string hurts performance.

## Idioms & style

- **List comprehensions for simple transforms/filters** — prefer over loops for straightforward operations; for complex multi-step logic, a loop may be more readable.
- **Enumerate instead of range(len())** — `for i, x in enumerate(lst)`.
- **Dict/set comprehensions** — `{k: v for ...}` instead of manual loops.
- **Truthiness** — `if my_list:` instead of `if len(my_list) > 0`.
- **`with` statements** — for resource management (files, locks, connections).
- **F-strings** — prefer over `.format()` or `%` formatting (Python 3.6+).
- **Pathlib** — use `Path` objects instead of string manipulation for paths.
- **Dataclasses** — cleaner than manual `__init__` for simple data containers (Python 3.7+).

## Output

Same JSON shape as the default pack.
