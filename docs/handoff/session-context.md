# Session context — 2026-07 audit & hardening cycle

Working notes handed off between assistant sessions. Records **what changed and
why**, plus what's still open. Project conventions live in
[CLAUDE.md](../../CLAUDE.md) and the topic docs; this file is state, not rules.

> **No personal data.** Per CLAUDE.md rule 13, endpoints/hosts here are
> placeholders (`http://<ollama-host>:11434/v1`), never real IPs — committing a
> real Tailscale IP was an actual incident in v0.13.0.

---

## Releases shipped

| Version | Theme |
|---|---|
| **v0.17.4** | External architecture + SecOps audit fixes |
| **v0.17.5** | Provider/report visibility — "stop trusting quiet failures" |

Both cut via the manual-dispatch path (`gh workflow run release.yml -f version=vX.Y.Z`),
since no merged PR carried a `release` label. Both verified: 5 platform binaries,
checksums manifest, signed SLSA provenance (`gh attestation verify <file> --repo
mshykov/local-review`), and Homebrew formula bumped.

---

## What shipped, by theme

### 1. SonarCloud backlog (PRs #158, #162–#165, #172)
10 Security + 22 Maintainability findings cleared. Added a **gocognit CI gate**
(`.golangci.yml` + ci.yml "Complexity" step) enforcing per-function complexity ≤15
across the whole repo — Sonar only gates a PR's diff, so pre-existing debt was
invisible until touched. The gate has since caught the assistant's own code twice.

**Sonar baseline gotcha:** `main`'s quality gate silently drifted red for ~2 months
because `sonar.projectVersion` was never set — the `previous_version` New Code mode
never saw a release boundary and kept comparing against v0.7.2. Fixed in #171
(`git describe --tags --abbrev=0`, fails loud if tags don't resolve). **The reset
lands one scan late** — check `/api/qualitygates/project_status` → `periods[0].date`
before diagnosing a "stuck" baseline again.

### 2. External audit (PRs #173, #174, #176, #178, #179, #180)
An independent architecture + SecOps audit found:

- 🔴 **`local-review commit <rev>` was broken for every explicit ref, including
  `HEAD`, for ~11 releases.** `rev-parse --short -- <ref>` treats the arg as a
  *pathspec*; the v0.6.0 flag-injection hardening broke the feature it defended.
  Fixed with `--verify <ref>^{commit}`. Survived so long because nothing tested the
  ref helpers against real git — that gap is now closed.
- 🟠 **Mutable releases** — the publish job reused tags and overwrote assets
  *including the checksums manifest*. Now append-only + immutable releases enabled +
  `v*` tag ruleset + signed provenance.
- 🟠 **Untrusted repo-config strip-list too narrow** — an *absolute*
  `prompts.pack_dir` bypassed containment entirely (only relative paths were
  checked), and `storage.base_path` let a hostile repo choose where files are
  written. Both stripped now; house-rules fields still merge but emit a
  `NOTE: repo config … shapes this review` line.
- 🟡 `WaitDelay` was missing on every real invoker path (the v0.10.5 pipe-drain
  wedge was only fixed in the probe); Copilot's tools-disabled argv had no
  regression test; provider-spec construction was duplicated and had drifted; dead
  `internal/output` package (469 lines).

### 3. Provider/report visibility (PR #188, v0.17.5)
Four dogfood findings, all "the tool had the evidence and didn't say anything":

1. **Silent truncation** — llama.cpp servers drop prompt overflow past the context
   window and still return HTTP 200. A 15,147-token diff was processed as 2,050
   tokens and reported *"No issues found"* — a clean APPROVE on 14% of a payment
   diff. Now compares the endpoint's reported `prompt_tokens` against the prompt
   sent; warns on a 2× gap.
2. **Provider timeouts** gave a bare `context deadline exceeded` while CLI agents
   got `ClassifyExit` guidance. Now equivalent hints, plus a local-endpoint note
   that a cloud agent is faster.
3. **Slow local runs** — a 17.7k-token diff to a local 7B took 20m42s. Now warns
   *before* the request goes out.
4. **Malformed merged reports** — a run claimed `Total findings: 5` while rendering
   two bullets (the *same* finding under both "Major Issues" and "Info / Notes").
   `multi.ValidateReport` checks the claimed count against rendered bullets and
   flags cross-section duplicates. **Warns after the report; never rewrites it** —
   findings may still be useful, and silently fixing a count would hide an
   unreliable merge model.

Every warning has a false-positive guard test. A warning channel that cries wolf
gets ignored.

### 4. Misc
`config` now prints a `# Config sources:` block (#181); claude API errors surface
the vendor diagnosis instead of JSON tail noise, and Copilot's per-run Premium cost
is on the roster line (#183); `setup-go check-latest` so a stale runner toolchain
can't hold CI on a vulnerable Go patch (#182).

---

## Repo/infra state

**Applied via API this session:**
- Immutable releases: **enabled**
- `v*` tag ruleset: update/deletion/force-push blocked, no bypass
- `homebrew-tap` `main` ruleset: force-push + deletion blocked
- Default workflow token → **read**; workflows can no longer approve PRs

**Applied by the maintainer:** `TAP_GITHUB_TOKEN` rotated to a **fine-grained PAT
scoped to `mshykov/homebrew-tap`, Contents: read/write**. Verified working — the
v0.17.5 `update-homebrew` job succeeded with it.

Alerts: **0** open across Dependabot / code-scanning / secret-scanning.

---

## Local ollama setup — hard-won knowledge

Config lives in `~/.local-review.yml` (the tool reads **`.yml` only** — a `.yaml`
file is silently ignored).

Two failure modes cost real time, both now diagnosed automatically by v0.17.5:

| Symptom | Cause | Fix |
|---|---|---|
| `400 … prompt is longer than the context length` | Model + context don't fit VRAM | Smaller model, or raise context |
| **`200 OK` + "No issues found"** on a big diff | **Silent truncation** to the context ceiling | `OLLAMA_CONTEXT_LENGTH=32768 ollama serve` |
| `context deadline exceeded` at exactly `timeout_seconds` | Local 7B ≈ 10 tok/s can't finish a 17k-token review | Cloud agent, or `commit <rev>`, or raise timeout |

**`OLLAMA_CONTEXT_LENGTH` must be on the same line as `ollama serve`** — setting it
in another shell, or restarting with a bare `ollama serve`, silently keeps the
4096 default. Verify with `n_ctx_slot` in the server log, not by assumption. If the
macOS Ollama.app is running, quit it first or it keeps serving on :11434.

**Practical guidance:** a local 7B is fine for small diffs and offline work. For
anything large — and for anything security-sensitive — use `--only claude`. A 20-minute
local run that produces a malformed report is worse than a 60-second cloud one.

**Detection vs config (surprised the maintainer twice):** `doctor` is a *machine
inventory* — it lists every installed+authenticated CLI regardless of YAML, and
doesn't honor `enabled: false`. Only `review` honors `enabled` / `--only` / sunset.
Absence from YAML never excludes an agent.

---

## Open items

1. **Delete the old classic `repo`-scope PAT** at <https://github.com/settings/tokens>
   — superseded by the fine-grained one, now dead weight and standing risk.
2. **`docs/release.md` still says `TAP_GITHUB_TOKEN` needs "`repo` scope"** — stale
   and would lead a future reader to recreate the over-privileged token. One-line fix.
3. **2 open Dependabot PRs** (#185 `x/term`, #190 actions group) — routine.
4. **`zero-to-moat`: 7 open Dependabot PRs**, including majors (`next` 15→16,
   `typescript` 5→6) that need real testing, not rubber-stamping.
5. **Parked suggestion:** point contributor gitleaks install hints at `go tool`.

---

## Working conventions that mattered

- **Branch before editing.** Started on `main` once and had to move the commit.
- **One concern per PR**, full verify before push (`gofmt -s`, `go vet`,
  `golangci-lint`, `go test -race ./...`, e2e).
- **Copilot re-reviews every push**, and required-conversation-resolution blocks
  merge — so a fix-push often spawns new threads. Most were legitimate (stdout
  cleanup, shell-quoting brittleness, a Windows skip, import grouping, concurrent
  write interleaving); a few raced fixes already pushed. Budget for 2–5 rounds.
- **Pre-push dogfood self-review was skipped** in these sessions (the `claude` CLI
  can't run inside a nested-agent context) — the documented fallback (full test
  suites + CI + CodeRabbit) was used and said so in each PR.
- **Verify claims against the source**, not memory. Several "obvious" conclusions
  were wrong until checked (the broken `commit` command was found by *running* it).
