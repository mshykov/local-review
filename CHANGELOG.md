# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- **Reject `prompts.pack_dir` paths that escape the config directory.** First-user dogfood of the new `local-review audit --topic security` (PR #73) surfaced a real defence-in-depth gap in `internal/config/config.go`: a `.local-review.yml` with `pack_dir: ../../../etc` would resolve outside the config's own directory and let the override-resolver touch files there. Now `resolveRelativePaths` rejects any relative `pack_dir` that resolves outside `<config-dir>` via `..` segments. Absolute paths still pass through unchanged (explicit-opt-in to a specific location remains supported). Tests cover both directions: traversal rejection AND paths-with-`..`-that-stay-inside (`foo/../bar`) still passing.

- **Environment variables now take precedence over deprecated YAML-stored API keys.** Same audit pass flagged that the YAML `api_key` field — already marked DEPRECATED in the schema and warned about at load time — was still winning over `os.Getenv(api_key_env)` at resolution. That made the silent-stale-key footgun real: a developer who committed a test key to `.local-review.yml`, then later set a correct prod key in the environment, would keep using the stale YAML value. v0.10.0 flips the precedence: when the env var is set, it overrides the YAML key (and the warning text now explicitly says so). Empty env still falls back to the YAML key for backward compatibility with users who put a key there and never set an env var. Behaviour change is small (only users with BOTH YAML and env-var set are affected, and they get the env-var anyway — which is what most expected).

### Added

- **`local-review audit --topic <security|tech-debt>` — deep analysis of the committed codebase (PR 0.10.0-c).** New top-level subcommand that walks `git ls-files`, groups source by directory, and runs each package through the LLM with a topic-specific system prompt. Unlike `review` (which inspects a git diff), `audit` surfaces accumulated issues that no individual diff would catch: pre-existing security gaps, dead code, duplicated logic, leaky abstractions. Single-LLM in v1 (audit cost is per-package × per-topic; multi-LLM would multiply spend without obvious quality return — deferred until we see real usage). New audit-pack mechanism in `internal/prompts/audit/<topic>.md` paralleling the language-pack discovery (`prompts.GetAuditPack`, `prompts.AvailableAuditTopics`); new `internal/audit/` package with walker (chunks by directory, soft 256 KiB cap per chunk), runner (drives the LLM and parses findings), and renderer (text + markdown + JSON). New `internal/git.TrackedFiles()` helper using `git ls-files -z` (NUL-separated so paths containing newlines round-trip). `--dry-run` prints the chunk plan without invoking the LLM so users can preview cost before paying tokens; `--include` / `--exclude` filter to a subset; `--out <path>` writes markdown to a file (or JSON when the extension is `.json`). Day-1 ships two topics: **security** (OWASP-aligned sweep — hardcoded secrets, missing authorization, weak crypto, injection surfaces, path traversal, …) and **tech-debt** (dead code, duplicated logic, leaky abstractions, inconsistent error handling, architectural smells). Audit packs deliberately skip the `nit` severity tier — whole-codebase reading produces enough signal that nits dilute the report. Closes the solo-dev gap that `review` couldn't reach: "I'm shipping into main alone; what's accumulating in code I haven't touched in months?"

- **Three new language packs: Swift, Kotlin, Liquid.** Activates automatically on `.swift`, `.kt`, `.kts`, and `.liquid` files. Each pack is structured the same way as the existing `go.md` / `python.md` / `rust.md` / `typescript.md` — sections on language-specific pitfalls (null safety, concurrency, lifecycle/memory, error handling, idioms) plus security, with finishing notes on style. **Swift** covers optionals + force-unwraps, ARC reference cycles, Swift Concurrency (`@MainActor` / `Task` / `async let`), SwiftUI state, value-vs-reference, and iOS-flavoured security. **Kotlin** covers `!!`, platform types, coroutines + structured concurrency, Android lifecycle leaks (Activity / Fragment / `Flow.collect` outside `repeatOnLifecycle`), `==` vs `===`, data-class invariants, and Android-flavoured security (`WebView`, `addJavascriptInterface`, `EncryptedSharedPreferences`). **Liquid** covers Shopify-theme XSS surfaces beyond default auto-escape (JS context, URL context, `| raw`), performance traps (unbounded `for` over `collections.all.products`, missing `paginate`, image sizing), Liquid-logic correctness (`blank` vs `nil`, `forloop.index` off-by-ones, `assign` scoping), Shopify-specific conventions (`{% render %}` over `{% include %}`, `| t` translations, `| money`), and theme accessibility. Closes the customer-feedback gap that 60% of the user's daily code (iOS Swift / Android Kotlin / Shopify Liquid) previously fell through to the generic `default.md` pack.

- **`local-review bench --swe-bench` — SWE-bench-lite catch-rate mode.** A second bench section ("SWE-bench-lite catch rate") in `bench/RESULTS.md` measuring how the reviewer performs on **bug-introducing diffs in the SWE-bench-lite format** — diffs adapted by reverse-applying a known fix patch, scored by case-insensitive keyword match between the LLM's review markdown and the task's `expected_keywords`. v0.10.0-a ships 3 **synthetic SWE-bench-shaped examples** (paginator, retries, ORM SQL injection); real-task curation from the upstream SWE-bench-lite dataset (target N=10) lands as a follow-up commit before the v0.10.0 tag, at which point the catch rate measures performance against real bugs from real Python projects we did not author — the credibility signal that closes v0.8/v0.9's "circular benchmark" critique. Loads tasks from `bench/swe-bench-lite/<id>/{case.yaml, diff.patch}`, runs the existing multi-LLM review path on each diff. v1 is binary `caught` / `missed`; a partial-credit LLM-as-judge tier is tracked for a later release. New flags `--swe-bench` and `--swe-bench-dataset <dir>` on the existing hidden `bench` subcommand; new `SWEBenchReport` JSON shape (text via `WriteTextSWE`, markdown via `WriteMarkdownSWE`); refuses `--uplift` and `--repeat > 1` loudly (neither concept maps onto binary catch scoring in v1). Error frames count toward the catch-rate denominator — a reviewer that crashes catches no bugs, and silently shrinking the denominator to the surviving subset would inflate apparent catch rate exactly when reviewers are flakiest.
- **`bench/swe-bench-lite/` dataset with 3 example tasks.** Day-1 ships synthetic SWE-bench-shaped examples covering a paginator off-by-one (integer-division-vs-ceil), a retry loop swallowing the wrong exception class, and an ORM SQL-injection regression. Clearly labelled as examples in the dataset README — real tasks curated from upstream SWE-bench-lite (target: N=10) land in a follow-up commit before v0.10.0 ships.

## [0.9.0] - 2026-05-11

**Theme: make the tool sell itself.**

Driven by direct customer feedback on v0.8.0: the bench harness was honest signal, but the README led with copy-pasteable shell blocks instead of the trust numbers, and the bench itself was a user-facing surface area users weren't going to use. v0.9.0 reorders the story.

### Added

- **Bench overhead deltas — time + tokens per case in the leaderboard.** `bench/RESULTS.md` and the `--uplift` text/markdown reports now carry an "Overhead vs raw model (lower is better)" sub-table next to the existing quality-uplift block. Two columns: `Time/case (Δ)` and `Tokens/case (Δ)`, both as `treatment (signed Δ vs baseline)` so positive Δ = treatment costs more than the raw-LLM baseline (expected direction; the table tells you *how much* more). Tokens thread through the live path via `cli.TokenUsage` and are stored per-case on `BaselineScore` and `CaseScore` (new `InputTokens` / `OutputTokens` fields plus `TreatmentDurationMs`, all `omitempty`). The per-case fields are paired and rolled up into a new `LLMReport.Overhead` (`OverheadAggregate` struct: `PairedCases`, `TreatmentDurationMs` / `BaselineDurationMs`, `TokenMeasuredCases`, and treatment/baseline token sums). Two denominators on purpose: time uses `PairedCases` (every paired success); tokens use `TokenMeasuredCases` (only paired successes where BOTH sides reported non-zero tokens) so partial CLI-parser coverage doesn't divide a real total by an inflated count. Tokens cell dashes when `TokenMeasuredCases == 0`; partial coverage surfaces as an inline note ("tokens measured on M of N paired cases; mean is over the token-known subset only"). Closes the negative side of the v0.8.0 quality-only story: the customer-facing question "is the quality uplift worth the extra spend?" now reads on one page.

### Changed

- **`local-review bench` is now a hidden subcommand.** The harness still ships and still runs when invoked by name (`local-review bench --replay ...` works for CI workflows and contributors regenerating `bench/RESULTS.md`), but `local-review --help` and shell completion no longer surface it. Bench is project-team tooling; the user-facing artifact is the committed [`bench/RESULTS.md`](bench/RESULTS.md), regenerated by CI. Reasoning: surfacing the harness as a top-level user command bloated `--help`, the documentation surface, and the support volume, for a feature only a tiny fraction of users would ever run.
- **README rewritten positioning-first.** New top-of-file order: *Why local-review* (what the tool exists for), *What it is, what it isn't* (the two-column scope table), *How good is it?* (new section pointing at `bench/RESULTS.md` and enumerating what the leaderboard tracks), then *Get started* (the 5-step install/auth/review block). Detail sections (Multi-LLM, Configure, CLI, Prompt packs, Privacy, …) stay below as the "detailed doc" the positioning sections link into. The v0.8.0 action-first ordering buried the trust signal under copy-pasteable shell blocks — v0.9.0 leads with the trust signal and follows with action.
- **`bench --uplift` (originally in v0.8.0's `[Unreleased]` block, never released under that header).** Measure treatment vs baseline on the same labelled dataset. Each `(case, LLM)` pair runs twice: once with `prompts.BaselinePrompt` (a deliberately minimal generic system prompt — the kind of thing a developer would type into Claude.app without specialised tooling) and once with the full local-review pipeline (language-specific pack via `prompts.Resolve` plus the multi-LLM merge surface). The leaderboard's "Uplift over baseline" block per LLM shows `treatment (Δ vs baseline)` for F1 / precision / recall / noise, with signed deltas so a regression (negative Δ on F1, or positive Δ on noise) is unambiguous at a glance. Live mode only; replay rejects `--uplift` outright (cached fixtures for both sides would just measure the fixtures' similarity to themselves). Costs roughly 2× the tokens of a normal bench run.
- **Baseline JSON fields in the bench `Report` (also originally in v0.8.0's `[Unreleased]`).** `LLMReport.Baseline` (per-LLM aggregate including `measured_non_clean_cases` and `measured_clean_cases` — the per-bucket counts of baseline passes that actually returned scoreable output, gated independently because F1/precision/recall are undefined when no non-clean baseline scored, and noise is undefined when no clean baseline scored) and `CaseScore.Baseline` (per-case TP/FP/FN/Produced/DurationMs). Both pointer-typed so JSON consumers can distinguish "not measured" (absent) from "feature attempted, every case errored" (present with both counts at zero). Per-case baseline failures surface as `CaseScore.BaselineError`; the renderer dashes out affected delta cells. `--strict` (default ON in `--replay`) exits non-zero when any baseline pass errored.
- **Explicit `Clean` field on `CaseScore`.** Mirrors `Case.Clean` / `len(Expected)==0`. Aggregate code paths read it directly instead of inferring "this case is clean" from treatment-side TP/FN counts — the previous heuristic happened to be correct for the canonical scoring path but coupled baseline aggregation to treatment internals.

### Fixed

- **CHANGELOG hygiene: v0.8.0 header was missing.** The v0.8.0 release tag (2026-05-09) shipped with its CHANGELOG block left under `## [Unreleased]` — no `## [0.8.0]` header existed, so renderers folded v0.8.0 features into "Unreleased" indefinitely. v0.9.0 inserts the missing header in the right place and promotes the surviving `[Unreleased]` items (uplift + baseline fields + Clean + README) into the v0.9.0 block above.

## [0.8.0] - 2026-05-09

**Theme: measure what you ship, customise what you check.**

This minor lands two things teams have been asking for: an objective signal for "is the review actually any good?" (the new `bench` subcommand) and a supported way to bend the bundled rules to match a team's house style (issue #55 prompt customization). Plus a redesigned landing page that explains both.

### Added

- **`local-review bench` subcommand — quality benchmark harness (#56).** A reproducible signal — precision / recall / F1, noise rate, consistency, per-language splits — for prompt + model changes. Loads a labelled dataset of diffs, runs each through every active LLM (or pre-recorded fixtures via `--replay`), and scores per-agent and per-language. Two output sinks: text summary by default, JSON via `--json` / `--out <file>` for cross-commit diffing, plus a markdown leaderboard via `--markdown bench/RESULTS.md` for committing. Live mode supports `--repeat N` for Jaccard consistency across runs (replay refuses `>1` because fixtures are deterministic). Ships with a 10-case starter dataset (Go nil-deref + race + error-shadow, TS SQLi + XSS, Python shell-injection + yaml-load, Rust unsafe FFI, two clean cases) and matching claude / codex / gemini fixtures. New `Bench (replay)` CI workflow runs on PRs touching prompt packs or the harness, deterministic and token-free. See `bench/README.md` and `bench/RESULTS.md`.
- **Prompt customization — `prompts.pack_dir` + `prepend` + `append` (#55).** Three lighter knobs than forking: a per-language override directory (drop a `go.md` in there to replace the embedded Go pack; missing files fall through), inline `prepend` text spliced before every pack body (house rules that colour the whole review), and `append` for output-shape rules. All three apply to BOTH the multi-LLM CLI invokers and the single-LLM fallback path so customizations reach every reviewer. New `--prompt-pack-dir <dir>` flag for one-off runs. `local-review config` now prints "Resolved prompt sources" so you can see which file each language actually loaded. `local-review doctor` actively probes the prompt configuration and warns on every misconfiguration the resolver tolerates silently: missing `pack_dir`, `pack_dir` pointing at a file (not a directory), `pack_dir` with no `<language>.md` files matching a shipped pack (so a stray `README.md` doesn't silence the diagnostic), and known-language override files that are present but unreadable (perms drift, NFS hiccup). The resolver itself stays fall-through-on-error so a transient FS glitch can't kill every review; doctor surfaces the same conditions once at setup-check time.
- **Redesigned landing page (`docs/index.html`).** Hero CTAs split into a primary row (`Download`, `Checklist`) plus an auxiliary icon row (GitHub, author site). New "Want the checklist behind the tool?" section with a live HTML preview of `CHECKLIST.md` styled as a screenshot. WCAG-AA contrast on every severity badge in the preview, keyboard-focus rings on the icon links, screen-reader-aware (mock content marked `aria-hidden`).

## [0.7.2] - 2026-05-07

### Fixed
- **Stability-week audit (PR #52).** Eight findings cleaned in a four-iteration dogfood loop: gofmt drift slipping past CI (added `gofmt -s -l .` step), codex parser false-positive on the streaming-then-tempfile duplicate output, merger crash on empty review set, merger now fences each review in `<review llm="...">` blocks with sanitised attributes (defends against prompt-injection from review content), storage sanitises vendor-supplied LLM name/version strings before path-construction, `SaveMetadata` errors no longer swallowed, doc comments softened on cached-re-run token totals, and `selectMergeLLM` roster-order iteration locked in by tests. Three follow-up iterations addressed `stripTrailingDuplicate` over-stripping on single-copy stdout, escaping a hallucinated `</review>` tag in content, and lifting a per-loop regex compile to package level.

## [0.7.1] - 2026-05-07

### Fixed

- **Claude token display: cache-served prompts no longer collapse to single-digit input.** Pre-fix, `parseClaudeJSON` excluded `cache_read_input_tokens` and `cache_creation_input_tokens` from the displayed input count on the theory that "those represent reuse, not new spend." In practice that meant a re-review of the same diff (where almost the whole prompt is served from Anthropic's prompt cache) rendered as `claude ✓ · 9 in / 5.2k out` — which read as a broken parser. v0.7.1 sums all three input components (`input_tokens` + `cache_read_input_tokens` + `cache_creation_input_tokens`) so the displayed `· N in` answers the question users actually have ("how big was my prompt?") rather than "how much was uncached new spend?" — that latter number lives in the vendor's billing dashboard, not the CLI summary line.

- **Codex token display: regex was matching context-window indicators instead of the actual usage summary.** Pre-fix, `parseCodexStdoutTokens` used a single permissive regex `(?i)tokens(?:\s+used)?:\s*(\d[\d,]*)...` that matched *any* line containing "tokens:" — including context-window indicators (`Total tokens: 800`, `Available context tokens: 800`) elsewhere in the codex banner. On real runs this surfaced as the misleading `codex ✓ · 800 total` line on prompts that were clearly larger than 800 tokens. Worse: the regex also failed to match codex v0.128's *actual* token-summary format, which puts the label and number on **separate lines**:
  ```
  tokens used
  2,415
  ```
  v0.7.1 splits the matcher into three strict patterns — split shape (`tokens: <in> input, <out> output`), v0.128 newline shape (`tokens used\n<total>`), and pre-v0.128 single-line legacy (`tokens used: <total>`) — each anchored with `\b` so misleading prefixes like "Total tokens" don't false-positive. Selection is **latest-position-across-all-three-patterns** rather than first-match: the assistant's reply is in the same combined buffer as the metadata and can contain pattern-shaped text, so only the rightmost match is reliably the real session summary. Real codex v0.128 stdout was captured to write the regression tests against actual output.

## [0.7.0] - 2026-05-07

**Theme: smarter, more visible reviews.**

This minor bundles three features that, together, move multi-LLM runs from "black box that prints a report" to "tool that tells you what's about to happen, what's happening, and what each agent cost." Each feature shipped as a v0.6.x patch over the past two days; v0.7.0 frames them as one coherent narrative with a release announcement.

### Bundled patch releases

- **Diff-too-large preflight** *(v0.6.5, see entry below)* — agents whose context window can't fit the prompt + diff are skipped *before* the run starts, with a one-line hint on how to scope smaller. No more 5-minute fan-outs that fail with N opaque errors on squash-merged release branches or vendored-blob diffs.
- **Per-LLM token visibility** *(v0.6.6, see entry below)* — every completion line shows what that agent consumed (`· 12.3k in / 4.5k out`); the closing line aggregates the run total. Same data persists in `<commit>_metadata.json` so paid-tier users can attribute spend per PR without round-tripping the vendor dashboard.
- **Live progress streaming** *(v0.6.7, see entry below)* — per-agent lines print as each agent finishes, not all-at-once after the slowest one. A 5-min gemini run no longer looks like a hung terminal. Dropped the `[N/M]` numeric prefix from the per-LLM line — with streaming, the number would track "how many have finished" rather than roster position.

### Fixed

- **Timeout-error hint pointed at a non-existent config field.** When an agent timed out, the failure message read `... raise llms.<agent>.timeout_sec in .local-review.yml` — but the actual YAML key is `timeout_seconds`. Pasting the suggested fix into a config left it untouched. Hint and `classify_test.go` now reference `timeout_seconds`. Discovered during the v0.7 doc audit (`internal/cli/classify.go`).
- **Landing page (`docs/index.html`) "Codex disabled by default" claim was wrong.** Codex's `Enabled` field is `nil` (= "run if active") in `Defaults()` — same posture as claude and gemini. Landing page now reads "Enabled when authenticated."

### Docs

- **Doc audit pass for v0.7.0 release**: `SECURITY.md` support matrix bumped (0.7.x active, 0.6.x exception-only); `CLAUDE.md` `metadata.json` example now includes the v0.6.6 token fields (`input_tokens`, `output_tokens`, `total_only_tokens`); `README.md` adds a "What's new in v0.7" callout and corrects the "structured-JSON multi-LLM is on the v0.7 roadmap" claim (now post-v0.7); cosign-signing roadmap reference in the v0.6.0 entry corrected for the same reason.

## [0.6.7] - 2026-05-07

### Added
- **Live progress streaming for multi-LLM runs.** Per-agent completion lines now print as each agent finishes instead of all-at-once after the slowest one. Pre-fix, when one agent (commonly gemini-3.x-preview at 5+ min) dominated runtime, users saw the roster, then a blank terminal for minutes, then every result + the merge step + findings in a single burst — no way to tell whether the tool was working, hung, or stuck on a specific agent. Now:

  ```
  Reviewing feat/v0.6.7-live-progress (3016d29) with 3 LLMs...
    • claude_claude-opus-4-7 (CLI v2.1.132) | timeout: 600s
    • gemini_gemini-3.1-pro-preview (CLI v0.40.1) | timeout: 600s
    • codex_gpt-5.3-codex (CLI v0.128.0) | timeout: 600s

  codex ✓ (10.4s) · 14k in / 4k out         ← appears at t=10.4s
  claude ✓ (51.5s) · 12.3k in / 4.5k out    ← appears at t=51.5s
  gemini ✓ (287.1s) · 15k in / 3k out       ← appears at t=287.1s

  Merging reviews...
  ```

  Emission order = completion order. The previous `[N/M]` numeric prefix was dropped because with streaming it would track "how many agents have finished" rather than roster position — visually the same but semantically different, and we'd rather drop the number than have it silently change meaning.

### Internal
- `Orchestrator.RunParallel` now returns `(<-chan ReviewResult, error)` instead of `([]ReviewResult, error)`. The channel is buffered to `len(llms)` so a slow consumer can't deadlock workers, and is closed after all per-agent goroutines finish so callers can `for r := range ch`. Per-agent failures still travel inside `ReviewResult.Error` (channel always emits one result per LLM, regardless of outcome). Added `Orchestrator.invokerFactory` (in-package test seam) so streaming behavior can be pinned with controlled-duration fakes — paid down a sliver of the parked `internal/multi/` test debt while we were here.

## [0.6.6] - 2026-05-07

### Added
- **Per-LLM token usage on every review.** `local-review review` now displays input/output token counts per agent on its completion line and aggregates a total at the end of the run:

  ```
  [1/3] claude ✓ (51.5s) · 12.3k in / 4.5k out
  [2/3] gemini ✓ (102.4s) · 15k in / 3k out
  [3/3] codex ✓ (85.0s) · 14k in / 4k out
  ✓ 3/3 LLMs produced output · total 2m51s · ~54k tokens
  ```

  Token counts come from each CLI's structured output (claude `--output-format json`, gemini `-o json`) or stdout metadata (codex's session-summary block). Codex pre-v0.128 reports a single combined total instead of split input/output; we render that as "Nk total" rather than the misleading "Nk in / 0 out" (the model produced output, we just don't have the breakdown). Same data persists in `<commit>_metadata.json` per-review and per-merge, so paid-tier users (codex API, claude paid) can attribute spend per PR without round-tripping the vendor dashboard.

  **Minimum CLI versions for token visibility:** claude-code v1+ (any version supporting `--output-format json`), gemini-cli with `-o json` support (newer releases), codex any version. Older CLIs that lack the JSON-output flag exit non-zero on the flag and the run fails fast — there is no silent "no tokens" fallback for that case. If your run errored out after this upgrade, update the offending CLI: `npm i -g @anthropic-ai/claude-code @google/gemini-cli @openai/codex`.

### Internal
- `Invoker.Review` and `Invoker.RunPrompt` now return a `cli.TokenUsage` alongside the response. `ReviewResult.Tokens` and `MergeMeta.{InputTokens,OutputTokens,TotalOnlyTokens}` plumb the data through. `TokenUsage.TotalOnly` flags the codex legacy single-total case so display callers render "Nk total" instead of "Nk in / 0 out". JSON parsers fall back to raw text + zero usage when valid JSON has an unexpected shape (a future schema drift); they do *not* compensate for older CLIs missing the structured-output flag — those exit non-zero before the parser is reached.

## [0.6.5] - 2026-05-07

### Added
- **Diff-too-large preflight.** Before fanning the diff out to agents, `local-review review` now estimates the token count (`bytes ÷ 3.5` — conservative for code) and compares against a per-agent context window: claude 200K, gemini 1M (floor for 2.5+/3.x), codex 128K (floor for gpt-4o-class). If the prompt + diff plus a 10K response margin would exceed an agent's window, the agent is skipped with a one-line warning explaining what fits and how to scope the run smaller (`local-review commit HEAD` or `local-review staged`). If *every* agent's context would overflow, the run errors out before any subprocess runs — saving the user the 2-minute fan-out + N opaque failures previously seen on squash-merged release branches and vendored-blob diffs. Agents whose name isn't in our context-window table (a future LLM, a hypothetical org-pack) pass through preflight unchanged so the rollout of a new agent type is never silently dropped.

## [0.6.4] - 2026-05-06

### Changed
- **Default per-agent timeout raised from 120s → 600s (10 min).** User feedback after v0.6.3: claude (Anthropic Sonnet on a thinking model) regularly took 2–5 min on branch-sized diffs while gemini and codex finished in 80–100s. The 120s default kept timing claude out on real-world `local-review review` runs even though the agent was making forward progress. 600s gives enough headroom for a worst-case agent on a worst-case diff while still failing fast on a genuinely hung subprocess. Users who want shorter timeouts can override per-agent via `llms.<agent>.timeout_seconds:` in `.local-review.yml`. Same bump applied to: per-agent default in `Defaults()`, the `applyConfig` fallback in the runner, the `RunParallel` fallback in the orchestrator, the merge-step fallback in `mergeAndPrint`, and the v0 single-LLM API path's `Provider.TimeoutSec` (60s → 600s).

## [0.6.3] - 2026-05-06

### Fixed
- **Failure lines now include actionable hints, not opaque error text.** Pre-fix, a SIGKILL'd CLI rendered as `[1/3] claude ✗ (claude review failed: signal: killed (output: ))` — three problems on one line: redundant `claude review failed:` prefix, empty-output noise (`(output: )`), and zero indication of *what to fix*. Real-user feedback was "I have all setup done, but it doesn't work — that's why users delete tools like this." Now: failures are classified by `internal/cli/ClassifyExit` into a one-line summary that always ends with an actionable next step. Examples:
  - `[1/3] claude ✗ killed — likely out of memory or a hard timeout for claude; try a smaller diff: \`local-review commit HEAD\` (last commit), \`local-review staged\` (staged only), or pin a smaller-context model via \`llms.claude.model:\``
  - `[1/3] claude ✗ timeout — try \`local-review commit HEAD\` for a smaller diff, or raise llms.claude.timeout_sec in .local-review.yml`
  - `[1/3] claude ✗ exit status 1: error: API key not valid. Please pass a valid API key.`

### Changed
- **Roster format**: configured-model agents render as `<agent>_<model>` (e.g. `claude_claude-sonnet-4-6`) so users see "what model is running" at a glance. Agents without a pinned model render as `claude (CLI v2.1.131) — using vendor's default model; pin via \`llms.claude.model:\``, replacing the previous `model: CLI default` which user feedback flagged as a non-answer. `local-review doctor` mirrors the same wording.
- **Per-LLM directory path printed after the agent block**, so users know where raw saved reviews live without grepping the storage convention. Format: `Per-LLM reviews → .local-review/reviews/<branch>/`. Routed to stderr when at least one agent failed (so it stands out as a debugging hint), stdout otherwise.
- **Merge prompt Summary uses "Findings flagged by both reviewers" when ReviewCount==2**, instead of the abstract "2+ reviewers agree" which read as broken arithmetic to users seeing `0` next to a `REQUEST CHANGES` recommendation. N≥3 keeps the threshold-based wording since it's no longer trivially derivable from the reviewer count.
- **Merge prompt drops the redundant `Reviewers: <names>` line from the Summary block** — the same names appear in the agent roster ~30 lines earlier.

## [0.6.2] - 2026-05-06

### Changed
- **`--help` figlet banner restored.** v0.6.1 swapped the 4-line figlet for a single-line title; user feedback was that the flat line read as a regression after several releases of the figlet. Restored the small-font figlet (~70 cols) and the `term.GetSize ≥70` width gate. Non-TTY stdout still suppresses.
- **Landing page polish**: Codex card body trimmed to match Claude / Gemini in length so the three cards have equal height; cards' content vertically centered (flex column + `justify-content: center`); scope-block "What it is" heading bumped from `#22543d` → `#15803d` to match the right column's red `#c53030` weight; inline `<code>` no longer renders as light-green-on-white (default → dark slate, scoped slate-100 pill on `.scope-col`, terminal-block green preserved on `pre code`); Codex pill renamed `Paid` → `$ OpenAI` with a 0.75em italic caveat inside the card ("You pay OpenAI. **local-review** is 100% free") so visitors don't think we charge.
- **Prompt-pack default rules expanded** to close ~30% coverage gap vs `mgreiler/code-review-checklist`. New top-level sections: Error handling and logging; Backward compatibility and dependencies; Usability and accessibility; Ethics and fairness; Specialist review needed? Plus SOLID names under Maintainability, comment hygiene checks, file/package-location smell, code-that-is-hard-to-test smell. Updated JSON `tag` enum to match.

### Fixed
- **Banner cleanup from review-round refinements**: extracted magic `70` to a `bannerMinWidth` constant so a future banner swap updates one place; stripped the implicit leading newline from the `banner` literal so whitespace is owned by `helpHeader()` explicitly; updated the comment that claimed the banner "stays readable in CI logs" — CI logs are non-TTY and the banner is correctly suppressed there.

## [0.6.1] - 2026-05-06

### Changed
- **`--help` no longer leads with a 5-line figlet banner.** Replaced with a single-line title so the `git commit` editor and CI logs don't get a wall of ASCII art every time. Subcommands now group as Review / Setup / Other (canonical `review` first, not alphabetised behind `branch`).
- **Review header now states what's being reviewed.** `Reviewing release/v0.6.1 (1ed03b9) with 3 LLMs...` replaces the generic `Running review with 3 LLMs...` so readers don't have to scroll past N findings to learn the branch + commit.
- **Closing line includes total wall-clock**: `✓ 2/3 LLMs produced output · total 2m 51s`. Pre-fix users had to mentally sum per-LLM durations + merge duration to know how long a run took.
- **`local-review doctor` shows the configured model** under each ready CLI (e.g., `model: claude-3-5-sonnet-20241022`) so misconfigured models surface before triggering an expensive review.
- **Merge prompt drops the editorial `## Conclusion` section.** `## Summary` already carries the Recommendation verdict the gate reads; the second narrative paragraph was redundant and noisy in the CLI.
- **Multi-LLM model defaults are now empty — vendor CLIs pick their own current stable.** Pre-fix the `Defaults()` config pinned `claude-3-5-sonnet-20241022`, `gemini-1.5-pro`, and `gpt-4` — model IDs from late 2023 and 2024, between 12 and 24 months stale by v0.6.x. We don't release on a vendor-rotation cadence, so hardcoded IDs were guaranteed to age into noise. Each invoker now skips passing `--model` when no value is set, and the CLI uses whatever the vendor currently considers stable. The roster line displays `(model: CLI default)` and `local-review doctor` shows `model: (CLI default)` so users can tell "I didn't pin one" from "config didn't load." Users who want to pin a specific model should set `model:` explicitly per LLM in `.local-review.yml`. A `TestDefaults_MultiLLMModelsAreEmpty` test guards against a future contributor silently re-introducing the staleness problem.

### Fixed
- **Single-LLM-survivor runs are no longer mis-framed as "Merged review".** When a multi-LLM run starts with N≥2 agents but only 1 produces output, the review is single-source — there is no cross-model consensus. The CLI now prints a `⚠ Only X of N LLMs produced output` warning, calls the post-step "Reformatting" instead of "Merging", and labels the saved file as a "Single-LLM report". Solo runs invoked via `--only <agent>` are also relabeled to drop the misleading "Merged" framing. The closing line reads `✓ X/Y LLMs produced output · total Ns` (was "succeeded"), aligning with the classifier's basis. The classifier counts non-empty Output (matching what the merger consumes), not `Error == nil`, so a SaveReview-after-success failure doesn't accidentally demote a real merge. The merge step still runs (it produces the structured Recommendation line the pre-commit gate reads); only the user-facing language changes.
- **Merger output no longer leaks ```markdown fence wrappers.** Some merger LLMs ignore the "Return ONLY the merged markdown report" instruction and wrap the result in a code fence; pre-fix the literal triple-backticks bled through to stdout AND were saved into `<commit>_merged.md`. The fence is now stripped when both opener and closer are present (a partial/truncated wrapper is left alone to avoid half-stripping).
- **Consensus threshold no longer asks the impossible.** Pre-fix the merge prompt always said "if 3+ reviewers agree, consolidate" even when only 2 agents ran — the LLM apologised in its own summary line ("0 (only 2 reviewers, but 3 issues have 2/2 consensus)"), reading as a broken template. The threshold is now clamped to the actual reviewer count before being passed into the prompt.

### Docs
- **README and landing page gain a "What it is, what it isn't" block** at the top, before the feature list. Heads off the recurring confusions that surfaced in launch feedback (vs Claude's `/simplify`, "is it really local?", SaaS vs CLI, LLM vs linter).

## [0.6.0] - 2026-05-05

### Added
- **Multi-LLM honors language-specific prompt packs.** Pre-fix, every agent ran with a generic 4-bullet prompt while README claimed pack-aware reviews. Now `lang.Dominant` + `prompts.Get` feed each agent the same Go/TS/Python/Rust pack the single-LLM path uses, with a markdown-output override appended so the merger can consolidate prose across reviewers.
- **`api_key_env` works end-to-end.** Doctor now reads the configured env var (not the hardcoded canonical default), and invokers inject the resolved key into the subprocess as the canonical name each CLI expects. A user with `api_key_env: MY_GEMINI_KEY` sees ✓ ready and gemini actually authenticates from `$MY_GEMINI_KEY`.
- **Per-agent model overrides actually take effect** — `--claude-model`, `--gemini-model`, `--codex-model` (plus the matching config fields) are now threaded through to the CLI command line. Pre-v0.6 they were displayed in the roster but never passed to the invoker.
- **`cli_path` honored** — corporate / nix-store installs at non-standard paths now work (was hardcoded to PATH lookup).
- **`local-review config` applies CLI flag overrides** as the docs claim (was previously claimed in --help but ignored).
- **Multi-LLM honors `review.include` / `review.exclude` globs.** Previously the multi path bypassed the filter entirely and reviewed files the user had told it to skip.
- **`local-review review` (already in v0.5) gains `--only` strictness**: when `--only` matches no ready agents (typo, unauthed agent), the run errors out instead of silently falling back to the configured `provider:` — that fallback would have sent the diff to a different vendor than the one named, a privacy / cost footgun.
- **Multi-LLM blocking gate has two independent signals**: the merged report AND each per-LLM Output (full, before merger truncation). Defends against a verbose reviewer pushing a Critical finding past the 8 KB merger-input cap.
- **`install.sh` verifies SHA-256 checksums** of release tarballs. Each release now ships a `checksums.txt` manifest; the installer downloads it alongside the tarball and verifies before extracting. Defends against accidental corruption and basic CDN/MITM tampering. Cosign signing is on the post-v0.7 roadmap for compromised-release-key defense (originally targeted v0.7; deferred — v0.7.0 ships without it).

### Changed
- **`ParseSeverity` defaults to `major` for unknown values** (was `warning`). LLM typos (`"criticl"`, `"sev-high"`, `"BLOCKER"`) used to silently demote out of the blocking range, hiding real findings from the pre-commit hook. Now: unknown → fail-closed at major. **Heads-up:** previously-passing reviews where the LLM emitted a typo'd severity may now block; rerun with the typo corrected, or treat the new failure as the gate working correctly.
- **JavaScript routes to the TypeScript pack.** No separate `javascript.md` ships — the TS pack covers React/Next.js/Node patterns that apply equally to plain JS.
- **CodexInvoker uses `codex exec --output-last-message`** instead of bare `codex` (which was launching the interactive TUI in v0.5.0 and aborting every codex review with `exit status 1`).
- **`--help` banner switched to figlet small font** (~70 cols) so it doesn't garble narrow terminals.
- **Privacy posture documented honestly.** README's "Privacy" section was overclaiming for default multi-LLM (which fans the diff out to Claude/Gemini/Codex cloud backends). Replaced with a per-mode matrix.

### Fixed
- **Diff parser preserves deleted-file paths.** `+++ /dev/null` is the standard `git diff` shape for a deleted file; the old parser took the path from `+++` and attributed every deletion to "/dev/null", silently breaking glob filtering and finding attribution.
- **Diff parser doesn't mistake hunk content for headers.** A deleted SQL comment `-- a/users` rendered inside a hunk as `--- a/users` (with the diff's leading `-`); the old parser matched that as a file header.
- **Diff parser uses `bufio.Reader` (no per-line cap)** instead of `bufio.Scanner` (4 MB cap → `ErrTooLong` on a single minified-bundle line, aborting the entire review even if globs would have excluded that file).
- **`parseUnifiedDiff` fails closed on scanner / I/O errors** — a partial-read diff can't satisfy the gate. The earlier "best-effort, swallow" comment was the wrong call.
- **`parseFindings` brace-counting JSON extractor** instead of first/last-`{` heuristic. The old approach concatenated example + answer when an LLM emitted both, failing to unmarshal.
- **LLM client caps response at 10 MB.** Defends against runaway providers / spoofed endpoints — the old `io.ReadAll` was unbounded.
- **LLM client only sets `Authorization: Bearer ...` when a key exists.** Empty-token header used to break local OpenAI-compat servers (Ollama, vLLM) that expect the header absent in unauthenticated mode.
- **Ollama "no API key" path works.** When `base_url` points at localhost / 127.x / ::1, the empty-key check is skipped — required for the documented fully-offline workflow.
- **Globs compile once, not per file.** Pre-fix `matchGlob` re-compiled the regex inside the per-file loop; a 500-file diff with 5 globs cost 2,500 compiles. Plus: `**/dist/**` now correctly anchors to a path-segment boundary (was matching `src/mydist/file`), and bracket character classes (`[0-9]`, `[!a-z]`) actually work.
- **Fail-closed on all-invalid include globs.** Previously `include: ["[!]"]` (and other compile-error patterns) silently *expanded* the review to every file because the empty `includeRE` was treated as "no include filter".
- **`Authorization` / `--json` / `--min-severity` / `--max-findings` ignored in multi-LLM** — README now states this explicitly instead of "passes them through with a warning". Multi-LLM emits a stderr warning when those flags are set but otherwise drops them; the structured-JSON multi-LLM mode is on the post-v0.7 roadmap (deferred from v0.7.0; unparking when the third user asks).
- **`mergeAndPrint` truncates per-review output to 8 KB** before feeding the merger, defending against verbose reviewers blowing the merger's context window. Independent gate signal scans the full per-LLM Output to catch findings past the cutoff.
- **Git refs are validated** — `--`-prefixed refs (e.g. `local-review commit -c`) and refs with NUL/newline are rejected to defend against `git` flag-injection.
- **Output write errors propagate** — `WriteText` and `configCmd` no longer silently exit 0 on broken pipe / disk full.
- **GeminiInvoker / ClaudeInvoker preserve subprocess output on failure** — auth/rate-limit/quota errors no longer collapse to a bare "exit status 1".
- **Storage SHA normalized** — full and short SHAs hit the same storage key, so re-running with `local-review commit <full-sha>` doesn't create a duplicate artifact.
- **Detached HEAD review writes to `detached-<short-sha>/`** instead of colliding under `HEAD/`.

### Removed
- **`LLMConfig.Mode` field**: never wired through to the runtime. yaml.v3 silently ignores stray `mode:` lines, so existing configs keep loading.
- **JavaScript constant in `internal/lang`** — unused after the JS-→-TypeScript routing.

### CI / release pipeline
- **Release workflow gates on Copilot review.** Release-labeled PRs that haven't received a Copilot review (or that have unresolved Copilot threads) fail the prepare job loudly instead of tagging silently. Both `PullRequest.reviews` and `reviewThreads` are queried; `PENDING` and `DISMISSED` review states are excluded from the "Copilot reviewed" check.
- Workflow `paths-ignore` updated to skip docs-only changes.
- Release workflow generates `local-review_<version>_checksums.txt` and uploads alongside binaries; `install.sh` consumes it.

## [0.5.1] - 2026-05-05

### Fixed
- **Pre-commit gate could exit 0 when the merge step failed.** If per-LLM reviews succeeded but the LLM-powered merge step failed (merger CLI down, save failed, etc.), `mergeAndPrint` returned an empty string, `mergedHasBlocking("")` was false, and `runMultiLLMReview` returned `nil` — meaning the command exited 0 even though no gate ever ran. A pre-commit hook would treat the commit as clean. Now: empty merged content returns a tool-failure error (exit 1, fail-open per project policy) so the user sees that the gate didn't run.
- **Multi-LLM path refused to run in detached HEAD.** `git rev-parse --abbrev-ref HEAD` returns `"HEAD"` in detached state (not an error), so `resolveCommitBranch` either errored out or stored every detached review under one `HEAD/` directory — colliding all CI runs, `git checkout <tag>` reviews, and bisect sessions together. The v0 single-LLM path worked there; v0.5.0 regressed it. Now: detached HEAD gets a synthetic `detached-<short-sha>` branch name so storage stays organized and the gate runs normally.

## [0.5.0] - 2026-05-05

### Added
- **`local-review review`** — canonical, friendly command. Reviews the current branch with every LLM CLI you have installed AND authenticated, in parallel, and prints a merged report. This is the "one command, three teammates" workflow the project was always supposed to ship with.
- **`--only <list>` flag** — restrict the agent set (e.g. `--only claude,gemini`). Overrides config: `--only codex` runs codex even when `codex.enabled: false` is set.
- **Per-agent model overrides** — `--claude-model`, `--gemini-model`, `--codex-model` to pin a specific model for one agent without editing config.
- **ASCII banner in `--help`** — figlet Block-font "LOCAL-REVIEW".
- **Findings printed inline** after multi-LLM merge instead of just "saved to file" — you see the report in your terminal without `cat`-ing the merged.md.

### Changed
- **Multi-LLM is now the default** for `staged|commit|branch|review`. Previously you had to run `local-review multi <cmd>`; now plain `local-review staged` (etc.) detects which LLM CLIs are active and runs them all in parallel. Falls back to single-LLM via the configured `provider:` only when no CLI is active.
- **Codex runs by default when authenticated.** Previously codex was opt-in via `enabled: true` in config — surprising for users who'd run `codex login` and expected it to "just work". Now an LLM CLI runs whenever it is installed + authenticated; `enabled: false` is the explicit opt-out.

  ⚠ **Heads-up if you have `OPENAI_API_KEY` exported for unrelated reasons** (e.g., other tooling): the codex CLI treats that env var as authentication, so v0.5+ will start invoking codex on every review. To keep the previous behavior, add to your `.local-review.yml`:
  ```yaml
  llms:
    codex:
      enabled: false
  ```
  Or, when you do want codex for one run only, pass `--only codex` (flags override config opt-outs).
- **`--model` and `--base-url` are explicitly single-LLM-fallback flags now** (clarified in `--help`). For multi-LLM use the per-agent overrides.

### Removed
- **`local-review multi <subcmd>`** — deleted entirely. Promoted to the default behaviour of the existing review commands. No deprecation period because there were no released users; if you scripted against `multi`, drop the prefix.

### Fixed
- The "no API key" error message (added in v0.4.4) now suggests `local-review review` instead of the deleted `local-review multi <cmd>`.

## [0.4.4] - 2026-05-05

### Fixed
- **Misleading "no API key" error.** Single-LLM commands (`local-review staged|commit|branch`) used to error with `set LOCAL_REVIEW_API_KEY or provider.api_key` regardless of what the user's actual config expected. Setting `OPENAI_API_KEY` (or whatever your `provider.api_key_env` names) and getting the same error anyway was a real first-run trap. The error now names the env var the resolved config is actually reading from, suggests `local-review init` to scaffold a config, and points at `local-review multi <cmd>` as an alternative for users who only have CLI auth (e.g., `claude login`) and no provider API key.

## [0.4.3] - 2026-05-04

### Fixed
- **CLAUDE.md** Codex auth note now includes the OPENAI_API_KEY path (was claiming "ChatGPT Plus subscription" only — contradicted v0.4.2 release notes).
- **README.md** auth-table column header renamed `API-key alternative` → `Alternative` to match the Gemini row's contents (which point to OAuth, not an API key).
- **`local-review doctor`** Gemini install hints now lead with `GEMINI_API_KEY` (the project's preferred path per the README) and list `gemini /auth` as the alternative — was the other way around.
- **`local-review doctor`** now propagates I/O write errors. Previously `local-review doctor > /dev/full` (or any broken-pipe scenario) would silently exit 0 even though writes had failed.
- **CHANGELOG** added the `[Unreleased]` section per Keep a Changelog convention.

## [0.4.2] - 2026-05-04

### Added
- **`local-review doctor` shows real authentication status per CLI.** Previously the command checked only "is the binary in PATH" and printed a misleading "ready" even when the CLI was unauthenticated. Now each CLI gets one of four explicit states with actionable next-step text:
  - ✓ ready (installed, version-detected, authenticated)
  - ⚠ not authenticated (install fine, missing credentials/API key — shows the exact command to fix it)
  - ⚠ install broken (binary in PATH but version probe failed — shows the resolved path so you can investigate)
  - ✗ not installed (shows install command)
- **Authentication section in README** documenting all auth paths per CLI: OAuth, API key, OS-keychain.

### Fixed
- **Codex auth misinformation across the project.** Doctor's install hints, the website's Codex card, the README provider table, and the multi-LLM example config all said "ChatGPT Plus required" — implying it's the only path. The OpenAI Codex CLI also accepts `OPENAI_API_KEY` (pay-per-token), which is usually **cheaper** than ChatGPT Plus for occasional review use. All five surfaces now make both paths explicit.
- **Doctor distinguishes broken installs from missing installs.** A binary in PATH whose version probe fails (broken symlink, corrupted install, missing runtime) used to be reported as "✗ not found" — hiding the resolved path and making the issue impossible to debug. Now it's a separate ⚠ state that shows the path and recommends reinstalling.

### Notes
- Auth detection uses heuristics where there's no API to query (Claude stores tokens in the OS keychain; we look for `~/.claude/sessions/` activity as proxy for "logged in"). False negatives are possible. False positives shouldn't happen — we never claim auth without concrete evidence (a sessions file, an env var, or an explicit `auth_mode` in `~/.codex/auth.json`).

## [0.4.1] - 2026-05-04

### Fixed
- `--json` mode now honors the blocking-finding exit gate (was incorrectly exiting 0 on major/critical findings).
- JSON output now emits `severity` as a string (e.g. `"major"`) instead of an integer, matching the documented contract.
- `parseUnifiedDiff` now strips trailing carriage returns when given CRLF-formatted input, fixing `\r`-suffixed paths in saved patches.
- `doctor` now reports `Available=false` for CLI providers when version detection fails (previously a stale PATH symlink showed as "ready"), and distinguishes broken installs (path shown) from missing installs.
- `doctor` install hint for Claude CLI now uses the correct npm package name (`@anthropic-ai/claude-code`).
- `examples/pre-commit` now references `local-review` and `LOCAL_REVIEW_SKIP` (previously a leftover from the project's earlier name).
- `gofmt` formatting issue in `internal/cli/invoker.go`.

### Changed
- Documentation: corrected stale Claude CLI npm package name across README, CONTRIBUTING, CLAUDE.md, and example configs.
- Documentation: updated SECURITY.md supported-versions table.
- Documentation: removed stale gemini/codex CLI version pins from CLAUDE.md and CONTRIBUTING.md (pins were already removed from the codebase's own install hints in commit 7c739f7).
- Documentation: corrected `CLAUDE.md` references to `merge.go` (the file is `multi.go`).

### CI
- Added `.github/dependabot.yml` for github-actions and gomod ecosystems.
- Pinned all GitHub Actions in CI/release workflows to commit SHAs (defense against floating-tag tampering).
- Bumped GitHub Actions to current major versions: `actions/checkout` v4 → v6.0.2, `actions/setup-go` v5 → v6.4.0, `actions/upload-artifact` v4 → v7.0.1, plus 2 others (PR #28).
- Bumped `github.com/spf13/cobra` from 1.8.1 to 1.10.2 (PR #29).

## [0.4.0] - 2026-05-04

### Added
- **`local-review init`** — interactive setup wizard. Walks through 5 questions (provider, model, API-key env var, severity floor, max findings) and writes a working `.local-review.yml`. Removes the biggest first-run friction.
- Provider presets shipped with `init`: OpenAI, Anthropic (Claude via OpenAI-compat), Mistral, DeepSeek, Ollama, plus a custom path for any OpenAI-compatible endpoint.
- `--force` flag on `init` for non-interactive script use.
- `--location=global` flag to write `~/.local-review.yml` instead of the project-local file.

### Changed
- **Website Quick Start** now leads with `local-review init` (3 steps) instead of the old manual 4-step npm-install flow. Matches the README.
- **Code review guidelines** (`docs/CODE_REVIEW_GUIDELINES.md`) significantly expanded based on FAANG, OWASP 2025, and 2026 industry research: added AI-generated code review as a first-class priority, observability, concurrency depth, API/backward-compat, comment-prefix conventions, process-norms numbers (PR size, review SLAs), automation-layer principle, PR template, and 60-second review checklist.
- **`docs/RELEASE_PROCESS.md`** rewritten to match the consolidated single-workflow pipeline (`release.yml`) instead of describing the old auto-release.yml + update-homebrew-formula.yml + release.yml split.
- **README provider table**: Anthropic row now links to the OpenAI-compat docs and notes the exact-model-name gotcha. Removed stale `v0.1:` and `v0.x` markers from section headings.
- **`CONTRIBUTING.md`** file structure block updated to match the actual `cmd/local-review/` and `internal/` tree (was listing `merge.go` which doesn't exist).

### Fixed
- Init wizard's input validation re-prompts on bad answers (severity, max-findings, provider choice) instead of aborting the whole wizard. Caps at 5 retries to avoid scripted infinite loops.
- Init wizard's rendered YAML quotes scalars via `strconv.Quote` so values containing `#`, leading reserved characters, or other YAML-special content produce a valid file.
- "Free tier via claude.ai" wording on the website corrected to "Free tier via the Claude CLI" (claude.ai is the consumer chat product, not the CLI auth path).

### Removed
- Stale launch-era docs (`DISTRIBUTION.md`, `OPEN_SOURCE_CHECKLIST.md`, `docs/RELEASE_SETUP_CHECKLIST.md`) moved to `do-not-merge/` (gitignored). They were planning artifacts from the v0.1.0 launch and made the project look pre-launch when GitHub visitors browsed it.

## [0.3.0] - 2026-05-04

### Added
- **Self-hosted fonts** on the website: Inter for body, JetBrains Mono for code (both SIL OFL). No third-party CDN requests; matches the "no telemetry" positioning.
- **DeepSeek and Mistral provider examples** in `examples/.local-review.deepseek.yml` and `examples/.local-review.mistral.yml` — copy-paste-ready configs.
- **Tests for `internal/llm/Client`** — 91% statement coverage on the HTTP client (constructor, request shape, error paths, context cancellation, network errors).

### Note on versioning
v0.2.0 → v0.3.0 was a label-discipline mistake on the release PR (`minor` applied where `patch` would have been more accurate). The published release notes on GitHub are accurate; this changelog covers what shipped. See [`docs/RELEASE_PROCESS.md`](docs/RELEASE_PROCESS.md) "Label cheat sheet" for the rule going forward.

## [0.2.0] - 2026-05-04

### Added
- **Rust prompt pack** (`internal/prompts/packs/rust.md`): Rust-specific review rules covering ownership/borrowing, lifetimes, async/futures, unsafe code, error handling, concurrency, and Cargo conventions. Activates automatically on `.rs` files.

## [0.1.1] - 2026-05-04

### Added
- **Homebrew distribution**: `brew install mshykov/tap/local-review` (macOS/Linux)

### Fixed
- `install.sh` now prints a copy-paste-ready one-liner for adding `~/.local/bin` to PATH, detecting the user's shell (zsh/bash/fish) instead of an abstract `export` line that left users with `command not found` after install.
- `local-review doctor` no longer prints hardcoded version pins (`@google/gemini-cli@0.40.0`, `@openai/codex@0.128.0`) in install hints.
- `local-review doctor` no longer probes for "Copilot" in the API fallback section (Copilot CLI support was removed earlier; this was leftover dead state).
- "OpenAI Plus" → "ChatGPT Plus" everywhere user-facing (`OpenAI Plus` is not an actual product).

### Removed
- Outdated planning docs: `docs/implementation-plan-v0.1.md`, `docs/multi-llm-architecture.md`. Both described the original 4-LLM design (including Copilot) and were superseded by `CLAUDE.md`.
- All remaining Copilot references from user-facing docs and code comments (CLAUDE.md, multi.go, CONTRIBUTING.md, OPEN_SOURCE_CHECKLIST.md).

## [0.1.0] - 2026-05-03

### Added
- **Multi-LLM Support**: Run code reviews in parallel with multiple AI models
  - Claude CLI integration (free tier available)
  - Gemini CLI integration (free API key required)
  - Codex CLI integration (ChatGPT Plus required, disabled by default)
- **Multi-LLM Commands**:
  - `local-review multi staged` - Review staged changes with all enabled LLMs
  - `local-review multi commit [ref]` - Review a specific commit
  - `local-review multi branch [base]` - Review current branch against base
  - `local-review doctor` - Check LLM installations and authentication
- **Intelligent Review Merging**: Automatically consolidate findings from multiple LLMs
- **Version Command**: `local-review version` - Print version information
- **Config Command**: `local-review config` - Show resolved configuration (with API key masking)
- **Release Automation**:
  - Auto-release workflow (creates tags on main merges based on PR labels)
  - Homebrew formula auto-update workflow
- **Documentation**:
  - Release process documentation
  - Release setup checklist
  - Multi-LLM architecture documentation
  - Code review guidelines (Google, Microsoft, OWASP 2025 standards)

### Changed
- Enhanced prompt packs with industry best practices:
  - Default pack: 10 → 50+ security patterns (OWASP 2025 aligned)
  - Go pack: Reorganized into 8 categories with 15+ new patterns
  - TypeScript pack: Comprehensive React/Next.js patterns
  - Python pack: Framework-specific patterns (Django, FastAPI, Pandas)
- Improved merge logic with 5-step consolidation process

### Fixed
- Security: API keys now masked in config output
- Path traversal vulnerabilities in review storage
- Timeout handling in git operations

### Removed
- GitHub Copilot support (interactive-only CLI, incompatible with automation)

---

## Version Labeling Convention

When creating PRs, add ONE of these labels to control version bumps:
- `major` - Breaking changes (v2.0.0)
- `minor` - New features, backward compatible (v1.1.0)
- `patch` - Bug fixes, backward compatible (v1.0.1)

If no label is present, defaults to `patch`.
