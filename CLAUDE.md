# CLAUDE.md

Instructions for Claude Code (and any agent) operating in this repository.
These OVERRIDE default behavior — read them before acting.

---

## Operating rules

These apply to every task in this repo. They exist because each one has cost us a review round, an incident, or a rewrite.

1. **Surgical changes only.** Touch what the task requires. No drive-by formatting, comment polish, or "while I'm here" refactors. If you spot something worth fixing, surface it separately — do not bundle it.
   *Why:* mixed-purpose diffs are the hardest to review and the easiest to revert badly.

2. **Read before you write.** Before editing a shared helper, contract, or user-visible string, grep all callers in the same pass — sibling code sites, doc comments, README, CHANGELOG, prompt templates, tests. Drift across files is the #1 reviewer-flagged defect class on this codebase. The `CountSuccessful` → `CountWithOutput` migration drifted across 5 review rounds before all sites aligned.

3. **Doc comments and cobra `Long:` strings describe *current* behavior.** Update the comment in the same edit that changes the behavior. A stale comment is a bug.

4. **Fail loud, fail closed.** Use `TrimSpace` for emptiness checks. Check `grep` exit code separately from `sha256sum`. Honor `pageInfo.hasNextPage`. Refuse on invalid input rather than silently passing it through. "Completed" is wrong if anything was skipped silently — surface what was skipped.

5. **Writing a v2 path next to v1? Enumerate v1's invariants explicitly and re-exercise each in v2.** Filters, severity caps, prompt-pack selection, JSON output, glob behavior — none of these carry over implicitly. The v0.5.0 multi-LLM rewrite shipped 5 separate "v2 dropped this" findings.

6. **User-visible strings drift fast.** When changing a CLI output line, error message, or warning, grep the repo for the prior wording — CHANGELOG, README, prompt templates, help text, and tests likely all need updating.

7. **State assumptions, then proceed.** Auto mode prefers action over questions. When ambiguous, name the assumption out loud in one sentence and continue. Stop and ask only for irreversible or shared-state actions (push, force-push, schema change, deleting data).

8. **Surface conflicts; don't average them.** If two patterns contradict, pick the more recent / more tested one, explain why in the diff or commit message, and flag the other for cleanup. Do not silently blend them.

9. **Tests encode the invariant, not the call.** A test named `Test_FooReturnsErrorOnEmptyInput` is better than `Test_Foo_2`. If a test can't fail when the business rule changes, it isn't testing the rule.

10. **Match the codebase's conventions even if you disagree.** Conformance > taste. If you genuinely think a convention is harmful, surface it in chat — don't fork silently.

11. **Pre-push self-review is non-negotiable.** Before pushing any branch with code changes, run `./local-review review main` (or `--only claude` if multi-LLM is too slow). Address `major` / `critical` findings before pushing. Skip only for pure docs / website / trivial-config changes. We eat our own dog food; if the tool produces noise on this codebase, that's a bug to file or fix.

12. **`cmd/local-review/runner.go` and `doctor.go` are the danger zone.** They historically account for ~40% of all reviewer findings. Both are orchestration files where multiple concepts meet. Apply rules 1–6 with extra care here.

---

## What this project is

`local-review` is a local, BYOK AI code reviewer — a single Go binary that runs against git diffs (staged / commit / branch).

**Two paths, picked automatically:**
- **Multi-LLM (default)** — every authenticated LLM CLI (claude, gemini, codex) runs in parallel against the same diff; findings are merged into one consolidated report.
- **Single-LLM fallback** — when no CLI is authenticated, hit the configured `provider:` (any OpenAI-compatible endpoint, including local Ollama).

**Hard constraints:**
- No vendor SDKs (keeps the binary small and portable).
- No telemetry (privacy first).
- Git CLI integration only — never go-git.
- Exit codes: `0` = no blocking findings, `2` = `major` or `critical` present (used by pre-commit hooks), non-zero = tool failure (hooks ignore so commits go through).

---

## Architecture map

Code is authoritative; this is just a pointer table. Read the package, don't trust the prose.

| Package | Purpose |
|---|---|
| [cmd/local-review/](cmd/local-review/) | Cobra entrypoint, `runner.go` dispatcher, `doctor.go`, `init.go`, `bench.go` |
| [internal/cli/](internal/cli/) | LLM CLI detection, version probe, invocation patterns per LLM |
| [internal/multi/](internal/multi/) | Parallel orchestrator, LLM-powered merger, on-disk storage, metadata |
| [internal/config/](internal/config/) | YAML cascade (`~/.local-review.yml` → `./.local-review.yml` → flags) |
| [internal/llm/](internal/llm/) | SDK-free HTTP client for OpenAI-compat endpoints (single-LLM fallback) |
| [internal/git/](internal/git/) | Shells out to `git`; `-U10` context; modes: `staged`, `commit <rev>`, `branch <base>...HEAD` |
| [internal/lang/](internal/lang/) | Extension → language ID mapping |
| [internal/prompts/](internal/prompts/) | `go:embed`-ed packs in `packs/*.md`; user override via `prompts.pack_dir` (v0.7+) |
| [internal/review/](internal/review/) | Filtering (min_severity, max_findings, include/exclude globs), formatting |
| [internal/output/](internal/output/) | Text and JSON renderers |
| [bench/](bench/) | Regression dataset + harness; `--uplift` compares treatment vs baseline (v0.8+) |

---

## Multi-LLM model — non-obvious facts

These are things you can't infer by reading the code in 30 seconds:

- **Codex invocation uses `codex exec --output-last-message`, NOT `codex review --commit`.** The dedicated subcommand re-extracts the diff itself, breaking the orchestrator's "extract once, fan out the same diff to all agents" contract.
- **Gemini gets a short `-p` prompt + the diff via stdin.** In headless mode, gemini appends `-p` content to stdin, sidestepping argv-size limits.
- **Version-probe regex failures fail silently.** `internal/cli/detector.go` marks an LLM `Available=false` on probe miss; the runner filters silently. `local-review doctor` is the only diagnostic surface — loosen the regex if a real CLI breaks.
- **Merge LLM selection priority:** `--merge-with <llm>` → `merge.preferred_llm` → auto (claude > codex > gemini).
- **Parallel execution continues on partial failure.** Failed LLMs are noted in `metadata.json` and `merged.md`; not-installed LLMs are skipped silently.
- **Token fields (v0.6.6+):** `input_tokens`/`output_tokens` are `omitempty`. Codex pre-v0.128 emits `total_only_tokens: true` — `input_tokens` then holds the combined total; render as "Nk total", not "Nk in / 0 out".
- **No standalone re-merge command.** To re-merge an existing commit, re-run `local-review commit <ref>`.
- **Pre-flight readiness probe runs BEFORE the real fan-out (v0.10.1+).** Each authenticated LLM gets a 10s `Reply OK` probe via `cli.ProbeAll`; failures (timeout / error / canceled) skip the real call. Probe wallclock is bounded by a `select` against `ctx.Done()` because `cmd.Wait()` would otherwise block on subprocess pipe drainage past the deadline (v0.10.5 fix — the v0.10.1 feature was failing on its target case for 4 patch releases). `--no-preflight` opts out. See `internal/cli/probe.go`.
- **Probe failures surface the vendor's actual stderr message (v0.10.6+).** Each invoker tees subprocess stderr through a 4-KiB `stderrCapture` ring buffer via `io.MultiWriter`; when the probe times out, `Probe` peeks the buffer and replaces the generic "timeout after Ns" with `timeout after Ns — <vendor message>` (e.g. `Error: You have exhausted your capacity on this model.`). Optional `cli.PartialStderrCapturer` interface — invokers that don't implement it fall back to the generic message. Custom `probeTimeoutErr` type preserves the `context.DeadlineExceeded` unwrap chain.
- **`ProbeCanceled` is distinct from `ProbeTimeout` (v0.10.5+).** User Ctrl+C surfaces with the `⊘ canceled` glyph and propagates through the runner's signal-handler exit path; vendor timeouts get the `✗` glyph. Don't conflate them — the runner's post-probe handler checks `ctx.Err()` FIRST and short-circuits on cancel before falling through to "every LLM failed."
- **Audit uses the same probe + invoker path as review (v0.10+).** `local-review audit --topic <id>` picks ONE authenticated LLM (first match; claude when available) and walks `git ls-files`-grouped chunks through it. Chunks above the per-chunk cap (default 96 KiB) auto-split into `pkg [part N/M]` sub-chunks via greedy bin-packing in `internal/audit/walker.go`, preserving per-file adjacency. Negative `--max-bytes-per-chunk` is rejected up front.
- **`isLocalURL` widened to RFC1918 + IPv6 ULA/link-local (v0.10.4).** Ollama on a LAN host (`http://192.168.x.x:11434/v1`) no longer needs a dummy `api_key`. Corporate-gateway invariant preserved via the existing `c.APIKey == ""` guard — bypass only fires when no key is configured. IPv6 zone suffixes (`fe80::1%en0`) stripped before `net.ParseIP`. See `internal/llm/client.go`.
- **`pathInsideDir` resolves symlinks before containment (v0.10.5).** Lexical-only check (v0.10.0) admitted symlink-escape paths. v0.10.5 adds `EvalSymlinks` + deepest-existing-ancestor walk-up (closes the missing-leaf bypass — `evil-link/new-leaf` where `evil-link → /etc`); fail-closed on resolve errors. See `internal/config/config.go`.
- **Antigravity (`agy`) is DETECTED but NOT a reviewer (2026-05).** Google's Gemini-CLI successor is surfaced by `doctor` (`◐ experimental` row) but gated out of the fan-out by `cli.IsReviewCapable("antigravity") == false`. The authenticated dogfood showed agy's headless `--print` runs a full autonomous agent loop: it explores the repo, reconstructs its OWN diff (ignoring the one passed), and streams tool-step narration ("I will run git diff…") to stdout instead of a review — a real `review --only antigravity` yielded 6.5 KB of narration, zero findings, empty merge. Short prompts can also return empty stdout. `AntigravityInvoker` exists as scaffolding only (reachable from `NewInvoker` for type/interface tests, never a real run). `--only antigravity` refuses cleanly. Don't wire it into the active set without first solving the structured-output / suppress-agent-loop invocation contract. Gemini stays the live Google agent until its 2026-06-18 cutoff.

---

## Storage

Reviews land at `.local-review/reviews/<sanitized-branch>/`:

```
abc123_claude_3.5.md      # one per active LLM
abc123_codex_0.128.md
abc123_gemini_0.40.md
abc123_merged.md          # consolidated by the merge LLM
abc123_metadata.json      # timestamps, exit codes, token counts, versions
```

Branch sanitization: `/` → `-`. Override base path via `storage.base_path` in config.

---

## Commands cheat sheet

```sh
local-review review              # canonical: current branch vs main (multi-LLM if any CLI authenticated)
local-review staged              # pre-commit shape
local-review commit [<rev>]      # one commit (default HEAD)
local-review branch [<base>]     # alias of review
local-review review --only claude,gemini       # restrict agent set
local-review review --merge-with claude        # pick merge LLM
local-review review --claude-model <id>        # override one agent's model

local-review init                # interactive config wizard
local-review doctor              # check installs + auth + version-probe state
local-review config              # print resolved config (keys masked)
local-review version
```

---

## Development

```sh
go test ./...                    # unit tests
go test -race ./...              # CI standard
go build -o local-review ./cmd/local-review
./local-review review            # smoke test
./local-review doctor            # check LLM installs
```

Required: Go 1.23+, git in PATH. For multi-LLM testing: Node 20+ plus at least one of `@anthropic-ai/claude-code`, `@google/gemini-cli`, `@openai/codex`. For single-LLM fallback: `export LOCAL_REVIEW_API_KEY=sk-...`.

CI (`.github/workflows/ci.yml`) runs `go vet` + `go test -race` + build on every push. Tagging `vX.Y.Z` on main triggers cross-compiled releases (darwin/linux/windows × amd64/arm64).

---

## Adding a new language

1. Create `internal/prompts/packs/<langid>.md`.
2. Add extension(s) to `byExt` in [internal/lang/detect.go](internal/lang/detect.go).
3. Add a constant for the language ID (e.g., `const Rust = "rust"`).
4. `go test ./internal/prompts/... ./internal/lang/...`.

---

## Style

- `gofmt -s` + `go vet` clean.
- Comment intent (*why*), never mechanics (*what*) — well-named identifiers describe what.
- One-line doc comments on exported symbols only.
- New logic needs a unit test.
- No vendor SDKs in `internal/llm/` — raw HTTP only.
