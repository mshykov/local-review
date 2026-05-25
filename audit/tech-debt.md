# Audit — tech-debt

_Generated 2026-05-25 12:36 UTC_ · _LLM: claude_ · _Packages: 35_ · _Findings: 25_

## Summary

| Severity | Count |
| --- | --- |
| critical | 0 |
| major | 9 |
| warning | 8 |
| info | 8 |

## cmd/local-review [part 1/3]

_7 files audited_

- **[major]** `bench.go:195`
  **Duplicated file-write-with-mkdir pattern across three functions.**
  Why: `writeBenchToFile()`, `writeSWEBenchToFile()`, and `writeAuditFile()` all implement the same sequence: mkdir parent dirs, os.Create, defer with close-error checking. This five-line boilerplate is repeated verbatim (only the emit callback differs).
  Suggest: Extract to a shared `writeFileWithDirs(path string, emit func(io.Writer) error)` helper in this package, called by all three. Audit can then handle JSON vs markdown routing at the call site.
- **[major]** `bench.go`
  **Parallel implementation of bench and SWE-bench code paths — multiple function pairs differ only in types (`bench.Report` vs `bench.SWEBenchReport`).**
  Why: ~250 lines of near-duplicate logic (load, execute, emit) split between two type-based branches. Adding a third benchmark mode or changing shared logic requires changes in two places.
  Suggest: Parameterize over the report type and emitter interface (`type BenchReporter interface { ... CaseCount int; ... }`), so `loadBenchInputs()` becomes generic. Move mode-specific wiring (loading SWE dataset vs regular dataset) to the top-level dispatch, not to parallel functions throughout.
  
  ## Warnings
- **[warning]** `init.go`
  **Four retry loops with identical structure, all using the same `maxRetries` constant.**
  Why: Each wraps a prompt function in a `for i := 0; i < maxRetries; i++` loop with the same error exit pattern. If `maxRetries` semantics change (e.g., "give up after N retries, but hint the user on iteration 3"), all four sites need updating.
  Suggest: Extract a generic `promptWithRetry(fn func() (string, error), label string) (string, error)` that handles the loop, so the retry discipline is defined once.
  
  ## Info / Notes
- **[info]** `bench.go:116`
  **Conditional strictness logic is implicit in a flag-changed check.**
  Why: The choice of "ON in replay, OFF in live" is explained in a comment but baked into the RunE closure as `strict = bf.replayDir != ""`. A future maintainer might change one without the other.
  Suggest: Extract to a named function like `resolveStrictness(flags, replayDir) bool` so the logic is explicit and testable. Not urgent — the comment is clear — but worth doing if you're touching this code.
- **[info]** `doctor.go`
  **Long, complex orchestration file with nested if/switch logic in several functions.**
  Why: `runDoctor()` (lines ~475–600), `checkPromptOverride()` (lines ~530–620), and `printLLMRow()` (lines ~422–470) each handle multiple nested concerns. Not dead code or duplication, but cognitive load is high.
  Suggest: Not a refactor blocker — the code is well-commented and each function has a clear job. But if doctor grows (new LLM types, new auth methods, new prompt checks), consider extracting helper types (e.g., `type LLMDiagnostic struct`) to reduce nesting depth.
  
  ---
  
  **Summary**: No clean-code findings in this package. Main debt is **duplication across file writers and the bench/SWE-bench code split**. The retry-loop pattern in init.go is secondary. Doctor's length is acceptable for now given the domain complexity (multi-LLM orchestration, varied auth mechanisms).

## cmd/local-review [part 2/3]

_3 files audited_

- **[major]** `**cmd/local-review/runner.go:186`
  Nested error-handling branches in `runMultiLLMReview()` when `mergeable == 0` — three distinct cases (all failed, all empty, mixed) with inline conditionals. The logic is correct and documented but dense and load-bearing.
  
  Why: This block has been refined through bugs (the comment at line 186 enumerates three past failure cases that slipped through earlier versions). The extensive documentation suggests it's fragile.
  
  Suggest: Extract the error classification into a helper — `classifyZeroMergeableError(successCount, len(results)) → error` — to separate the error taxonomy from the reporting logic.
  
  ---
- **[major]** `**cmd/local-review/runner.go:156`
  Dual-metric gate design uses `CountSuccessful()` (Error == nil) and `CountWithOutput()` (non-empty Output) as distinct signals. Both are needed (SaveReview can fail while Output exists) but the dual existence across six call sites suggests past gate-logic regressions.
  
  Why: The code comment explicitly catalogs two past bugs where the metrics diverged (SaveReview-failed-with-output, CLI-exited-zero-with-empty-output). This is load-bearing documentation masking an architectural smell.
  
  Suggest: Consolidate gate logic into a single `GateDecision` type that computes both signals once, then threads the result through (similar to how `classifyRunMode()` already does). Prevents the metrics from drifting again.
  
  ---
- **[major]** `**cmd/local-review/runner.go:478`
  Both functions apply identical blocking-detection heuristics (Recommendation line + Critical/Major section headings) to different markdown sources. Not duplication yet, but the two code paths could diverge if one is updated and the other missed.
  
  Why: `mergedHasBlocking()` is called from two contexts — checking merged output and checking per-LLM outputs — suggesting the logic is applied at multiple layers. Changes to one heuristic risk being half-applied.
  
  Suggest: Extract the blocking-detection rules into a const or helper `isBlockingMarkdown(markdown string) bool` to unify the check. Both call sites use the same function.
  
  ---
  
  ### Warnings
- **[warning]** `**cmd/local-review/runner.go:114`
  Function name suggests timeout configuration, but the comment and code reveal it handles timeout, Model, and APIKey. Originally named `withTimeout`, suggesting scope creep.
  
  Why: Next maintainer reading `applyConfig(llm, cfg)` may assume it only sets timeout, missing the Model and APIKey mutations.
  
  Suggest: Rename to `threadConfigIntoLLM()` or add a brief doc comment noting all three mutations.
  
  ---
- **[warning]** `**cmd/local-review/runner.go:104`
  Function is ~280 lines with sequential concerns: validation → extraction → filtering → prompting → preflight → orchestration → streaming → merging → gating. Each concern is well-commented but the length makes refactoring error-handling blocks (like lines 186–231) harder.
  
  Why: Large orchestration functions are harder to test in isolation and harder to refactor without risking regressions in gate logic.
  
  Suggest: Extract the "no mergeable output" error branch into `handleZeroMergeableOutput(...)` and the gate-evaluation logic into `evaluateGate(merged, perLLM, metadata) error` to shrink the main function and isolate test-critical paths.
  
  ---
- **[warning]** `**cmd/local-review/init_test.go**`
  Test functions follow a repetitive pattern: construct input string → call `runInitTo()` → loop through expected strings and check containment. Eight tests use nearly identical structure for different inputs.
  
  Why: Parameterized test input would reduce lines and make it clearer what each test varies. (This is test-code maintainability, not functional debt, but worth noting.)
  
  Suggest: Refactor into a table-driven test with `[]struct{input, wantStrings}` to factor out the common loop.
  
  ---
  
  ### Info / Notes
- **[info]** `**cmd/local-review/runner.go:244`
  The blocking-finding gate uses two independent signals (`anyPerLLMHasBlocking` + `mergedHasBlocking`) as a security measure against the 8 KB truncation in `BuildMergeInput()`. Both are needed but create two code paths to maintain.
  
  Why: If the merger's truncation hides blocking findings, the per-LLM scan catches them. The dual check is a good safety measure but adds complexity worth documenting prominently.
  
  Suggest: Keep as-is, but extract both scans into a named `type BlockingSignal` struct so the gate logic is: `if sig.MergedReport || sig.PerLLM → return errBlockingFindings`. Clarifies the two-signal design.
  
  ---
- **[info]** `**cmd/local-review/runner.go:500`
  Package-level pre-compiled regex. The comment justifies it (performance — one regex per ~10 per-LLM outputs in large runs). Good optimization but creates module-level side-effect that's only used by one function.
  
  Why: Acceptable trade-off for performance, but worth noting if the function ever moves or testing changes.
  
  Suggest: No action; pattern is correct and documented.
  
  ---
  
  [clean] No dead code detected. All functions, constants, and test helpers are active.

## internal/bench [part 3/3]

_2 files audited_

- **[major]** `internal/bench/types.go`
  **Finding:** `BaselineScore` and `CaseScore` both implement identical `Precision()`, `Recall()`, and `F1()` methods with the same formulas and edge-case logic.
  
  **Why:** Any fix or improvement to the scoring formulas must land in two places, and divergence between them becomes a hidden bug risk. The methods operate on the same three fields (`TruePositives`, `FalsePositives`, `FalseNegatives`) and compute the same values; there's no reason to duplicate this logic.
  
  **Suggest:** Extract into a helper interface or utility functions that both types can use — e.g., `func computePrecision(tp, fp int) float64` and shared methods via embedding, or a `ScoreMetrics` interface that both implement by delegating to shared functions. This keeps the calculation logic in one place.
  
  ---
  
  ## Warnings
- **[warning]** `internal/bench/swe_test.go`
  **Finding:** `mkSWECase()` and `mkSWEFixture()` both follow the same pattern: create `root/id`, mkdir with error handling, then write file(s) with identical error handling. The only differences are the filenames written and count of files.
  
  **Why:** If the file-writing logic needs to change (e.g., different permissions, error message format, retry logic), both helpers must be updated. Duplication in test infrastructure can hide that the change missed one location.
  
  **Suggest:** Consolidate into a `mkTestFile()` helper that accepts file path(s) and content as varargs or a map, or split these into truly distinct concerns if they're expected to diverge in the future. Given they're minimal helpers at the end of the test file, even light consolidation reduces surface area.
  
  ---
  
  ## Info / Notes
- **[info]** `internal/bench/types.go`
  **Finding:** `CaseScore` carries 30+ fields with intricate cross-references (e.g., `Attempts`/`RunCount`/`Jaccard` are mutually-dependent; `Duration` vs `DurationMs`; treatment vs baseline vs repeat vs uplift fields). Same for `LLMReport` and `OverheadAggregate`.
  
  **Why:** The field relationships are well-documented in comments, but the sheer number and interdependence make it easy to accidentally break an invariant when adding a new feature (e.g., introducing a new timing field and forgetting to update aggregation logic).
  
  **Suggest:** No immediate action needed — the documentation is thorough — but a test that explicitly exercises all the invariants (e.g., "DurationMs = TreatmentDurationMs + BaselineDurationMs + RepeatCost" or "if Baseline == nil then BaselineError must be empty") would catch drift early.

## internal/cli

_12 files audited_

- **[major]** `internal/cli/invoker.go`
  Near-identical `run()` method implementations in GeminiInvoker and ClaudeInvoker — both build args, set stdin/env, capture stdout/stderr separately, call cmd.Run(), and invoke JSON parsers. Differences are CLI-specific (args, parser function, agent name) but the error-handling pattern is duplicated.
  Why: Future changes to the common pattern (e.g., context propagation, error classification) must land in two copies or diverge.
  Suggest: Extract a generic `runWithParser(args, agent, parser)` helper that both invokers call, or accept the duplication as a per-CLI specialization cost.
- **[major]** `internal/cli/invoker.go`
  Unused parameter `errLabel` explicitly ignored with `_ = errLabel`. Comments explain it was used for error prefixing but now duplicates the agent name already in the per-LLM line. The parameter is hardcoded in all callers and never read.
  Why: Dead parameter obscures the actual interface and suggests incomplete refactoring.
  Suggest: Remove `errLabel` from both method signatures and their call sites (Review, RunPrompt).
  
  ## Warnings
- **[warning]** `internal/cli/version.go`
  Unused parameter `name` — the function ignores the LLM name and just invokes `runVersionCmd(path, "--version")`. Callers in `detector.go` pass the name but it's never used.
  Why: Parameter suggests the function is name-aware when it's not; misleads readers about what the function does.
  Suggest: Remove `name` parameter, update callers to `detectVersion(path)`.
- **[warning]** `internal/cli/invoker.go`
  The `errLabel` pattern also appears in ClaudeInvoker where it's unused. Both invoker `run()` methods have nearly identical structure but GeminiInvoker's is cleaner (no unused param). ClaudeInvoker's leftover parameter suggests incomplete refactoring.
  Why: Inconsistency across similar implementations increases maintenance burden.
  Suggest: Remove `errLabel` from ClaudeInvoker.run and unify the error path across both invokers.
  
  ## Info / Notes
- **[info]** `internal/cli/invoker.go`
  Returns `nil` for unknown LLM names. The interface doesn't prevent this, but all call sites should handle nil. Worth documenting in callers or the function itself whether nil is an error or a graceful "no-op" case.
  Why: Silent nil return could mask typos in agent names without warning.
  Suggest: Consider returning `(nil, error)` for unknown agents, or pin nil-safety assertions in callers.
- **[info]** `internal/cli/parsers.go`
  High complexity (3 regex patterns, suffix stripping, latest-position logic) but extremely well-justified by the test suite. Each complexity layer (same-pattern false positives, cross-pattern precedence, trailing duplicates) has a dedicated test. This is debt-free complexity — legitimate.
  Why: No issue; cited as a positive example of justified complexity.
  Suggest: None — keep the existing approach.
  
  ---
  
  **Summary**: The package is well-structured overall. The primary debt is unused parameters (`errLabel`, `name`) left from incomplete refactoring, and duplication of the `run()` pattern between two similar invokers. None of these are bugs, but they're code smells worth cleaning up in the next round of touching this code.

## internal/review

_4 files audited_

- **[major]** `internal/review/review.go:306-315`
  **Dead code: `matchGlob()` function is never called, has no tests, and misleading comment about external API.**
  
  Why: The function is marked as private (`func matchGlob`, not `MatchGlob`), yet the comment claims it's "kept for back-compat with any external caller"—a contradiction since private functions have no external callers. The codebase replaced this slow single-pattern path with `matchesAny()` (tests) and `compileGlobs() + matchesAnyCompiled()` (production), but `matchGlob()` was never removed. No test file exercises it (all glob tests use `matchesAny()`).
  
  Suggest: Remove the `matchGlob()` function entirely. Update the `globToRegex()` docstring to remove the reference "Kept for tests and back-compat with any external caller" since the intended audience already uses `matchesAny()` or the optimized path.
  
  ---
  
  ## Warnings
- **[warning]** `internal/review/review.go:296-298`
  **Two glob-matching functions with overlapping purpose and inconsistent coverage.**
  
  Why: Both `matchesAny()` (for tests) and `matchGlob()` (supposedly for back-compat) exist to support glob matching without pre-compiled regexes, but only `matchesAny()` is actually tested and used. The separation is artificial: `matchesAny()` wraps `compileGlobs()`, while `matchGlob()` reimplements the same pattern-compilation logic inline. If `matchGlob()` were kept for legitimate callers, it should have test coverage to prevent drift.
  
  Suggest: Either (1) remove `matchGlob()` and rely on `matchesAny()` for the test/convenience path, or (2) add test coverage for `matchGlob()` if it's a deliberate external API. The current state—dead code with a comment claiming external use—invites future maintainers to keep it "just in case."
  
  ---
- **[info]** `internal/review/review.go:195-228`
  **Silent glob pattern failures risk user confusion.**
  
  Why: The `compileGlobs()` function (line 230) silently drops invalid patterns that fail to compile (`regexp.Compile` error). While the comment explains this is intentional (config typo treated as non-matching), a user who accidentally writes `["**/*.go", "[!]"]` will only get warnings about the valid pattern—the invalid one is dropped silently. Combined with `filter()`'s "fail-closed" logic (line 211), this can mask configuration errors. The behavior is correct but could be fragile if patterns are generated dynamically.
  
  Suggest: No change needed if this is working as designed (the test `TestFilter_AllInvalidIncludesFailClosed` confirms fail-closed semantics). However, consider whether `local-review doctor` should warn about unparseable glob patterns in config, to surface silent drops.
  
  ---
  
  [clean] no additional tech-debt findings in internal/review/types.go

## Clean packages

_30 packages with no findings:_ ., .github, .github/ISSUE_TEMPLATE, .github/workflows, bench/dataset/clean-go-rename-1, bench/dataset/clean-ts-import-reorder-1, bench/dataset/go-error-shadow-1, bench/dataset/go-nil-deref-1, bench/dataset/go-race-mapwrite-1, bench/dataset/python-shell-injection-1, bench/dataset/python-yaml-load-1, bench/dataset/rust-unsafe-deref-1, bench/dataset/ts-sql-injection-1, bench/dataset/ts-xss-innerhtml-1, bench/swe-bench-lite/example-orm-sql-injection, bench/swe-bench-lite/example-paginator-off-by-one, bench/swe-bench-lite/example-retries-swallow-timeout, cmd/local-review [part 3/3], examples, internal/audit, internal/bench [part 1/3], internal/bench [part 2/3], internal/config, internal/git, internal/lang, internal/llm, internal/multi, internal/output, internal/prompts, scripts

