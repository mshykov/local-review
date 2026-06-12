# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.17.2] - 2026-06-12

**Cleanup patch.** Robustness, accessibility, and tidy-ups from the audit "Later" tier and the SonarCloud board — no new features (those land in v0.18.0).

### Fixed

- **`git.Extract` is now bounded and interruptible (INF-1).** It previously used a plain `exec.Command` — no context (a Ctrl+C couldn't interrupt a wedged git), no deadline, and an unbounded stdout buffer. Now: the runner's signal-trapped context flows through (`exec.CommandContext`) with a generous 2-minute backstop timeout, and a 64 MiB fail-closed size cap rejects a pathological diff with an actionable message instead of ballooning memory or silently truncating.
- **Landing-page contrast (WCAG AA).** `.btn-primary` text and the rule-severity badge were `#667eea` (3.67:1); deepened to `#4f46e5` (6.3:1).

### Internal

- **Accessibility / JS tidy-ups on the landing page:** copy-status live region uses `<output>` over `role="status"`; `dataset` over `setAttribute`/`removeAttribute`; `ta.remove()` over `removeChild`; `globalThis` over `window`.
- **Stale comment fixed (MIN-9):** `audit.resolveTimeout`'s comment conflated bench's 120s with review's actual 600s default; corrected, and the bare `300` literal is now `defaultAuditTimeoutSec`.
- **Drift guard (NIT-3):** new test asserts the `init` wizard reproduces every `config.Defaults()` exclude glob (the cascade merge replaces the list wholesale, so a missed default would silently vanish).
- **Lint cleanups:** deduped the 4× `" env var set"` literal in `doctor.go`; `[[ ]]` over `[ ]` in `scripts/install-hooks.sh`.

## [0.17.1] - 2026-06-11

**Security patch.** Closes a `major` finding surfaced by the tool's own `audit --topic security`, plus a v0.16.0 regression that mislabeled the user's home config as untrusted.

### Security

- **Fixed arbitrary-file disclosure via a symlinked prompt-pack override (`major`).** `internal/prompts` had its *own* `pathInsideDir` that was lexical-only — the symlink-escape hardening that landed in `internal/config` (v0.10.5) was never mirrored here. Since `prompts.pack_dir` is a "non-sensitive" field that merges from an **untrusted** repo `.local-review.yml`, an attacker could commit `pack_dir/<lang>.md` as a symlink to e.g. `/etc/passwd`; the lexical check passed (`rel == "go.md"`) and `os.ReadFile` followed the symlink, leaking arbitrary user-readable files into the LLM prompt. `prompts.pathInsideDir` now resolves symlinks (`EvalSymlinks` + deepest-existing-ancestor walk-up, fail-closed), with an end-to-end regression test proving an escaping symlink override is not read.
- **Defense-in-depth base_url scheme validation.** `provider.Probe` and `llm.Client.Complete` now reject a non-`http(s)` `base_url` (`file://`, `gopher://`, …) before issuing any request — `Complete` is the actual sink the diff is POSTed to, and `--no-preflight` skips the probe.

### Internal

- **Symlink-safe path containment consolidated into one package (`internal/pathsafe`).** The check was duplicated in `internal/config` and `internal/prompts`, and the drift between those copies is exactly what caused the disclosure bug above. Both packages now call `pathsafe.InsideDir` — single source of truth for a security primitive. (Also resolves the SonarCloud "duplication on new code" gate the ported copy would have tripped.)

### Fixed

- **The home config is no longer mislabeled as an untrusted repo layer.** When the project lives under `$HOME` with no project-local config, `FindRepoConfig` walks up and returns the same file as `~/.local-review.yml`; v0.16.0 then re-ran that trusted file through the untrusted pass, printing an alarming "ignoring security-sensitive field(s) … untrusted by default" warning about the user's own `base_url`/`api_key_env`. (The values still worked — the trusted pass set them first — but the warning was wrong and confusing.) `Load` now skips the untrusted pass when the repo-config path is the same file as the home config (`os.SameFile`).

## [0.17.0] - 2026-06-10

**Theme: more providers, and proof the binary works.** Popular OpenAI-compatible providers are now one wizard-pick away, and a new end-to-end suite drives the real binary against a fake LLM on every push — so each release ships with an automated CLI smoke.

### Added

- **Provider presets for Kimi (Moonshot), Groq, OpenRouter, and Qwen (Alibaba DashScope)** in `local-review init`. The provider-agent model already accepted any OpenAI-compatible endpoint via `llms.<name>.base_url`, but these popular targets weren't discoverable in the wizard. Each preset ships an accurate `base_url` + `api_key_env` + a note flagging region / model-id caveats. (`local-review init` now offers OpenAI / Anthropic / Mistral / DeepSeek / Kimi / Groq / OpenRouter / Qwen / Ollama / Other.) Note: "Claude Opus" is not a separate provider — it's a model of the existing claude CLI (`--claude-model` / `llms.claude.model`). README provider lists updated to mention Kimi / Qwen.
- **End-to-end test harness (`e2e/`, behind the `e2e` build tag).** Builds the real binary and drives it (`staged` / `version` / `doctor`) as a subprocess against an in-process fake OpenAI-compatible LLM, asserting CLI output + exit code — no real LLM, no network, fully deterministic. Possible because the provider-agent model treats any OpenAI-compatible endpoint as a real agent, so the whole pipeline (config → detect → probe → review → merge → exit gate) runs for real. Hermetic by construction: an empty `$HOME` + minimal env neutralizes every real CLI agent, and `--only fake` pins the active set. Wired into `ci.yml` (every push/PR, so it also gates the release PR) and documented in [docs/testing.md](docs/testing.md). Covers blocking-review → exit 2, clean-review → exit 0, `version`, and `doctor`. Run locally: `go test -tags e2e ./e2e/...`.

### Fixed

- **Landed the MIN-8 merge-overlay test that missed the v0.16.0 commit.** v0.16.0's CHANGELOG claimed `TestMergeCoversAllExportedFields` exercised `merge()`'s per-field overlay branch, but the test change was left unstaged in that release — `main` still seeded an empty `dst` and only walked the wholesale-copy branch. The intended change (seed `dst` with a same-key entry) is now in.

### Internal

- **`init` tests pick presets by name (`presetMenuChoice`) instead of hardcoded menu numbers**, so adding or reordering providers can't silently break them (CLAUDE.md rule 9).
- **Sonar copy-paste detection excluded for `cmd/local-review/init.go`** — it's a declarative provider-preset table where near-identical struct literals are the intended shape, not duplication debt.

## [0.16.0] - 2026-06-09

**Theme: close the senior-engineer audit's backlog.** A focused sweep of the confirmed findings from the v0.15.1 L6 audit: the untrusted-config security boundary, `audit`-path robustness brought to parity with the `review` path, the `--min-severity` / `--max-findings` flags finally doing what they advertise, plus v0.15 cleanup debt and supply-chain hardening.

### Added

- **`--min-severity` / `--max-findings` now actually filter the `audit` report.** Both flags were advertised on `audit` but inert (the single-LLM path that consumed them was removed in v0.15). `audit` now applies a severity floor (drops findings below the threshold) and/or a total cap (across packages in report order), resolved flag-first then `review.*` config. Hidden findings are disclosed on stderr — never silently dropped (CLAUDE.md rule 4) — and an invalid `--min-severity` is rejected up front. Implemented as a tested, pure `audit.Report.Filtered`. Help text corrected to "audit only" (bench never honored them). (MIN-10)

### Changed

- **`audit` honors Ctrl+C / SIGTERM.** Previously the worker pool kept dispatching every remaining chunk against an already-canceled context, recording each as "errored" and exiting 0 — a "completed" report that silently dropped the tail. The loop now short-circuits on cancellation and the run exits non-zero with an "audit canceled after N/M chunks" message, matching the review path. (MIN-2)
- **`audit` no longer counts an empty LLM response as "clean."** A CLI that exits 0 with empty stdout (rate-limited / capacity-exhausted reply, empty `--output-last-message`) is now recorded as an error rather than folded into `PackagesClean`, which had overstated coverage. (MIN-4)
- **Provider reviews record `mode: "provider"` in `metadata.json`** (was hardcoded `"cli"` for every agent, including HTTP/Ollama/vLLM providers that never spawn a subprocess). (MIN-5)
- **Default `review.min_severity` / `review.max_findings` are now empty / `0`** (no implicit filter). The prior `"warning"` / `20` defaults were inert — nothing read them before v0.16 — and would otherwise have started silently filtering `audit` output.

### Fixed

- **Provider auth-failure error no longer names the removed `LOCAL_REVIEW_API_KEY`.** A provider configured with `llms.<name>.api_key_env: FOO` but no key exported previously errored `export LOCAL_REVIEW_API_KEY=…` — a variable removed in v0.15 that the tool never reads — because the configured env-var name was dropped before the HTTP client was built (`cli.NewInvoker` passed `apiKeyEnv=""`). The name is now threaded through (`cli.LLM.APIKeyEnv` → `ProviderSpec.APIKeyEnv` → `provider.New`), so the error names the variable you actually configured; when none is set it points at `llms.<name>.api_key_env` rather than the dead default.
- **Setting `base_url` on a CLI agent name no longer double-runs it.** `llms.claude.base_url: …` with the claude CLI installed previously produced both a CLI `claude` and a provider `claude` in the roster — two same-named agents that both ran and collided in the name-keyed ready/merge maps. `pickAgents` now drops the CLI twin so the provider entry wins (`dropCLITwins`).
- **Mid-review Ctrl+C now reports cancellation, not "all N LLM reviews failed."** The fan-out re-checks `ctx.Err()` after the result drain, so a user interrupt during the long review phase surfaces as cancellation instead of a fabricated "all agents failed" diagnosis. (MIN-3)
- **`init` wizard and `examples/.local-review.yml` stop defaulting `api_key_env` to the removed `LOCAL_REVIEW_API_KEY`** — generated configs now use a neutral `YOUR_PROVIDER_API_KEY` / `OPENAI_API_KEY` placeholder. (NIT-4)

### Security

- **The repo-level `.local-review.yml` is now an untrusted config layer.** Because a `.local-review.yml` ships inside code you may be reviewing for the first time (a CI runner checking out a hostile commit, a fresh clone), the security-sensitive LLM fields it could carry are no longer honored from the repo layer by default: `cli_path` (the runner feeds it to `exec.LookPath` + `exec.CommandContext` → arbitrary binary execution), `base_url` (registers a provider agent that POSTs your diff — or, under `audit`, the whole tracked source tree — to that endpoint → silent exfiltration), `api_key` (a credential committed into a repo), and `api_key_env` (redirects which env var is read as a credential). Each is stripped from the repo layer with a stderr warning naming what was dropped; non-sensitive fields (`prompts`, `review`, model/timeout/enabled) still apply. The user-home `~/.local-review.yml` is unaffected (it isn't writable by the code under review). A team that genuinely trusts a checked-in config (e.g. a standardised LAN Ollama `base_url`) opts back in with `LOCAL_REVIEW_TRUST_REPO_CONFIG=1`. This is the same trust boundary already drawn for `prompts.pack_dir`, extended to the execution / network / secret fields. (`config.sanitizeUntrustedLayer`; closes the two `major` findings from the v0.15.1 senior-engineer audit.)
- **Homebrew formula SHAs are cross-verified against the release checksums manifest.** The `update-homebrew` job now downloads the release's own `checksums.txt` and verifies each tarball against it before recording the hash in the formula — making the build-time manifest the single integrity root for both the `install.sh` and Homebrew channels (previously the formula recorded the hash of whatever bytes the URL served at homebrew-update time). (MIN-12)
- **`install.sh` fails closed when no `sha256sum` / `shasum` verifier is present.** Previously it warned and installed anyway; now it requires an explicit `INSTALL_REVIEW_SKIP_VERIFICATION=1` or an interactive acknowledgement, matching the already-hardened manifest-missing branch. Closes the silent-unverified-install gap on minimal container / CI images. (MIN-13)

### Internal

- **Review exit-gate decision extracted into a pure, tested helper (`decideExitGate`).** The security-critical ordering — the per-LLM blocking scan must run even when the merged report is empty, so a merger timeout/rate-limit can't collapse an exit-2 (blocked) into an exit-1 (which pre-commit hooks let through) — was previously only testable through the full git/probe/orchestrator path. New `TestDecideExitGate` table-tests all four cases directly.
- **Removed dead code and never-written schema fields.** Deleted the unused exported `CountSuccessful` / `GetSuccessful` (Error==nil remnants of the `CountWithOutput` migration) and the three `metadata.json` findings-count fields (`findings_count`, `final_findings_count`, `deduplication_removed`) that the markdown-only review path never populated. (MIN-6, MIN-7)
- **Stale `Invoker.Review` docs** no longer describe the removed single-LLM JSON path as a live mode (`internal/agents`). (NIT-1)
- **Config merge-coverage test** now seeds `dst` with the same key so it exercises `merge()`'s per-field overlay branch — the path where a dropped field silently no-ops in production — instead of only the wholesale-copy branch. (MIN-8)
- **Audit-path test coverage:** new tests for cancellation, empty-output handling, parser-miss-as-clean, the severity/cap filter, and provider-vs-cli mode.

## [0.15.1] - 2026-05-29

**Theme: audit performance.** Real-world dogfood on a 37-chunk monorepo via Ollama qwen2.5-coder:7b ran 22 min — a clear cost of audit's sequential per-chunk dispatch. v0.15.1 ships two related fixes that together can take that same run to ~5 min with no quality loss.

### Added

- **`audit --parallel <N>` flag.** Caps concurrent per-chunk LLM calls. Default 1 (sequential — preserves the v0.10-v0.15.0 behaviour). Set `>1` against backends that serve concurrent requests (local Ollama with `OLLAMA_NUM_PARALLEL=4`, vLLM, self-hosted endpoints) to fan out N chunks at a time — wallclock drops roughly N×. Cloud LLMs (claude/codex/copilot) typically rate-limit per-tier; keep at 1 there to avoid 429s. New `Options.Parallelism` field in `internal/audit`; matching `Options.Invoker` test seam (production callers leave nil) so the concurrency contract (peak-in-flight, order-preserved results) is verifiable without real LLM plumbing.
- **New test `TestRun_ParallelDispatch_ReducesWallclock`** pins the runtime shape: with Parallelism=4 and 8 chunks at 100ms each, total wallclock is ≤500ms (vs ≥700ms sequential) AND peak-in-flight hits exactly 4, AND result order matches chunk order regardless of completion order. Catches both the "concurrent dispatch happened" and "completion-order leaks into final report" failure modes.

### Fixed

- **`pnpm-lock.yaml` (and other lockfiles ending in `.yaml`) were being audited.** The walker's `isAuditable` check evaluated the extension allowlist (which returned `true` for `.yaml` because of CI workflows / k8s manifests) BEFORE the lockfile base-name skip. A 272 KiB `pnpm-lock.yaml` chunk was burning ~5 min on Ollama per real-world v0.15 dogfood. Reordered so the lockfile + minified-bundle skip set wins absolutely. Skip set expanded: added `bun.lockb`, `mix.lock`, `pubspec.lock`, `flake.lock`, `Pipfile.lock`, `npm-shrinkwrap.json`, and the `*.min.js` / `*.min.css` / `*.min.map` suffix family. New regression test `TestWalker_IsAuditable_LockfilesAndMinified_v0151` covers each entry + sanity-checks that legitimate `.yaml` files (CI workflows, k8s manifests, compose files) still get audited.

## [0.15.0] - 2026-05-29

**Theme: the unified-agent v2.** v0.14 introduced the unified `llms.<name>.base_url` shape and deprecated the v0 top-level `provider:` block. v0.15 is the breaking removal — `provider:` is gone, the single-LLM-fallback code path is gone, the `--model` / `--base-url` flags are gone. Loading a stale v0.14 config now surfaces a copy-pastable migration error from the cascade loader, not a silent drop.

Alongside the cleanup, v0.15 lands the **Gemini CLI sunset hardening**: Google announced the CLI stops serving Pro / Ultra / free-tier requests on **2026-06-18** (now ~20 days out). `doctor` shows a live countdown banner; the multi-agent runner auto-drops gemini from the default fan-out as soon as the cutoff passes; an opt-out (`llms.gemini.force_after_sunset: true`) lets users keep trying past the cutoff in case Google extends or their tier survives.

This shipped across four PRs (#110 / #111 / #112 / #113):

- **PR 1** removed the `provider:` block + single-LLM fallback (-767 lines).
- **PR 2** added the gemini sunset + force_after_sunset opt-out.
- **PR 3** flipped `docs/index.html` + `audit/README.md` over to v0.15 messaging.
- **PR 3.5** (QA-found) fixed two real defects in the sunset machinery: a name-based gate that would auto-drop user-named provider entries called `gemini`, and a doctor `X/Y ready` display where numerator and denominator could disagree near the cutoff boundary.

### Removed (BREAKING)

- **Top-level `provider:` config block** (and the v0 single-LLM fallback code path it drove). Deprecated in v0.14, scheduled for removal in v0.15 — this is that removal. A `.local-review.yml` that still carries a `provider:` key now surfaces an actionable migration error at load time pointing at the unified `llms.<name>.base_url` shape (verbatim field rename — `provider.base_url` → `llms.<name>.base_url`, same for `model` / `api_key_env` / `timeout_seconds`). Internals dropped alongside: `config.Provider` struct + field, `resolveAPIKey`, `warnDeprecatedProviderBlock`, `shouldWarnDeprecatedProvider`, the `internal/review` single-LLM orchestration (`Reviewer.Run`, `parseFindings`, `topLevelJSONObjects`, `applyFilters`, `buildUserMessage`), and the `runSingleLLMFallback` runner branch.
- **`--model` and `--base-url` CLI flags** went with the same removal — they only overlaid `cfg.Provider`. Per-agent equivalents (`--claude-model`, `--codex-model`, …) and the config-time `llms.<name>.model` field cover the same use case under the unified shape.
- **Legacy example configs**: `examples/.local-review.mistral.yml`, `examples/.local-review.deepseek.yml`, `examples/.local-review-multi.yml` deleted. The previous `examples/.local-review.unified.yml` is promoted to the canonical `examples/.local-review.yml`.

### Fixed (pre-release QA)

- **Sunset auto-disable now only matches CLI agents.** The PR 2 gate matched on `llm.Name` alone, which would have auto-dropped a user-named provider entry like `llms.gemini: { base_url: http://my-self-hosted-llm/v1 }` post-2026-06-18 — even though Google's sunset only applies to the Gemini CLI subprocess. The predicate now also requires `llm.BaseURL == ""` (CLI only); provider agents short-circuit out regardless of name. Caught by the pre-release multi-agent review (codex). New regression test `TestSelectAgents_PostSunsetDoesNotAffectProviderNamedGemini`.
- **`doctor`'s `X/Y LLMs ready` denominator is now consistent with the runtime fan-out.** Pre-fix `readyCount` (numerator) excluded sunset-gated gemini but `reviewCapable` (denominator) still counted it, so output read "3/4 ready" when the 4th was about to be auto-dropped. Both now apply the same gate. `time.Now()` is also hoisted to function scope so per-agent rows and the readiness footer always see the same instant — near the 2026-06-18 boundary the previous shape could produce a row reading "sunset" while the denominator still counted the agent.

### Documentation

- **Website + audit/README polish** (PR 3 of the v0.15 series). `docs/index.html` dropped the `provider.base_url` "single-LLM fallback" phrasing (removed in v0.15) and the Gemini agent card now surfaces the v0.15+ sunset auto-disable + `force_after_sunset` override. `audit/README.md` reworded the methodology paragraph to say reports are refreshed at each release that materially changes the code surface (v0.10.0, v0.14.0, v0.15.0); the actual run timestamp is on each report's first line. The committed `audit/security.md` + `audit/tech-debt.md` files still date from 2026-05-25 (pre-v0.15) — a refresh via claude is on the v0.15.1 / v0.16 backlog, deferred from this release after two consecutive long-run kills mid-audit.

### Changed

- **Gemini CLI auto-disables on/after 2026-06-18** (PR 2 of the v0.15 series). Google's announced sunset for the Gemini CLI is hard-coded as `cli.GeminiSunsetDate`; once `time.Now().UTC()` is at or past that instant, the runner drops gemini from the multi-agent fan-out by default (the configured-disabled return list calls it out so the no-active-agents path can still hint the override). `local-review doctor` switches from a static deprecation notice to a clock-aware banner with three modes: pre-sunset countdown ("N days until Gemini CLI sunset on 2026-06-18..."), post-sunset auto-disabled ("auto-disabled in the review fan-out; ..."), or post-sunset force-overridden ("force_after_sunset is set — running anyway..."). Users who want to keep trying past the cutoff (in case Google extends, or in case their network sees a different rollout) can set `llms.gemini.force_after_sunset: true` to opt back in. `--only gemini` after the cutoff also runs — explicit user intent wins, with a stderr warning about expected failures. Tests use an injectable `now time.Time` so the three branches are verifiable without waiting for the wall clock to cross the cutoff.
- **`local-review init` wizard now emits the unified shape** — output is `llms.<presetName>:` (e.g. `llms.openai:`, `llms.ollama:`) instead of the v0 top-level `provider:` block. The preset name is free-form; the comment above the entry tells users they can rename it to anything that reads well for their team.
- **`internal/review` package slimmed** to just the diff-filter machinery (`FilterDiffs` + the glob helpers the multi-LLM runner shares with the audit walker). The structured `Severity` / `Finding` / `Report` types stay (used by `internal/output`, `internal/multi`, audit), but the LLM orchestration is gone.

## [0.14.1] - 2026-05-28

### Fixed

- **`provider:` deprecation warning fired on every invocation, even with no user config.** `Defaults()` pre-populates `Provider.BaseURL` / `Model` / `APIKeyEnv`, so post-cascade `cfg.Provider.BaseURL` was never empty — the v0.14.0 `shouldWarnDeprecatedProvider` predicate fell straight through to the "warn" branch on `doctor` / `config` / `review` / `audit` whether the user had a legacy `provider:` block or not. Now the predicate also suppresses when the resolved Provider equals `Defaults()` verbatim (no-config or tautological-config case); a user-configured non-default `provider.base_url` (e.g. Ollama at `http://localhost:11434/v1`) still triggers the migration nudge. Two new tests in `internal/config/config_test.go` pin both directions.
- **`local-review config` leaked credentials embedded in `base_url` values.** The `api_key:` field was masked, but a basic-auth userinfo URL (`https://user:pass@host/v1`) or a query-string key (`?api_key=sk-…`) printed verbatim — a pre-existing bug, not a v0.14 regression. The config printer now runs every `base_url` (top-level `provider.base_url` AND each `llms.<name>.base_url`) through the same `SanitizeBaseURLForDisplay` helper introduced for the v0.14.0 deprecation warning. Scheme + host + path survive; userinfo, query, and fragment are dropped. The helper moves from unexported to exported in `internal/config` so both surfaces share one implementation. Five new tests in `cmd/local-review/config_test.go` pin basic-auth masking, query-string masking, and plain-URL roundtrip for both the `provider:` block and the `llms.<name>` entries.

## [0.14.0] - 2026-05-28

**Theme: unified agent model.**

Provider endpoints (Ollama / vLLM / any OpenAI-compatible — OpenAI, Anthropic, Mistral, DeepSeek, Together, Groq, OpenRouter) are now first-class agents alongside the CLI subprocess invokers (claude / codex / gemini / copilot). One config drives both, one fan-out runs both, one report consolidates both. The old top-level `provider:` block stays working for one release with a stderr deprecation warning; new configs should use the unified `llms.<your-name>.base_url` shape.

This shipped across five PRs (#104 / #105 / #106 / #107 / this) — each landed independently to keep the blast radius small. The series-level themes:

- **PR 1** carved out an `internal/agents.Invoker` contract that both CLI subprocess invokers and the new HTTP `provider.Invoker` implement. Foundation only; no user-visible change.
- **PR 2** added `cli.DetectProviders` + the `base_url:` field on `llms.<name>` config entries, and wired the new agent kind into `doctor` so providers appear in the readiness block side-by-side with CLIs.
- **PR 3** made the pre-flight readiness probe kind-aware (cheap `GET /v1/models` for providers by default; `--strict-probe` forces a full chat completion) and wired the unified fan-out through `local-review review` end-to-end.
- **PR 4** added `--with <agent>` to `audit` so a single-LLM audit can pin to any CLI or provider name.
- **PR 5** (this) flips the docs over, deprecates the legacy `provider:` block at config-load time with a printable migration shape, and stamps the release.

### Added

- **Unified agent model is now the recommended shape.** Any entry under `llms:` with a `base_url:` becomes a **provider agent** — see [`examples/.local-review.unified.yml`](examples/.local-review.unified.yml) for a fully-commented config. The entry name is free-form (`ollama`, `qwen`, `local-fast`, `cloud-openai`, …) and surfaces in `doctor`, `--only`, `--with`, and the on-disk review filename.
- **`audit --with <agent>` flag (PR 4 of the unified-agent series).** Pin a single audit run to a specific agent — CLI (`--with claude`) or provider (`--with qwen`, any free-form name from `cfg.llms` with a `base_url:` set). Exact-match on the agent name; fails closed with the live "ready" candidate list when the name isn't authenticated, so a typo is fixable from the error alone (no `doctor` round-trip needed). Composes with `--only`: `--only` is the upstream allow-list, `--with` picks one entry within it. Default behaviour (no `--with`) is unchanged — first authenticated agent wins, claude-preferred. Audit stays single-LLM in v1; this flag closes the "I have Ollama set up but audit always picks claude" gap from the unified-agent dogfood. Provider agents already flow through `cli.NewInvoker` → `provider.Invoker` (PR 1), so no audit-internal plumbing changed.
- **`internal/agents/provider.Invoker`** — HTTP-backed implementation of `agents.Invoker`, wraps `llm.Client`. Sends `system` + `user` messages, never sets `response_format` (the prompt drives output format, not the wire header), maps token usage from the response. Folds `total_tokens`-only responses into `InputTokens` with `TotalOnly=true` so partial OpenAI-compat providers (some Ollama builds) still show a count. Compile-time `var _ agents.Invoker = (*provider.Invoker)(nil)` assertion + 6 unit tests against an `httptest` server (wire shape, usage mapping, total-only folding, missing-usage degrades to zero, error attribution by agent name, output trimming).
- **Light provider pre-flight + `--strict-probe` flag (PR 3 of the unified-agent series).** Provider agents had been getting the same `Reply OK` chat-completion treatment as CLI agents (via the unified `Invoker.RunPrompt` contract from PR 2) — which works, but is overkill for "is the endpoint up?" and incurs a model load on Ollama. `ProbeAll` now dispatches on kind: providers default to a cheap `GET /v1/models` HTTP probe (no model load, no tokens, sub-second on a healthy box); the new `--strict-probe` flag forces them back through the full `Reply OK` chat completion when the configured model id specifically must be loaded. CLI agents are unaffected — they have no lighter alternative. The classifier uses `errors.Is` (not raw `ctx.Err()`) so wrapped `DeadlineExceeded` errors from `provider.Probe`'s inner timeout still map to `ProbeTimeout` (not a generic `ProbeError` glyph). The HTTP-probe seam in `ProbeAllWithInvokerFactory` is a per-call function parameter (not a package global), so tests can run in parallel without reassignment races. 4 new tests cover the dispatch + the wrapped-error case.

- **Provider entries detected + surfaced in `doctor` (PR 2 of the unified-agent series).** A new `base_url:` field on `llms.<name>` config entries turns that entry into a provider agent (Ollama, vLLM, any OpenAI-compat endpoint). `cli.DetectProviders` HTTP-probes each entry's `/v1/models` in parallel (3s cap per endpoint) and `doctor` renders an endpoint-shaped row (`✓ qwen provider ready` / `✗ qwen provider unreachable` with a diagnostic note). The readiness denominator counts providers too (`4/6 LLMs ready for multi-review`). `cli.NewInvoker` dispatches BaseURL-set entries to `provider.Invoker`, and `pickAgents` appends detected providers to the active-agent set alongside CLIs. The pre-flight readiness probe used the same `Reply OK` contract for providers (correct but heavier than needed); PR 3 below makes that pre-flight kind-aware and adds a `--strict-probe` flag.

### Deprecated

- **Top-level `provider:` config block.** Superseded by the unified `llms.<name>.base_url` shape — same fields (`base_url`, `model`, `api_key_env`, `timeout_seconds`), just nested under a `llms:` entry name you pick. Loading a config with `provider:` still works, but now emits a one-time stderr warning at config load with a copy-pastable migration snippet (the warning suppresses itself if any `llms.*` entry already carries a `base_url`, so users mid-migration don't see noise). The legacy single-LLM fallback code path stays for v0.14; full removal is on the v0.15 milestone.

### Refactored

- **`agents.Invoker` contract moved to its own package (PR 1 of the unified-agent series).** The review-agent interface and `TokenUsage` shape, previously in `internal/cli`, now live in `internal/agents` so both the CLI-subprocess invokers (`internal/cli`) and the new HTTP-provider invoker (`internal/agents/provider`) can implement the same contract without depending on each other. `cli.Invoker` and `cli.TokenUsage` are now type aliases to the new home — all existing callers keep working unchanged. No user-visible behavior change in this PR; foundation only.
- **`llm.Client.Complete` now returns `(text, Usage, error)`.** The HTTP client surfaces the provider's `usage` object so `provider.Invoker` (below) can map it to `agents.TokenUsage` for per-call token counts. Single caller (`internal/review`) updated; usage discarded there for now (a later PR threads it into the Report meta).

## [0.13.1] - 2026-05-27

**Theme: workflow guardrails + the local/cloud two-pass flow.**

A maintainer-facing patch: secret/PII commit guardrails (born from the v0.13.0 IP-leak incident) and a fix that lets a single config drive both a free local-Ollama pass and a cloud-LLM pass.

### Added

- **Secret + personal-data guardrails (local pre-commit + CI).** A new `gitleaks`-based secret scan runs in CI (`.github/workflows/secret-scan.yml`, over full git history) and as a local pre-commit hook (`scripts/install-hooks.sh`). The hook also greps the staged *content* of changed files (not the raw diff, so a commit that *removes* a leaked value isn't blocked) against a **gitignored** `.git-personal-denylist` (seeded from `.git-personal-denylist.example`) so you can block your own IPs / hostnames / emails / names from ever being committed — those stay on your machine since CI can't hold them. `.gitleaks.toml` allowlists the repo's obviously-fake test placeholders so real secrets are still caught everywhere. Motivation: a maintainer's real Tailscale IP was committed into a test fixture in v0.13.0 — this is the backstop so it can't recur silently. (Secrets are reliably auto-detected; personal IPs/names rely on the denylist + the new CLAUDE.md rule 13, since a generic rule can't tell a real personal IP from the many legitimate example IPs in the test suite.)

### Fixed

- **`--only` now works with an all-disabled-LLM config.** `--only claude,codex,…` is an explicit allow-list that overrides config-level `enabled: false` for agent *selection*, but `cfg.Validate()` still hard-rejected an all-disabled config in the multi-LLM path (*"all LLMs are explicitly disabled"*), so the override didn't work end-to-end. Now `Validate` returns a sentinel `config.ErrAllLLMsDisabled` and the runner tolerates it specifically when `--only` is set. This enables a clean two-pass workflow from a **single** config (disable all LLMs + point `provider` at a local Ollama → `local-review review` runs Ollama; `local-review review --only claude,codex,copilot` runs those cloud agents). Every other config-validation failure (e.g. a bad `merge.preferred_llm`) still aborts. Surfaced by dogfooding the Ollama-then-cloud workflow.

## [0.13.0] - 2026-05-27

**Theme: make the local/offline path actually work.**

Two bugs the cloud multi-LLM path had hidden, both surfaced by pointing the single-LLM fallback at an Ollama running over Tailscale: tailnet IPs weren't recognised as local (so they demanded a key Ollama never wanted), and the language packs never actually shipped the JSON schema in single-LLM mode (so weak local models returned unparseable output). Strong cloud models had been papering over the second one by inferring the schema.

### Fixed

- **Ollama over Tailscale now works without a dummy `api_key`.** `isLocalURL` only treated RFC1918 + IPv6 ULA/link-local as local, but Tailscale assigns IPs from `100.64.0.0/10` (RFC 6598 CGNAT), which Go's `IsPrivate()` doesn't cover. Pointing `provider.base_url` at a Tailscale Ollama (`http://100.x.x.x:11434/v1`) hard-errored "no API key" despite Ollama needing none. Added a `100.64.0.0/10` check; the corporate-gateway invariant is preserved (the bypass still only fires when no key is configured). Surfaced by an Ollama-over-Tailscale dogfood.
- **Single-LLM reviews of non-default languages now send the JSON output schema.** Every language pack (go/python/typescript/rust/swift/kotlin/liquid) ended with "Same JSON shape as the default pack", but `prompts.Resolve` only ever sent ONE pack — so the schema (which lived only in `default.md`) never reached the model. Strong cloud models inferred `{"findings": […]}` anyway; weaker local models (e.g. Ollama `llama3`) returned JSON without the `findings` key and the review failed with "no JSON object with a `findings` key found in response". The schema is now centralised in `prompts.FindingsJSONSchema` and appended at resolve time when the caller parses JSON (single-LLM path), so every language carries the contract. The multi-LLM path is unchanged — it appends its own markdown-output override, so the JSON schema is deliberately NOT injected there.

## [0.12.0] - 2026-05-27

**Theme: a new reviewer that actually works.**

GitHub's Copilot CLI joins the fan-out as a first-class reviewer — the first agent added since the Antigravity evaluation, and the one that passed the dogfood where `agy` didn't. Where `agy --print` runs an autonomous agent loop and emits narration, `copilot -p` returns a clean review on stdout and reviews the diff it's handed. The integration was hardened by Copilot reviewing its own code plus a multi-LLM self-review: tools are disabled to close a prompt-injection vector, generic `GH_TOKEN`/`GITHUB_TOKEN` no longer auto-enable a paid reviewer, and `merge.preferred_llm: copilot` validates out of the box.

### Added

- **GitHub Copilot CLI (`copilot`) as a live reviewer.** Copilot joins the parallel fan-out as a first-class agent alongside claude / gemini / codex (`cli.IsReviewCapable("copilot") == true`). A 2026-05 authenticated dogfood confirmed `copilot -p` returns a clean review on stdout (agent/usage telemetry stays on stderr) and reviews the diff it's handed rather than reconstructing its own — the opposite of antigravity, which is why Copilot ships live and `agy` stays experimental. `local-review doctor` shows it; auth is `copilot login` or a `COPILOT_GITHUB_TOKEN` (a bare `GH_TOKEN` / `GITHUB_TOKEN` works for the `copilot` CLI but won't auto-enable this paid reviewer); `--copilot-model` overrides the model. A default `llms.copilot` config entry ships so `merge.preferred_llm: copilot` validates out of the box. Token counts are parsed best-effort from Copilot's stderr usage summary. Each run consumes one Copilot **Premium request** (not BYOK-free).
  - **Security:** Copilot is invoked tools-disabled (`--available-tools=`). The review prompt embeds an attacker-controllable diff, and `--allow-all-tools` would let a prompt-injecting diff drive Copilot's shell/write/url tools to mutate the workspace. A diff review needs no tools, so disabling them removes the vector while keeping non-interactive mode hang-free (`--no-ask-user` also stops it blocking on a question).

## [0.11.0] - 2026-05-26

**Theme: prepare for the Gemini-CLI sunset.**

Google retires the Gemini CLI (and Gemini Code Assist IDE extensions) for Pro/Ultra/free-tier requests on **2026-06-18**, with Antigravity (`agy`) as the successor. v0.11.0 detects `agy`, deprecates Gemini with an in-tool migration notice, and bundles the two unreleased v0.10.x probe-UX improvements (`--preflight-timeout` + the bare-timeout hint).

The honest headline: `agy` is **detected but not yet a reviewer**. The authenticated dogfood showed its headless `--print` mode runs a full autonomous agent loop rather than answering with a review, so it's gated out of the fan-out until a structured-output invocation contract exists.

### Added

- **Antigravity CLI (`agy`) detection — Google's Gemini-CLI successor (experimental).** `local-review doctor` now detects `agy` and shows it as a `◐ experimental` row. It is **deliberately excluded from the review fan-out** (`cli.IsReviewCapable("antigravity") == false`): an authenticated dogfood showed agy's headless `--print` mode runs a full autonomous agent loop — it explores the repo, reconstructs its own diff instead of using the one it's handed, and streams tool-step narration rather than returning a clean review (`review --only antigravity` produced 6.5 KB of narration, zero findings, and an empty merged report). The `AntigravityInvoker` ships as scaffolding for a future structured-output integration; `--only antigravity` refuses cleanly with an explicit "detected but excluded" note rather than running the broken path. Motivation: Google sunsets the Gemini CLI on **2026-06-18**, so detecting the successor early lets us iterate toward a working integration before the cutoff.

- **`--preflight-timeout <duration>` flag.** First-customer dogfood on the v0.10.6 build showed `claude ✗ timeout after 10s` with no vendor diagnostic — claude-code's cold-start on a loaded host can exceed the 10s probe cap before the CLI writes anything to stderr. Bumping with `--preflight-timeout 20s` rescues those runs without forcing `--no-preflight` (which loses the readiness signal entirely). Default unchanged (10s); the flag exists as the "cold-start escape hatch" — `--no-preflight` is still the "CI / scripting" escape hatch.

### Changed

- **Bare-timeout readiness line now includes a "no diagnostic captured" hint.** Pre-fix `claude ✗ timeout after 10s` read identically to a vendor-message-present timeout, leaving users unable to tell silent-claude apart from gemini-with-vendor-message. The readiness block now appends `(no diagnostic captured — run \`local-review doctor\`, or raise --preflight-timeout)` to bare-timeout lines, pointing at the two most common fixes. Vendor-message-present timeouts (the v0.10.6 path) are unchanged — they already surface the vendor's actual text. Also the rendered timeout now reflects the configured `--preflight-timeout` value instead of always saying "10s"; pinned by `TestFormatProbeLine/timeout_renders_configured_timeout_not_default`.

### Deprecated

- **Gemini CLI — stops serving 2026-06-18.** Google is retiring the Gemini CLI (and Gemini Code Assist IDE extensions) for Pro/Ultra/free-tier requests. `local-review doctor` now prints a migration notice on every gemini row pointing at Antigravity. Gemini keeps working until the cutoff; it will be removed in a later release. (Note: the migration target, `agy`, is detected but not yet a working reviewer — see Added.)

## [0.10.7] - 2026-05-26

**Theme: catch the docs up to the code.**

Six v0.10.x feature releases shipped between v0.10.0 and v0.10.6 — audit subcommand, pre-flight readiness probe (timeout fix + vendor-error surfacing + ProbeCanceled distinction), Swift / Kotlin / Liquid prompt packs, SWE-bench-lite mode, Ollama-on-LAN auth bypass, install.sh TTY prompt. The user-facing docs (README, website, audit/bench READMEs, CLAUDE.md) were last refreshed for v0.9 and progressively fell out of sync. v0.10.7 catches them up across all five surfaces in one focused docs-only release.

No code changes — `internal/` and `cmd/` are untouched. The release tag exists for cadence continuity (the website's footer links a current version; bumping the tag keeps the footer accurate).

Three Copilot findings + four CodeRabbit findings were resolved in-PR before merge — accuracy claims (`--json` scope, multi-LLM-ignored paragraph placement, SWE-bench credibility-gap overstatement), discoverability (missing `--swe-bench` flags in bench's flag table), accessibility (WCAG contrast on the new Audit feature card), and a trailing-space typo. Same multi-reviewer pattern that drove the v0.10.x code releases.

### Documentation

- **`README.md`** rewritten for v0.10.x: replaced the v0.9 "What's new" callout with a v0.10.x summary covering six themes; added a pre-flight probe step to the "Multi-LLM is the default" how-it-works list plus a full expected-output code block showing the readiness block + per-LLM lines + merged-report tail; new "Audit — whole-codebase deep analysis" section between Multi-LLM and Configure with `--dry-run` cost-preview, `--out` flag, and methodology pointer; CLI section adds `local-review audit` to the command list, a new audit-specific flag table, and `--no-preflight` in the common-flags table.
- **`docs/index.html`**: Multi-LLM feature card mentions the pre-flight probe; new "Whole-Codebase Audit" feature card with the right WCAG-compliant code styling; 4th Quick Start step for `local-review audit --topic security --dry-run`.
- **`audit/README.md`**: "Regenerating" section spells out the patch-vs-minor cadence that emerged from v0.10.x sessions (patch releases that only refactor existing review-path code don't regen; minor or audit-walker-touching releases do). New "Pre-flight probe note" explains the probe's behaviour inside audit + the `doctor` escape hatch.
- **`bench/README.md`**: new "SWE-bench-lite catch-rate mode (v0.10+)" section — explains the credibility-loop motivation, the v0.10.0 current state (3 synthetic shipped examples vs. real-task curation as deferred follow-up), and the binary-scoring rationale including why error frames count toward the denominator. Adds `--swe-bench` and `--swe-bench-dataset` rows to the Useful flags table.
- **`CLAUDE.md`** "Multi-LLM model — non-obvious facts" extended with 6 new bullets covering pre-flight probe semantics, partial-stderr capture with `Unwrap`-chain preservation, `ProbeCanceled` vs `ProbeTimeout`, audit's invoker path, `isLocalURL` widening with the IPv6 zone-suffix detail, `pathInsideDir` symlink resolution with the missing-leaf bypass.

## [0.10.6] - 2026-05-26

**Theme: tell the user why.**

The pre-flight probe shipped in v0.10.1, learned to actually honor its 10s wallclock in v0.10.5, and in v0.10.6 finally tells the user **why** an LLM timed out. Pre-v0.10.6 a hung gemini rendered as the unhelpful `gemini ✗ timeout after 10s`; v0.10.6 reads the vendor's stderr live and shows the actual cause inline: `gemini ✗ timeout after 10s — Error: You have exhausted your capacity on this model.` No `doctor` chase, no log-digging — the diagnostic lands in the readiness block at the moment the probe gives up.

How: a small 4-KiB ring buffer (`stderrCapture` in `internal/cli/stderr_capture.go`) is teed onto every invoker's subprocess stderr via `io.MultiWriter` so the bytes accumulate live as the CLI writes. When `ctx.Done()` fires before the subprocess does, the probe snapshots the buffer and surfaces whatever the CLI complained about before it hung. Empty captures fall back cleanly to the v0.10.5 generic message. The `context.DeadlineExceeded` unwrap chain is preserved via a custom `probeTimeoutErr` type — codex caught the dropped chain in this PR's own dogfood. Sonar flagged duplication on the first build's per-invoker copy-paste — that got refactored into a shared embedded `partialStockerField` (auto-method promotion) + a `teeStderr` helper, also caught in-PR before merge. Live-verified on the PR's dogfood: the readiness block now reads the way it should.

Note the session's review pattern at this scale: three reviewers + one external GitHub incident, all caught and routed around inside the same PR. The PR ships green.

### Added

- **Pre-flight probe timeout now surfaces the vendor's actual error message instead of generic "timeout after 10s".** When a CLI hangs past the 10s probe deadline, v0.10.5 rendered the readiness line as `gemini ✗ timeout after 10s` — accurate but unhelpful: the user still had to run `doctor` or read CLI logs to find out *why* (exhausted capacity? auth failed? network?). v0.10.6 adds **live partial-stderr capture** via a new `stderrCapture` ring buffer (capped at 4 KiB, goroutine-safe, first-bytes-wins because vendors print the diagnostic line BEFORE hanging on the network call) tied to each invoker via `io.MultiWriter`. The probe layer peeks the buffer when ctx expires via a new optional `cli.PartialStderrCapturer` interface; Claude / Gemini / Codex invokers all implement it. Readiness block now renders: `gemini ✗ timeout after 10s — Error: You have exhausted your capacity on this model.` Empty / whitespace-only partial-stderr falls back to the generic "timeout after Ns" message (so we never produce `gemini ✗ timeout after 10s — ` with trailing dash). Invokers that don't implement the optional interface fall through to the same generic message — additive change, no breakage. Five new tests pin the invariants: concurrent Write/Snapshot under `-race`, cap discards excess bytes correctly, empty Snapshot for unused buffer, zero-cap-falls-back-to-4-KiB-default, and the probe-layer "vendor message surfaces" case + two defensive fallbacks (no-interface, whitespace-only). 240-char cap on the rendered text (with `…` ellipsis past the cut) so a misbehaving CLI dumping a stack trace doesn't blow up the readiness column.

## [0.10.5] - 2026-05-25

**Theme: close the gaps the audit kept flagging.**

A focused patch release closing three findings the dogfood audit has been re-flagging since v0.10.0:

1. **`prompts.pack_dir` symlink-bypass** (#89, `Security`): v0.10.0's `pathInsideDir` was lexical-only — a symlink inside the config dir pointing outside it would slip through. The defence-in-depth gap was caught by every audit run since v0.10.0; v0.10.5 adds `EvalSymlinks` + deepest-existing-ancestor walk-up + fail-closed posture on resolve errors. Three dogfood round-trips of in-diff catches (codex found the missing-leaf bypass, then the fail-open on baseDir error, then a stale doc comment) — all resolved before merge.

2. **Pre-flight probe wallclock honors the 10s cap on hung CLIs** (#88, `Fixed`): the v0.10.1 feature that was supposed to collapse the 4-minute gemini hang to a 10s `✓`/`✗` signal was *itself* failing on the target case — `cmd.Wait()` blocked on pipe drainage past `exec.CommandContext`'s SIGKILL, so the probe phase wallclock matched the CLI's subprocess-death time, not the per-LLM cap. v0.10.5 races `inv.RunPrompt` against `ctx.Done()`. Live verification on the dogfood: probe phase = **10s exactly** (down from 4m34s on PR #86's dogfood).

3. **`ProbeCanceled` distinct from `ProbeTimeout`** (#88, `Added`): caught by codex on PR #88's own first dogfood. The race-fix above introduced a `case <-ctx.Done()` branch that returned `ProbeTimeout` unconditionally — conflating real timeouts with user Ctrl+C. v0.10.5 adds a fourth `ProbeStatus` enum variant, branches on `ctx.Err()` to distinguish, propagates `context.Canceled` through the runner's signal-handler exit path, and renders canceled probes with a distinct `⊘` glyph.

The session pattern that emerged: every release since v0.10.0 has been driven by the previous release's audit (v0.10.1, v0.10.2, v0.10.4, v0.10.5) or by the dogfood of the PR itself surfacing in-diff catches (every PR since v0.10.3). Three reviewers (3-LLM diff review + Copilot + CodeRabbit) each catch findings the others miss; the redundancy IS the trust signal.

### Security

- **`pathInsideDir` now resolves symlinks before the containment check.** v0.10.0's PR #75 added the `prompts.pack_dir` traversal guard but the check was lexical only — `filepath.Rel(Clean(base), Clean(target))` catches `..` escapes but admits a path that lexically stays inside `base` while its actual filesystem target (via symlink) points outside. Defence-in-depth gap caught by every audit dogfood run since v0.10.0 and re-confirmed by claude on PR #88's review. v0.10.5 adds a second-pass `EvalSymlinks` on both `base` and `target`, then re-checks containment against the resolved real paths. Missing targets fall through to the lexical-pass result (the file-open path catches "no such file" downstream with a clearer error than this layer could produce); the fail-closed posture is preserved because the lexical pass runs FIRST and the EvalSymlinks pass can only ADD rejections, never admit a path lexical rejected. Three tests pin the invariants: `TestLoadUsesRepoYAML_PromptsPackDir_RejectsSymlinkEscape` (symlink at `<base>/evil-link → /etc` is rejected), `TestLoadUsesRepoYAML_PromptsPackDir_AllowsSymlinkInsideDir` (symlink staying inside `base` is allowed — the `current → versions/v2` legitimate-alias case), and `TestLoadUsesRepoYAML_PromptsPackDir_AllowsNonExistentTarget` (path that doesn't exist yet passes; the user can `mkdir` it later). Windows skipped via `runtime.GOOS` because symlink creation requires Developer Mode or admin privilege there — the hardening itself still applies, only the test exercises the platform-portable case.

### Fixed

- **Pre-flight probe phase wallclock now actually honours the 10s per-LLM cap on hung CLIs.** v0.10.1's pre-flight readiness probe was designed to collapse the ~4-minute gemini-exhausted-capacity hang to a sub-10s `✓`/`✗` signal — but in practice the probe phase wallclock blew past the cap whenever the underlying CLI's `cmd.Wait()` blocked on stdout/stderr pipe drainage after `exec.CommandContext` SIGKILLed the subprocess. Net result: v0.10.4's PR #86 dogfood reported a 4m34s probe phase against the exact case v0.10.1 was supposed to fix (the gemini-capacity-exhausted hang was re-confirmed twice in this session). v0.10.5 races `inv.RunPrompt` against `ctx.Done()` in `cli.Probe` using a goroutine + select pattern: when ctx expires first, `Probe` returns immediately with `ctx.Err()` as the underlying error, and the background goroutine drains until the OS reaps the subprocess (bounded by SIGKILL — typically ms, occasionally seconds, never indefinitely). The leaked goroutine writes to a buffered channel so it can't deadlock on send. Two regression tests pin the fix: `TestProbe_RespectsCtxDeadlineEvenWhenInvokerHangs` (uses a `hungInvoker` that deliberately ignores `ctx.Done()`, asserts Probe completes within 200ms of a 30ms deadline) and `TestProbeAll_PhaseRespectsTimeoutEvenWhenOneInvokerHangs` (asserts the fan-out phase wallclock is bounded by per-LLM timeout + scheduler slack). Both would have hung indefinitely pre-fix.

### Added

- **`ProbeCanceled` status distinct from `ProbeTimeout`.** Caught by codex during PR #88's own pre-push dogfood: the v0.10.5 timeout-fix above introduced a new `case <-ctx.Done()` branch that returned `ProbeTimeout` unconditionally — conflating real timeouts (`context.DeadlineExceeded`) with user cancellation (`context.Canceled`, e.g. Ctrl+C or a parent context being canceled). On a user interrupt the runner would surface "every active LLM failed pre-flight" when the actual cause was a deliberate stop signal. v0.10.5 adds a fourth `ProbeStatus` enum variant (`ProbeCanceled` — String() returns `"canceled"`) and branches on `ctx.Err()` inside the `Done()` case: `context.Canceled` → `ProbeCanceled`, anything else context-shaped → `ProbeTimeout`. The runner's post-probe handler now checks `ctx.Err()` first and propagates user cancellation directly (so `main()`'s signal-handler exit path gets the right exit code) before falling through to the "every LLM failed" message. The readiness block renders canceled probes with a distinct `⊘` glyph and `canceled` label so users can tell "I pressed Ctrl+C" from "vendor timed out" at a glance. Two pin tests (`TestProbe_CanceledIsDistinctFromTimeout`, `TestProbe_DeadlineExceededStillTimeout`) prevent the two states from collapsing again.

## [0.10.4] - 2026-05-25

**Theme: Ollama on the network.**

A focused patch release covering a single user-visible improvement: pointing local-review at Ollama running on a LAN server (e.g. a GPU box at `192.168.1.50:11434`) just works now, without setting a dummy `api_key` to fool the auth check. The pre-fix `isLocalURL` was deliberately narrow (loopback only) to keep auth enforcement on corporate gateways inside RFC1918, but in practice the narrow scope shadowed the much more common Ollama-on-LAN setup. v0.10.4 widens the bypass to RFC1918 + IPv6 ULA + IPv4 link-local while preserving the corporate-gateway invariant via the existing `c.APIKey == ""` guard: anyone who needs auth enforcement on a LAN host just sets `provider.api_key` as before. Three Copilot findings caught during PR review (IPv6 zone-suffix handling, doc-comment guard mis-citation, IPv6 documentation block phrasing) landed as in-PR follow-ups.

### Changed

- **`isLocalURL` widened to RFC1918 + IPv6 unique/link-local + IPv4 link-local.** Users running Ollama on a LAN server (e.g. `provider.base_url: http://192.168.1.50:11434/v1`) hit a confusing "no API key" error pre-v0.10.4, even though Ollama doesn't authenticate. The pre-fix scope was deliberately narrow — "corporate API gateway at 10.0.0.5 still authenticates and shouldn't bypass the check" — but in practice it shadowed the more common Ollama-on-LAN use case. v0.10.4 extends the bypass to `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` (IPv4 link-local APIPA), `fc00::/7` (IPv6 unique-local), and `fe80::/10` (IPv6 link-local). The corporate-gateway invariant is preserved by gating on `c.APIKey == ""` — the bypass ONLY fires when no key is configured, so an operator who needs auth-enforcement on a LAN host just sets `provider.api_key` (or `api_key_env`) as they would have for any non-local URL. mDNS `.local` hostnames are deliberately NOT included because they can resolve to anywhere. The widened set is pinned by 23 sub-tests in `internal/llm/client_test.go::TestIsLocalURL` covering RFC1918 boundary edges (172.15/172.32 just-outside-the-/12), public IPv4 (8.8.8.8), the IPv6 documentation reserved block 2001:db8::/32 (RFC 3849 — not publicly routable but treated as non-local for this check), IPv6 link-local with RFC 6874 zone identifiers (`fe80::1%en0` shape — needs the `%zone` suffix stripped before `net.ParseIP` to classify correctly), and the mDNS-must-still-require-auth case. README adds an "Ollama on a LAN host" row pointing users at `OLLAMA_HOST=0.0.0.0:11434` server-side config.

## [0.10.3] - 2026-05-25

**Theme: tighten the install path.**

A focused patch release closing the only remaining `warning`-severity finding from v0.10.0's first audit — `install.sh`'s env-var-only opt-out for skipping checksum verification. The fix is small (~30 lines of shell) but the surface it covers is the project's primary install path (`curl ... | sh`), so worth its own release rather than bundling into a larger v0.11. The TTY-aware probe + `if !` read guards (caught by Copilot mid-review) also harden the failure path against the ENXIO-under-`set -e` crash that would have terminated the script on some non-interactive contexts. Three-LLM review found no issues in this PR's diff; CodeRabbit caught a CHANGELOG header that I had accidentally deleted in an earlier edit. The interplay of reviewers each with their own blind spots is the trust signal.

### Security

- **`install.sh`: env-var checksum bypass no longer accepts silent opt-out when a TTY is available.** v0.10.0-RC1's `audit/security.md` (single `warning` finding the LLM caught on the stale-binary dogfood) flagged that `INSTALL_REVIEW_SKIP_VERIFICATION=1` could be set silently by a compromised shell rc, parent process, or CI config — forcing an unverified install without the user's explicit awareness. Three-way resolution now: (1) env var set explicitly → proceed with a loud warning (CI's documented escape hatch is preserved); (2) `/dev/tty` available (the common case, true even for `curl | sh` because stdin is the piped script but `/dev/tty` still points to the user's terminal) → prompt `y/N` for explicit acknowledgement that no env var alone could provide; (3) no env var AND no `/dev/tty` (true non-interactive CI without explicit opt-in) → fail loud with the env-var hint, same as pre-v0.10.3. Behaviour is unchanged for users who already pass the env var; the new interactive prompt only fires on the previously-fail-loud branch when a real terminal is reachable, replacing the v0.10.2 "Sorry, can't install — set this env var" wall with a one-key acknowledgement.

## [0.10.2] - 2026-05-25

**Theme: clear the deck.**

A no-features patch release whose job is to make v0.10.0's audit dogfood pay off — burn down the `major` findings `audit/tech-debt.md` surfaced on this codebase. Four of the six majors closed across PRs #80–#82; the remaining two (parallel `bench` / `swe-bench` code paths ~250 lines, and a `GeminiInvoker.run` / `ClaudeInvoker.run` extraction ~80 lines) deferred as needs-design rather than cleanup.

No user-visible behaviour change in any commit. Every PR shipped behind 0-blocking `local-review review main` self-review before merge; the burn-down also caught two genuine Copilot doc-clarity issues (vague "three writers" wording, wrong file-perm assertion — `os.Create` uses `0o666` masked by umask, not a guaranteed `0o644`) that landed as follow-up fixes inside the same PR.

Pattern worth noting: the v0.10.0 audit was the source for the entire v0.10.1 + v0.10.2 cleanup roadmap. The tool's own deep-codebase output is the project's roadmap now; not a one-time artifact.

### Removed

- **Dead code + dead parameters surfaced by v0.10.0's `audit/tech-debt.md`.** Three small deletions, no user-visible behavior change, opens the v0.10.2 burn-down on the audit's outstanding findings. (1) `internal/review/review.go` — removed the unused `matchGlob(path, pattern string) bool` function. It was lower-case (private), claimed in its doc comment to be "kept for back-compat with any external caller," but private functions have no external callers; production filtering goes through `compileGlobs` + `matchesAnyCompiled` (amortises regex compilation across the diff), tests go through `matchesAny`. Audit flagged it as dead code on `review.go:306-315`; the audit's framing was right. (2) `internal/cli/invoker.go` — removed the unused `errLabel string` parameter from `ClaudeInvoker.run` and its two callers (`Review`, `RunPrompt`). Pre-fix the parameter was passed in by every caller and explicitly discarded via `_ = errLabel` — a vestige of a pre-v0.7 "claude review failed:" prefix that the runner's per-LLM completion line now owns. CodexInvoker's `runExec` retains its `errLabel` parameter because it still distinguishes pre-invocation errors (temp-file creation) from post-success errors (output-file read), neither of which the runner's per-LLM line covers. (3) `internal/cli/version.go` — removed the unused `name` parameter from `detectVersion(name, path string)`. The function never read `name`; the audit's warning that "parameter suggests the function is name-aware when it's not" was accurate. Doc comment now points future maintainers at the right discriminator (`path`'s basename) if a CLI ever needs version-flag-specific branching.

### Changed

- **File-write boilerplate consolidated into `writeFileWithDirs` (audit/tech-debt #2).** `cmd/local-review/audit.go:writeAuditFile`, `bench.go:writeBenchToFile`, and `bench.go:writeSWEBenchToFile` all implemented the same `mkdir + create + deferred-close-with-error-check` sequence. New helper in `cmd/local-review/iohelpers.go` takes `(path string, emit func(io.Writer) error)` and owns that contract; the three call sites become one-line wrappers that close over their report value. No user-visible behavior change — file creation still calls `os.Create` (open mode `0o666`, masked down to typically `0o644` by the process umask), directory mode is `0o755`, and the close-error-doesn't-shadow-emit-error precedence is preserved exactly.

- **`mergedHasBlocking` → `isBlockingMarkdown` (audit/tech-debt #3).** Same function, clearer name. The audit flagged `runner.go:478` as duplication; the duplication claim was a false positive (there's only one implementation), but the function name was misleading enough that the auditor couldn't tell — it's called both against the merged report AND against each per-LLM Output (via `anyPerLLMHasBlocking` as the truncation-safe backstop). The rename makes the symmetry visible at the call site without changing behavior. Doc comment now spells out both call contexts so the next reader doesn't have to grep.

- **Scoring math deduplicated into package-private helpers (audit/tech-debt #4).** `internal/bench/types.go` had identical `Precision()` / `Recall()` / `F1()` methods on both `BaselineScore` (from --uplift) and `CaseScore` (the primary treatment pass) — six methods, three pairs, exactly the same formulas. Any improvement to the math had to land in two places, and a missed update would silently produce divergent leaderboard numbers for "the same" metric on the same case. v0.10.2 extracts `scorePrecision(tp, fp)`, `scoreRecall(tp, fn)`, `scoreF1(p, r)` into `internal/bench/score_helpers.go`; both score types' methods delegate. The asymmetric "0 on empty precision / 1 on empty recall" convention is documented at the helper (it's load-bearing for the per-language aggregator on clean-case slices) and pinned by tests, including a `TestScoreTypes_ShareSameMath` sentinel that runs the same `(tp, fp, fn)` triples through both `BaselineScore` and `CaseScore` so a future delegation-edit-on-only-one-type can't ship green.

## [0.10.1] - 2026-05-25

**Theme: fail fast on the slow path.**

A focused follow-up to v0.10.0 driven by the same first-customer dogfood run that produced v0.10.0's `audit/` reports. Two changes — one user-facing, one structural — both pointed at the same problem: the real-review code path was tolerating failure modes it should have been catching upfront.

The user-visible win is the **pre-flight LLM readiness probe**. v0.10.0's first run surfaced gemini's `"You have exhausted your capacity on this model."` error after **~4 minutes** of dead air. claude (32s) and codex (53s) had long since finished; the real-review timeout window was large enough to hide a doomed gemini for 240s. v0.10.1 inserts a `Reply OK` probe per LLM (10s per-LLM timeout) before the real fan-out and renders a `✓`/`✗` block immediately — the same signal in seconds, not minutes, and the run proceeds with only the surviving agents.

The structural win is the **`multi.GateDecision` consolidation** — the first cleanup landed against an `audit/tech-debt.md` finding from v0.10.0's dogfood. The dual-metric pattern that drove the gate decisions in `runner.go` lived in six call sites and had a documented history of drifting; consolidating into one type means future runs of `audit --topic tech-debt` won't keep flagging it. Both PRs ran through `local-review review main` self-review with 0 blocking findings before merge.

### Added

- **Pre-flight LLM readiness probe — `✓`/`✗` status in seconds, no more 4-minute hangs on doomed LLMs.** v0.10.0's first customer run surfaced gemini's `"You have exhausted your capacity on this model."` after **~4 minutes** of dead air inside the real-review timeout window — claude (32s) and codex (53s) had long since finished, but the user was stuck waiting for gemini's error to propagate. v0.10.1 adds a tiny `Reply OK` probe to every active LLM with a 10s per-LLM timeout *before* the real fan-out: the readiness block renders top-to-bottom as `claude  ✓ (1.2s)` / `gemini  ✗ You have exhausted your capacity on this model.` / `codex   ✓ (0.8s)` and the run proceeds with only the surviving agents. `--no-preflight` is an escape hatch for callers who want the v0.10.0 behaviour (CI jobs where the ~10s + ~1k tokens per LLM aren't worth it). The probe lives in `internal/cli/probe.go` as `cli.Probe` (single-LLM) / `cli.ProbeAll` (parallel fan-out preserving roster order) / `cli.SplitReady`; the runner wires it in after the existing static `PreflightFilter` (context-window check, no LLM call) and before `orch.RunParallel`. Hard fail with a `doctor` hint when every active LLM probe-fails — same posture as "all reviews failed" in the GateDecision path. Tests in `internal/cli/probe_test.go` pin per-status outcomes (`Ready`, `ErrorOnInvokerError`, `TimeoutOnDeadlineExceeded`, `TimeoutFromWrappedErrorString` covering the ClassifyExit-wrapped message-text match), `ProbeAll` roster-order preservation, mixed-shape outcomes (one ready / one capacity-exhausted / one timeout — the actual v0.10.0 dogfood shape), and a parallelism check (3×50ms probes must complete in <100ms wall-clock or the fan-out has serialised).

### Changed

- **Consolidated the dual-metric gate pattern in `cmd/local-review/runner.go` into a single `multi.GateDecision` type.** v0.10.0's first audit dogfood (`audit/tech-debt.md`) flagged `runner.go:156` as a `major` finding: six call sites in the runner each computed `CountSuccessful` (Error == nil) and/or `CountWithOutput` (HasMergeableOutput) independently, with load-bearing comments cataloguing two distinct historical bugs (SaveReview-failed-with-output, CLI-exited-zero-with-empty-output) where the two metrics had drifted apart. The new shape: `multi.DecideGate(results)` returns a `GateDecision` summarising both views in a single pass; the runner threads that one value through the zero-mergeable short-circuit, the run-mode classifier (renamed `classifyRunMode` → `classifyRunModeFromGate`), the merge-step framing, and the progress display lines. Error taxonomy for the zero-mergeable case moved onto the type as `GateDecision.ClassifyZero() ZeroMergeableReason` with three named cases (`ZeroMergeableAllFailed`, `ZeroMergeableAllEmpty`, `ZeroMergeableMixed`) — the runner picks the error message by switch, the categories themselves are pinned by tests in `internal/multi/orchestrator_test.go` (`TestDecideGate_*`) named for the underlying scenarios (`AllFailed`, `AllSucceededButEmpty`, `MixedFailedAndEmpty`, `SaveReviewFailedWithOutput_StillMergeable`, …) so a future regression makes the failing line readable without re-reading the body (CLAUDE.md rule 9). No user-facing behavior change — exit codes, error strings, and console output are byte-identical to v0.10.0; the change is structural, eliminating the surface area that historically drifted. `multi.CountSuccessful` and `multi.CountWithOutput` are retained as public helpers (still used in tests and stable callers), but the runner no longer uses them directly.

## [0.10.0] - 2026-05-25

**Theme: reach beyond the diff.**

v0.10.0 grows the tool past the one shape it has carried since v0: *review a diff, exit.* Three customer-driven additions break that frame. `audit --topic <security|tech-debt>` runs the same reviewer against the **whole committed codebase**, surfacing pre-existing issues no diff would catch — closes the solo-dev gap that `review` couldn't reach. New language packs for **Swift / Kotlin / Liquid** activate auto-detection on the 60% of users' daily code that previously fell through to the generic pack. And `bench --swe-bench` measures the reviewer against **real bugs from projects we did not author** — closing v0.9.0's "circular benchmark" critique with a SWE-bench-lite catch-rate leaderboard alongside the existing F1/uplift tables. Plus first-user dogfood of `audit` itself surfaced two real security issues in `internal/config/` (path traversal in `pack_dir` resolution, deprecated YAML keys winning over env vars) — both fixed before the tag, both committed in [`audit/security.md`](audit/security.md) and [`audit/tech-debt.md`](audit/tech-debt.md) as the second public trust artifact next to `bench/RESULTS.md`.

### Security

- **Reject `prompts.pack_dir` paths that escape the config directory.** First-user dogfood of the new `local-review audit --topic security` (PR #73) surfaced a real defence-in-depth gap in `internal/config/config.go`: a `.local-review.yml` with `pack_dir: ../../../etc` would resolve outside the config's own directory and let the override-resolver touch files there. Now `resolveRelativePaths` rejects any relative `pack_dir` that resolves outside `<config-dir>` via `..` segments. Absolute paths still pass through unchanged (explicit-opt-in to a specific location remains supported). Tests cover both directions: traversal rejection AND paths-with-`..`-that-stay-inside (`foo/../bar`) still passing.

- **Environment variables now take precedence over deprecated YAML-stored API keys.** Same audit pass flagged that the YAML `api_key` field — already marked DEPRECATED in the schema and warned about at load time — was still winning over `os.Getenv(api_key_env)` at resolution. That made the silent-stale-key footgun real: a developer who committed a test key to `.local-review.yml`, then later set a correct prod key in the environment, would keep using the stale YAML value. v0.10.0 flips the precedence: when the env var is set, it overrides the YAML key (and the warning text now explicitly says so). Empty env still falls back to the YAML key for backward compatibility with users who put a key there and never set an env var. Behaviour change is small (only users with BOTH YAML and env-var set are affected, and they get the env-var anyway — which is what most expected).

### Fixed

- **`local-review audit` now auto-splits oversized packages instead of failing them with `prompt_too_long`.** First-user dogfood on an Android codebase failed 321 of 343 packages because the previous build's "256 KiB soft cap" only emitted a warning and shipped oversized chunks to Claude, which rejected them. The new behaviour: greedy bin-packing into sub-chunks under the cap, with `pkg [part N/M]` labelling preserved in the report. Per-file adjacency is preserved (files A, B, C, D pack as [A,B] + [C,D] — relevant for LLM cross-file reasoning within a package). Default cap lowered from 256 KiB to **96 KiB** based on empirical headroom needed for claude-code's own system prompt + tool definitions on top of the audit pack body. Single files individually over the cap stay as standalone chunks with a clearer warning ("cannot split a single file across chunks") — splitting source mid-function would produce semantically broken chunks; the caller can `--include` / `--exclude` around them.

### Added

- **`local-review audit --topic <security|tech-debt>` — deep analysis of the committed codebase (PR 0.10.0-c).** New top-level subcommand that walks `git ls-files`, groups source by directory, and runs each package through the LLM with a topic-specific system prompt. Unlike `review` (which inspects a git diff), `audit` surfaces accumulated issues that no individual diff would catch: pre-existing security gaps, dead code, duplicated logic, leaky abstractions. Single-LLM in v1 (audit cost is per-package × per-topic; multi-LLM would multiply spend without obvious quality return — deferred until we see real usage). New audit-pack mechanism in `internal/prompts/audit/<topic>.md` paralleling the language-pack discovery (`prompts.GetAuditPack`, `prompts.AvailableAuditTopics`); new `internal/audit/` package with walker (chunks by directory, auto-splitting packages over the per-chunk cap into `pkg [part N/M]` sub-chunks — see the `### Fixed` entry above for the empirical 96 KiB default), runner (drives the LLM and parses findings), and renderer (text + markdown + JSON). New `internal/git.TrackedFiles()` helper using `git ls-files -z` (NUL-separated so paths containing newlines round-trip). `--dry-run` prints the chunk plan without invoking the LLM so users can preview cost before paying tokens; `--include` / `--exclude` filter to a subset; `--out <path>` writes markdown to a file (or JSON when the extension is `.json`). Day-1 ships two topics: **security** (OWASP-aligned sweep — hardcoded secrets, missing authorization, weak crypto, injection surfaces, path traversal, …) and **tech-debt** (dead code, duplicated logic, leaky abstractions, inconsistent error handling, architectural smells). Audit packs deliberately skip the `nit` severity tier — whole-codebase reading produces enough signal that nits dilute the report. Closes the solo-dev gap that `review` couldn't reach: "I'm shipping into main alone; what's accumulating in code I haven't touched in months?"

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
