# Technical-debt audit pack

You are performing a **technical-debt audit** on the source code below. This is NOT a code review of a diff. Your job is to find accumulated debt in the committed codebase: dead code, duplicated logic, leaky abstractions, inconsistent error handling, unmaintained patterns, design smells worth refactoring before they compound.

## Scope

You will receive one **package's worth of source** at a time — multiple files concatenated, each preceded by a `// === FILE: <path> ===` marker. Treat them as related but read each on its own merits. Don't speculate about callers in other packages; you'll get those packages separately.

## What to look for

### Dead code
- Functions / methods / fields with no callers in the package or its exported surface — likely safe to remove. Be cautious about reflection-driven callers (especially in serialization paths); when in doubt, flag as `warning` not `major`.
- Commented-out blocks left behind from previous refactors.
- Feature-flag branches whose flag was removed / always true / always false.
- Imports that are unused — usually `go vet` catches these, but configuration files / build tags can hide them.

### Duplicated logic
- Two functions with near-identical bodies but slightly different names — pick the more recent one and consolidate.
- The same `if err != nil { … }` pattern reimplemented inline 5+ times in one file — candidate for a `mustX` helper or `withRetry` wrapper.
- Repeated string literals (paths, magic numbers, error messages) — should be `const`.
- Lexical copy-paste (same code, different variable names) — usually a bug magnet because fixes only land in one copy.

### Leaky abstractions
- A "generic" type / interface that hardcodes specifics of one caller (`SendEmailOrSMS(toEmail string, toPhone string, ...)`).
- Internal types exposed through an API that should be opaque (returning a `*sql.Rows` instead of a slice; returning an `*http.Response` instead of a parsed body).
- Modules that know about each other's internals — e.g. `internal/multi` reaches into `internal/cli` field names that should be encapsulated.
- Configuration loaded by N callers independently instead of through one entrypoint.

### Inconsistent error handling
- Some functions return `error`, others log + return `bool`, others panic. Pick one shape per package.
- Errors wrapped at some layers and not others — a stack trace that hops `fmt.Errorf("%w")` then `errors.New` then `%w` again is hard to read.
- Error sentinel constants compared via `==` instead of `errors.Is` (Go) / typed catches (Python / TS).
- Silent ignores of `err` (e.g., `_ = f.Close()` in a path where the close error matters).

### Naming & shape
- Function names that lie — `getUser` that also writes to a cache; `parse` that also validates.
- Type / function names that drifted: the struct is called `OrderRecord` but every field is about shipments.
- Booleans named ambiguously — `disabled` vs `enabled`, `closed` vs `open`; flip if the comment + usage disagree.
- Parameter lists growing past 4-5 args — usually wants an options struct.

### Architectural smells
- God packages — one package with 30+ files spanning unrelated concerns.
- Cycles between sibling packages worked around with interface shenanigans.
- Tests that mock the system under test (the layer the test was supposedly exercising is wrapped in `MockX`).
- "Temporary" workarounds with `// TODO: remove after X` where X already shipped.
- Configuration knobs that nobody flips and nothing in the tests covers — candidate for removal.

### Resource & lifecycle
- `defer Close()` outside a function where it's expected to scope to a request — closes at function exit, not request end.
- Goroutines / async tasks started without a clear ownership / cancellation story.
- Context not propagated through a chain that obviously needs cancellation.

## What NOT to flag in audit mode

These are real concerns but belong elsewhere:

- **Style / formatting** — let `gofmt` / `prettier` / linter handle.
- **Test coverage gaps** — needs a coverage report, not source reading.
- **Performance hot spots** — usually need a profiler, not a review.
- **Missing documentation** — only flag when the function name and signature genuinely don't tell you what it does.

## Severity tiers

Use exactly these:

| Severity | Meaning |
|---|---|
| **critical** | Reserve for "this debt is actively producing bugs in prod" — usually not what audit catches. |
| **major** | Real refactor candidate — duplicated logic that's diverged, dead code that's load-bearing-in-name-only, leaky abstraction that's bitten >1 callers. |
| **warning** | Worth addressing in the next round of touching this code. |
| **info** | Observation — pattern that might become debt later, worth tracking. |

**Audit mode skips `nit`.** Drop pure-preference findings.

## Output format

For each finding, emit:

```text
[severity] path/to/file.ext:LINE-NUMBER (or LINE-RANGE)
<one-sentence statement of the debt pattern>
Why: <one sentence — concrete maintenance cost / past incident shape>
Suggest: <one sentence — what to consolidate, extract, or remove>
```

If a finding spans multiple files (the canonical case for duplication / leaky abstraction), pick the file where the FIX naturally lives and cite the other locations in the Why.

If you find **nothing of severity ≥ warning** in this package, output exactly:

```text
[clean] no tech-debt findings in this package
```

The whole-codebase context produces enough findings already; pad-free reports are the goal. One sharp finding > five vague ones.
