# Kotlin review pack

Apply the default review rules. Plus: Kotlin-specific patterns to look for.

## High-signal Kotlin pitfalls

### Null safety
- **Not-null assertion (`!!`)** — almost always a smell outside test code and Java-interop boundaries; flag any `!!` on a value that wasn't trivially just null-checked. Suggest `?.`, `?: throw`, `requireNotNull`, `checkNotNull`, or pattern match via `let`.
- **Platform types from Java interop** — `String!` (no `?` and no non-`?`) silently allows nulls in; add nullability annotations on the Java declarations (or external annotations), and coerce immediately at the Kotlin boundary (`String?`, `requireNotNull`, etc.).
- **`lateinit var` accessed before assignment** — `UninitializedPropertyAccessException`. When the value won't be reassigned after init AND init can be expressed as a local lambda (e.g. a derived helper, a cached parsed config), prefer `val x by lazy { … }` — same single-shot init, but the field stays immutable and a re-init bug becomes a compile error. `lateinit var` is the right shape when the value is genuinely mutable or set by an external framework (DI, view binding, test setup) where `lazy` can't express the initializer.
- **`!!` on chain results** (`a?.b?.c!!`) — defeats the chain's null-safety purpose; either commit to the null path or throw explicitly.
- **Smart cast lost across a non-`val`** — mutable property re-read after the null check; capture into a local `val` first.
- **`Boolean?` in conditions** — `if (flag)` won't compile, but `flag == true` silently drops `null`; usually you want `flag == true` *intentionally* OR `flag ?: false`.

### Coroutines & concurrency
- **`GlobalScope.launch { }`** — leaks the coroutine across the lifecycle of the calling component; use a structured scope (`viewModelScope`, `lifecycleScope`, `coroutineScope`).
- **`runBlocking { }` on a UI / main thread** — defeats the whole reason to use coroutines.
- **`Dispatchers.Main` for CPU work** — blocks UI; use `Dispatchers.Default` for CPU, `Dispatchers.IO` for blocking I/O.
- **Suspend function calling blocking I/O directly** — wrap in `withContext(Dispatchers.IO)`.
- **`launch` where `async/await` was intended** — caller can't observe failure of `launch` without an explicit `CoroutineExceptionHandler`.
- **`Job.cancel()` not propagating** — child coroutines may keep running; cancel the parent scope or use cooperative cancellation (`isActive`, `yield()`).
- **`Flow.collect { }` outside a scope** — never starts collecting; or collected with `launchIn(scope)` against the wrong scope and leaking.
- **`StateFlow.value` mutated from multiple coroutines** without `MutableStateFlow.update { }` — last-write-wins under contention.
- **`Channel` not closed** when the producer is done — `consumeAsFlow` collectors hang forever.

### Memory & lifecycle (Android)
- **Activity / Fragment reference captured in a long-lived object** (singleton, repository, ViewModel) — leak.
- **`Context` reference held outside the lifecycle** — pass `applicationContext` when long-lived, never the Activity.
- **`Handler.postDelayed` / `Runnable` posted to a removed view** — runs after `onDestroy`; cancel in `onPause`/`onDestroy`.
- **`LiveData.observeForever` without `removeObserver`** — leak.
- **`Flow.collect` in `lifecycleScope.launch { }` without `repeatOnLifecycle`** — collects continue while stopped; use `repeatOnLifecycle(STARTED)`.
- **`registerReceiver` without `unregisterReceiver`** in matching `onPause` / `onDestroy`.
- **`Disposable` (RxJava) not added to a `CompositeDisposable`** that's cleared on lifecycle teardown.

### Collections & sequences
- **`map { }.filter { }.map { }` on large collections** — each step allocates a new list; switch to `.asSequence()` for chained transforms.
- **`forEach { }` where the result matters** — return value is `Unit`; use `map` or `onEach` if you want the receiver back.
- **`mutableListOf` / `mutableMapOf` returned from a public API** — exposes internal mutability; return `List` / `Map` (read-only views).
- **`!!` on `Map.get(key)`** — `getValue(key)` throws a clearer `NoSuchElementException`; `getOrElse`/`getOrDefault` if a fallback exists.
- **Empty-collection checks** — `if (list.size == 0)` vs `if (list.isEmpty())`; prefer the latter.

### Error handling
- **Generic `catch (e: Exception)` / `catch (e: Throwable)`** — same anti-pattern as bare except in Python; flag unless the comment says "and rethrow."
- **`runCatching { }.getOrNull()`** in a place that needed the error — `Result` discarded silently; either propagate or log.
- **`throw RuntimeException("…")`** for things that should be checked failures — use a typed exception or `Result`.
- **`require` / `check` used for runtime user-input validation** — they throw `IllegalArgumentException` / `IllegalStateException`; OK for preconditions, wrong for "the network came back malformed."

### Equality & data classes
- **`==` vs `===`** — `==` calls `equals`; `===` is reference identity. Mixing them up is a Java-habit bug.
- **`data class` with mutable `var` properties** — broken `equals`/`hashCode` if the instance is mutated after being put in a `HashSet`/`HashMap`.
- **`copy()` missing on a data class refactor** — adding a non-default field silently breaks every caller.
- **`hashCode` / `equals` not co-overridden** — every Set/Map will misbehave.

### Security
- **`String.format` / interpolation into SQL** — use parameterized queries (Room handles this; raw `SQLiteDatabase.rawQuery` does not).
- **`WebView.loadUrl(userInput)`** without scheme validation — XSS / code execution.
- **`addJavascriptInterface` on a WebView loading untrusted content** — remote code execution.
- **Hardcoded API keys / signing secrets** — should be in `local.properties` or build-time injection; never committed.
- **`SharedPreferences` used for secrets** without `EncryptedSharedPreferences`.
- **Implicit `Intent`** for sensitive actions — any app can claim the action; use explicit `Intent` or `setPackage`.
- **`exported="true"`** on activities/services receiving untrusted input — Android 12+ requires explicit declaration; flag if not justified.

### Gradle / build
- **`implementation` vs `api`** confusion — `api` leaks transitive deps to consumers and bloats the dependency graph.
- **`kapt` for Hilt/Room** — slower than KSP; if the library supports KSP, prefer it.
- **`minSdk` raised without checking calls** — runtime crash on older devices.
- **`compileSdk` lower than `targetSdk`** — won't compile; project misconfiguration.

## Idioms & style

- **`when` for finite alternatives** — exhaustive over sealed classes / enums catches missed cases at compile time (use it as an expression so the compiler enforces exhaustiveness).
- **Scope functions** (`let` / `also` / `apply` / `run` / `with`) — each has a distinct shape (receiver vs argument, returns receiver vs lambda); pick the one that reads cleanest, don't mix conventions.
- **`val` over `var`** — immutability by default; flag `var` that isn't actually mutated.
- **Extension functions** — keep them in the package of the receiver if reasonable; avoid `Any.extension` that pollutes every type.
- **Sealed classes / interfaces** for state machines — exhaustive `when` is the payoff.
- **`object` for singletons** — prefer over `companion object { @JvmStatic … }` unless Java callers need the static look.

## Output

Same JSON shape as the default pack.
