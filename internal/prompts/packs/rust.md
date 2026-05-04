# Rust review pack

Apply the default review rules. Plus: Rust-specific patterns to look for.

## High-signal Rust pitfalls

### Error handling
- **`.unwrap()` / `.expect()` in production code** — every panic on `None`/`Err` is a runtime crash. Flag in non-test, non-`main`, non-prototype code; require `?`, `match`, or a documented invariant explaining why panic is acceptable.
- **`?` swallowing context** — propagating `?` without wrapping (`.context("loading config")?`) loses the error trail. Flag for I/O, parse, and external-call errors.
- **`Result<T, Box<dyn Error>>` in libraries** — opaque error type prevents callers from matching specific failures; prefer a typed error enum (`thiserror`) for libs.
- **Catching everything with `let _ = result`** — silently discarding errors. Always intentional? Add a comment, or handle/log.
- **Panicking in `Drop`** — aborts the program if drop runs during another panic. Never panic in `Drop::drop`.
- **`assert!` / `panic!` for caller errors** — should usually be `Result::Err`. Reserve panic for true invariant violations (state the code itself broke).

### Ownership & borrowing
- **Unnecessary `.clone()`** — cheap on `Arc`/`Rc`/small types, expensive on `String`/`Vec`/large structs. If a borrow would work, prefer `&T`.
- **`.to_string()` / `.to_owned()` in hot paths** — flag allocation in tight loops; consider `&str` or `Cow<str>`.
- **Returning references to local data** — caught by the borrow checker, but easy to "fix" by cloning unnecessarily; the right fix is often a different ownership shape.
- **`RefCell` / `Cell` borrows held across `await` points or function calls** — runtime panic if a re-borrow happens. Tighten the scope.
- **`.iter().collect::<Vec<_>>()` then iterate again** — wasted allocation if the original iterator could be consumed directly.
- **Mutable aliasing through `unsafe`** — UB if more than one `&mut` exists at a time. Demand justification.

### Lifetimes
- **`'static` bounds where they aren't needed** — tightens the API surface; often added to silence the compiler instead of fixing the underlying issue.
- **Anonymous lifetime `'_` in returned references** — fine, but verify the implicit elision is what the author intended (especially with multiple input lifetimes).
- **Self-referential structs** — usually a smell; pin/`Pin` only justifies it for futures/generators. Otherwise refactor.

### Concurrency
- **`Mutex` poisoning ignored** — `.lock().unwrap()` panics on poison; consider `.lock().unwrap_or_else(|e| e.into_inner())` or surface poisoning explicitly.
- **`Arc<Mutex<T>>` for read-heavy data** — prefer `Arc<RwLock<T>>` when reads dominate.
- **Lock held across `await`** — deadlock risk if any task awaiting on the same lock runs on the same thread; either drop the guard before `.await` or use `tokio::sync::Mutex`.
- **`std::sync::Mutex` in async code** — blocks the executor thread. Use `tokio::sync::Mutex` (or hold briefly and drop before await).
- **Channels without bounded capacity** — unbounded channels can OOM under backpressure. Use `tokio::sync::mpsc::channel(N)` or `std::sync::mpsc::sync_channel(N)` (note: `std::sync::mpsc::channel()` is always unbounded — that's the issue, not a missing arg).
- **Spawned panics swallowed** — `thread::spawn` and `tokio::spawn` capture panics into the returned handle. Dropping the handle (or never `.join()`/`.await`-ing it) means a task can panic silently with no observable failure.
- **Hand-rolled `Send`/`Sync` impls** — high bar; require a `// SAFETY:` comment justifying the thread-safety invariant.

### Async & futures
- **Blocking I/O inside an async fn** — `std::fs`, `std::net`, `Mutex::lock` block the executor. Use the `tokio::` / async equivalents, or `spawn_blocking`.
- **`.await` inside `Mutex` guard scope** — see above (lock-across-await).
- **Forgotten `.await`** — calling an async fn without `.await` returns a future that does nothing. Compiler usually catches via `must_use`, but flag any `let _ = some_async_call()`.
- **`tokio::spawn` with `'static` capture surprises** — passes ownership; if the task panics and isn't `.await`ed, the panic is silently swallowed.
- **`select!` without cancellation safety check** — branches can drop a partially-completed future. Flag complex `select!` blocks for review.

### Memory & performance
- **`Box::leak` outside of init code** — leaks memory; only acceptable for one-time globals.
- **`Rc<RefCell<T>>` cycles** — strong cycles never drop; use `Weak` for back-references.
- **`Vec::push` in a loop without `Vec::with_capacity`** — repeated reallocation when size is knowable.
- **`format!` for simple concatenation** — `String::push_str` is cheaper for known suffixes.
- **`String::from_utf8_lossy` silently dropping bytes** — flag if input is supposed to be valid UTF-8 elsewhere.
- **`.collect::<Vec<_>>()` then `.iter()`** — wasted allocation; chain iterators directly.

### Unsafe & FFI
- **`unsafe` block without a `// SAFETY:` comment** — undocumented invariants. Always require justification for every unsafe block.
- **`mem::transmute`** — almost always wrong outside of FFI/serialization corners. Demand a strong reason.
- **Raw pointer dereferencing in safe-looking APIs** — the unsafe contract crosses the API boundary; safe wrappers must enforce all invariants.
- **`extern "C"` types not `repr(C)`** — UB across the FFI boundary.
- **Dropping FFI-allocated memory with Rust's allocator** — must use the matching `free` from the foreign side.

### Match & pattern matching
- **`_ =>` arms hiding new variants** — adding an enum variant won't trigger a compile error at the match site; prefer explicit patterns when the enum is owned by the project.
- **`match x { Some(_) => ..., None => panic!() }`** — that's `.unwrap()` with extra steps; prefer the latter or `.expect("reason")`.
- **`if let` binding then never used** — `if let Some(v) = expr { … }` where `v` isn't read inside the block; either consume `v` or drop it (`if let Some(_)`/`if expr.is_some()`).

### API design
- **Returning `&str` vs `String`** — prefer `&str` for slices into existing data; return owned `String` only when allocating is unavoidable.
- **Generic over too much** — `fn foo<T: AsRef<str>>(x: T)` is fine for ergonomics, but `fn foo<T: Trait>(...)` for one-call sites adds API surface for no reason.
- **`pub` on internal helpers** — restricts future refactors; default to `pub(crate)` and only widen when actually consumed externally.
- **Builder methods that take `self`** — fine, but breaks chaining if any branch returns a `Result`. Consider `&mut self` for fallible builders.

### Cargo, modules, and dependencies
- **`unused_imports` / `dead_code`** — ship-blocker if the project enforces `#![deny(warnings)]`; otherwise still cleanup-worthy.
- **Wildcard `use` in non-test code** — pollutes namespace, hides shadowing.
- **`features = ["full"]` on `tokio`** — pulls everything; pin to the actual features needed (`rt-multi-thread`, `macros`, `net`, ...) for compile time and binary size.
- **Version specifiers `"*"` or `">=X"`** in `Cargo.toml` — non-reproducible builds; use `^X.Y` (the default).
- **Dependencies on `git = "..."` without `rev = "..."`** — non-reproducible.
- **`cargo-deny` / `cargo-audit` flags ignored** — yanked or vulnerable deps.

### Testing
- **`#[test]` without `#[cfg(test)]`-gated helpers** — test-only code shipped in the binary.
- **`unwrap()` in tests** — fine for setup, but use `expect("...")` to make failures readable.
- **Async tests without an async test attribute** — `#[test] async fn …` is a compile error on stable Rust; require `#[tokio::test]`, `#[async_std::test]`, or the runtime-specific equivalent.
- **Doctest examples that don't run** — `/// ```ignore` is a smell; either fix the example or document why it can't compile.
- **Flaky tests with `thread::sleep`** — replace with explicit synchronization (channel, `Notify`).

## Idioms & style

- **`if let` chains nested deeply** — flatten with early returns or `let-else` (Rust 1.65+).
- **`match` arms returning `()` with side effects** — fine, but ensure no missing `;` ate a value silently.
- **`as` casts for numeric narrowing** — silent truncation; prefer `try_into()` and handle the error.
- **`.into()` chains hiding type confusion** — when the target type isn't obvious from the line, name it.
- **`Default::default()`** — `T::default()` is clearer.
- **Trait imports forgotten** — extension methods invisible without `use SomeTrait;` (e.g. `itertools::Itertools`, `futures::StreamExt`, `tokio::io::AsyncReadExt`).

## Output

Same JSON shape as the default pack.
