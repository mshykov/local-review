# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.6.4] - 2026-05-06

### Changed
- **Default per-agent timeout raised from 120s → 600s (10 min).** User feedback after v0.6.3: claude (Anthropic Sonnet on a thinking model) regularly took 2–5 min on branch-sized diffs while gemini and codex finished in 80–100s. The 120s default kept timing claude out on real-world `local-review review` runs even though the agent was making forward progress. 600s gives enough headroom for a worst-case agent on a worst-case diff while still failing fast on a genuinely hung subprocess. Users who want shorter timeouts can override per-agent via `llms.<agent>.timeout_sec:` in `.local-review.yml`. Same bump applied to: per-agent default in `Defaults()`, the `applyConfig` fallback in the runner, the `RunParallel` fallback in the orchestrator, the merge-step fallback in `mergeAndPrint`, and the v0 single-LLM API path's `Provider.TimeoutSec` (60s → 600s).

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
- **`install.sh` verifies SHA-256 checksums** of release tarballs. Each release now ships a `checksums.txt` manifest; the installer downloads it alongside the tarball and verifies before extracting. Defends against accidental corruption and basic CDN/MITM tampering. Cosign signing is on the v0.7 roadmap for compromised-release-key defense.

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
- **`Authorization` / `--json` / `--min-severity` / `--max-findings` ignored in multi-LLM** — README now states this explicitly instead of "passes them through with a warning". Multi-LLM emits a stderr warning when those flags are set but otherwise drops them; the structured-JSON multi-LLM mode is on the v0.7 roadmap.
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
