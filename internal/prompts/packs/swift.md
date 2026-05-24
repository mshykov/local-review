# Swift review pack

Apply the default review rules. Plus: Swift-specific patterns to look for.

## High-signal Swift pitfalls

### Optionals & nil safety
- **Force-unwrap (`!`)** — almost always wrong outside `@IBOutlet` and test code; flag any non-test `try!` / `as!` / `x!` on non-trivially-known-non-nil values. Suggest `guard let` / `if let` / nil-coalescing (`??`).
- **Implicitly unwrapped optionals (`Type!`)** — defer crash to first use; treat as a code smell unless the value is provably set before any read (IB outlets, late-init pattern).
- **`as!` downcasts** — runtime crash on mismatch; use `as?` + `guard let`.
- **Chained `?.`** swallowing errors silently — when the chain is doing real work (mutation, side effect), the silent-nil-skip is usually a bug.
- **Comparing `Optional` to a value** without unwrapping — `someInt == 0` is `false` when `someInt` is `nil`; usually you meant `(someInt ?? 0) == 0` (parens added for clarity even though `??` does bind tighter than `==` in Swift — the unparenthesized form trips readers).
- **`Optional<Bool>` in conditions** — `if let x` is correct; `if x == true` silently drops the `nil` case.

### Memory & reference cycles
- **Strong-strong reference cycles** between class instances (delegate, parent/child, closure-captured-self) — leak; use `weak` or `unowned`.
- **`[weak self]` missing in closures** that outlive `self` (network handlers, timers, observers, `Task {}`, Combine `sink`).
- **`unowned` on a reference that can outlive its target** — crash; prefer `weak` unless you can prove the lifetimes.
- **Retain cycles via `@escaping` closures** stored on `self` — the closure captures `self` strongly by default.
- **`Timer.scheduledTimer`** without invalidation in `deinit` — leaks the target.
- **NotificationCenter observers** not removed in `deinit` (pre-iOS 9 selector-based API).

### Concurrency
- **Data race**: mutable state shared across `Task`/`DispatchQueue` boundaries without an actor / lock / `@MainActor`.
- **`@MainActor` annotation missing** on UI-touching code reached from a background context — runtime warning in Xcode 14+, crash in stricter targets.
- **`async let` not awaited** — the result is dropped, the task may be cancelled.
- **`Task {}` inside a sync function that returns immediately** — fire-and-forget; errors disappear unless explicitly handled.
- **`DispatchQueue.main.async` chained inside `@MainActor`** — redundant and confusing; you're already on the main actor.
- **`Task.detached` without good reason** — usually you want `Task {}` (inherits actor); detached drops actor isolation and can cause UI updates off the main queue.
- **Blocking the main thread** with `sleep()`, `DispatchSemaphore.wait()`, or synchronous I/O.

### Value vs reference types
- **`struct` mutated through a `let` constant** — won't compile, but a common confusion in code that toggles `let`/`var`; flag if the intent looks like mutation.
- **`class` used where `struct` would do** — extra ARC overhead, identity vs equality confusion. Prefer `struct` for plain data.
- **`Array` / `Dictionary` copied unintentionally** in performance-critical code (they're value types with COW; large arrays passed by value can be expensive on the first mutation).
- **`Codable` conformance with `class` inheritance** — subclass fields silently dropped unless `init(from:)` / `encode(to:)` are overridden.

### Error handling
- **`try?` swallowing real errors** — when the error matters (network call, file I/O), prefer `try` with a `do { } catch { }` block.
- **`try!` outside test code** — crashes on the first non-happy-path; flag unconditionally.
- **`throws` not actually thrown** — vestigial annotation; either remove or use.
- **`Result.failure` ignored** in completion handlers (legacy API style).
- **NSError bridging** dropping `userInfo` — losing context across the Cocoa boundary.

### API & framework patterns
- **`@IBOutlet` declared without `weak`** when the view is owned by the superview — retain cycle.
- **`@Published` on `@MainActor`-isolated property accessed from background** — runtime warning / actor violation.
- **SwiftUI body referring to mutable state outside `@State` / `@StateObject` / `@ObservedObject` / `@Environment`** — view doesn't redraw on mutation.
- **`@StateObject` recreated on every render** because it's declared on a non-`View`-owned property.
- **`@EnvironmentObject` not provided** in the view hierarchy — runtime crash.
- **`Combine` subscription not stored** in a `Set<AnyCancellable>` — sink fires once and is deallocated.

### Security
- **`URLSession` without TLS** — explicit `http://` URLs in production code; ATS exceptions in `Info.plist`.
- **Hardcoded API keys / secrets** in source — should be in environment / keychain / Configuration.
- **Keychain wrappers swallowing OSStatus errors** — silent auth failures.
- **`String(data:encoding:)` with `.utf8` on untrusted bytes** without validation — silent corruption.
- **Insecure deserialization** — `NSKeyedUnarchiver` without `requiringSecureCoding: true` on untrusted input.

## Idioms & style

- **`guard` for early exit** — flatter than nested `if let`; required when the value is needed for the rest of the function.
- **Trailing closure syntax** for the final closure argument; multiple-trailing-closure syntax (Swift 5.3+) for two closures.
- **Computed properties vs methods** — no-argument `() -> T` reads better as a computed `var x: T { … }` when there are no side effects.
- **`map` / `filter` / `reduce` / `compactMap`** instead of imperative loops for transforms.
- **`enum` for finite sets of states** rather than `String` constants — exhaustive `switch` catches missed cases at compile time.
- **`fileprivate` / `private`** — prefer the narrowest access level that works.
- **Tuple destructuring** `let (x, y) = pair` — clearer than `pair.0` / `pair.1`.

## Output

Same JSON shape as the default pack.
