# local-review benchmark suite

This directory holds the **review-quality benchmark** introduced in
issue #56. It gives the project a reproducible signal — precision,
recall, F1, noise rate, consistency — for prompt and model changes,
instead of shipping releases on vibes.

**Phase 1** shipped the harness: a small hand-curated dataset, an
in-CLI scorer, and a deterministic replay mode for CI.
**Phase 2** added per-language splits, consistency runs
(`--repeat N`, Jaccard similarity), a markdown leaderboard
(`--markdown bench/RESULTS.md`), and grew the dataset to **10
cases** spanning Go, TypeScript, Python, and Rust.
**Phase 3** (v0.10.0 in progress) adds a **SWE-bench-lite
catch-rate mode** (`--swe-bench`) measuring how the reviewer
performs on real-world bug-introducing diffs adapted from the
upstream SWE-bench-lite dataset — closes the "circular benchmark"
critique by scoring against bugs we did not author. See
[`swe-bench-lite/README.md`](swe-bench-lite/README.md). A
partial-credit LLM-as-judge backstop and cross-tool comparisons
(CodeRabbit, Copilot review, etc.) are tracked for later releases.

## Layout

```text
bench/
  dataset/                   # labelled inputs
    <case-id>/
      case.yaml              # metadata + expected findings
      diff.patch             # the diff fed to the reviewer
  fixtures/                  # pre-recorded LLM outputs (for --replay)
    <case-id>/
      claude.md
      codex.md
      gemini.md
```

## Running the bench

Live (slow, costs tokens, requires authenticated LLM CLIs):

```sh
local-review bench
```

Replay (deterministic, free, no auth — what CI uses):

```sh
local-review bench --replay bench/fixtures
```

Useful flags:

| flag | effect |
| --- | --- |
| `--dataset <path>` | override the dataset root (default `bench/dataset`) |
| `--replay <path>` | switch to replay mode and read from `<path>/<case>/<llm>.md` |
| `--only claude,codex` | restrict to a subset of agents |
| `--json` | emit machine-readable JSON to stdout |
| `--out <path>` | also write JSON to disk (text still goes to stdout) |
| `--markdown <path>` | also write a leaderboard markdown table to disk (Phase 2) |
| `--repeat N` | sample each (case, LLM) N times for Jaccard consistency (live mode only; Phase 2) |
| `--uplift` | also run each (case, LLM) with a minimal generic system prompt and report treatment-vs-baseline deltas (live mode only; Phase 3) |
| `--strict` | exit non-zero on any per-case error; default ON in `--replay` |
| `--swe-bench` | switch to SWE-bench-lite catch-rate mode (binary `caught`/`missed` scoring against bug-introducing diffs). Mutually exclusive with `--uplift` and `--repeat > 1`. See the "SWE-bench-lite catch-rate mode" section below. |
| `--swe-bench-dataset <dir>` | override the SWE-bench dataset root (default `bench/swe-bench-lite`) |

## Scoring rules

For each non-clean case, every expected finding tries to match a
produced finding:

* **File** matches by path suffix on a path-segment boundary, so
  `login.go` matches `src/auth/login.go` but `bar.go` does not match
  `foobar.go`.
* **Line** matches within `±window` lines (default `3`, override per
  expected finding). `line: 0` means "anywhere in the file".
* **Category** and **severity** are recorded but **not** required
  for a match — LLM markdown output is too noisy on those dimensions
  in v1 to use them as a filter without inflating false negatives.
* Each produced finding satisfies **at most one** expected finding,
  so a single bullet covering the right line doesn't paper over
  two distinct expected bugs nearby.

Aggregates across the dataset:

* **Precision / Recall / F1** are micro-averaged across non-clean
  cases (sum TP, FP, FN; then compute). This weights cases by how
  many bugs they contain — what a user actually cares about.
* **Noise rate** is the mean number of findings produced per
  *clean* case (`clean: true` in `case.yaml`). Lower is better. A
  noisy reviewer trains people to ignore findings, which is worse
  than missing some bugs.
* **Per-language F1** is the same micro-average computed against
  each language subset. Lets you see "did the new prompt pack help
  Go without regressing TypeScript?" without arithmetic. The split
  is omitted automatically when the dataset has only one language.
* **Consistency** is the mean Jaccard similarity (over `--repeat N`
  live runs) of finding `(file, line)` sets per case. 1.0 means
  every run produced the same set, 0.0 means no overlap. Severity
  and snippet are deliberately ignored — LLMs paraphrase the same
  finding's title across runs, but `file:line` survives. Replay
  mode rejects `--repeat > 1` (fixtures are deterministic; the
  number would always be 1.0 and tell you nothing about the
  underlying model).
* **Uplift over baseline** (Phase 3, `--uplift`) answers the
  question that drives adoption: *is local-review better than
  running the raw LLM cold?* Runs each `(case, LLM)` pair twice:
  - **Baseline**: minimal generic system prompt (`prompts.BaselinePrompt`)
    — the kind of thing you'd type into Claude.app: "you are a
    code reviewer, list bugs with file:line, be concise."
  - **Treatment**: full local-review pipeline (language-specific
    pack via `prompts.Resolve`).
  The leaderboard adds a row per LLM showing
  `treatment (Δ vs baseline)` for F1 / precision / recall / noise.
  A positive Δ on F1 means local-review's pack adds value over a
  generic prompt; a negative Δ means the pack is hurting.
  **Why we don't tune the baseline**: the point is to measure
  what no-effort produces, not to maximise apparent uplift.
  Tuning the baseline would inflate the delta artificially.
  Replay mode rejects `--uplift` (need real LLM calls to measure
  the baseline; cached fixtures for both sides would just compare
  the fixtures to themselves).
* **Median / p95** wall-clock are computed from successful runs
  only. Errors don't pollute timing.

## Adding a case

1. Pick a stable `<case-id>` (kebab-case, language-prefixed by
   convention: `go-…`, `ts-…`, `python-…`).
2. Create `bench/dataset/<case-id>/case.yaml`:

   ```yaml
   id: go-race-mapwrite-1
   title: Concurrent write to a map without lock
   language: go
   description: |
     Goroutine spawned in handler() writes to s.cache without holding
     s.mu. The map is read by other handlers, so this is a data race.
   expected:
     - file: server/cache.go
       line: 42
       category: correctness
       severity: major
       note: write to s.cache without holding s.mu
   ```

   For a clean case (used to measure noise on diffs that should
   produce no findings) set `clean: true` and omit `expected:`.

3. Add `bench/dataset/<case-id>/diff.patch` — the unified diff that
   would actually be reviewed. Use the same `## File: <path>` /
   `@@ ... @@` shape that `local-review` itself feeds to LLMs (see
   `internal/multi/orchestrator.go` for the format).

4. Optionally record fixtures under `bench/fixtures/<case-id>/<llm>.md`
   so CI can score the case in replay mode. The simplest way is to
   run the bench live once and copy each LLM's output out of
   `.local-review/reviews/...`. Hand-edit if needed for stability.

5. Run `local-review bench --replay bench/fixtures` and confirm the
   numbers look sane.

## Why replay mode?

* **CI determinism.** A live run depends on three external services
  and their model snapshots. Replay reads files. CI passes or fails
  on the parser/scorer/dataset, not on a vendor outage.
* **No tokens spent on every PR.** Live runs cost real money;
  replay is free.
* **No CLI auth in CI.** GitHub Actions doesn't have access to your
  Anthropic / OpenAI / Google credentials. Replay sidesteps the
  whole auth surface.

The trade-off: a replay fixture is a **point-in-time snapshot** of
what one model said at one moment. Re-record fixtures (live `local-
review bench` + copy outputs) when prompt packs or expected
behaviour changes meaningfully — otherwise replay scores measure
"how the v0.6 prompt pack scored on cached v0.6 outputs," which is
not what a v0.7 release actually ships.

## SWE-bench-lite catch-rate mode (v0.10+)

The primary bench (above) measures precision/recall/F1 on a curated
labelled dataset where we wrote both the diff and the expected
findings. That's deliberate — the dataset is small, every case is
auditable, and per-language splits are stable. But it's also a
circular trust signal: we wrote both the question and the answer
key.

`local-review bench --swe-bench` runs against **bug-introducing
diffs in the SWE-bench-lite format** — diffs adapted by
reverse-applying a known fix patch, scored by case-insensitive
keyword match between the reviewer's markdown and the task's
`expected_keywords`. The mode + scorer + report shape are the
permanent infrastructure; the *credibility* depends entirely on
what's in the dataset.

**v0.10.0 status (current):** the shipped `bench/swe-bench-lite/`
dataset contains **3 synthetic SWE-bench-shaped examples**
(paginator off-by-one, retry loop swallowing the wrong exception
class, ORM SQL injection). These are clearly labelled as examples
in the dataset README — useful for exercising the harness, but
they don't yet close the "circular benchmark" critique because
*we* still wrote both the diff and the keyword answer key.

**Roadmap:** real-task curation from the upstream SWE-bench-lite
dataset (target N=10) lands as a follow-up. At that point the
catch-rate measures performance against real bugs from real
projects we did not author — the credibility signal the v0.8 /
v0.9 leaderboard couldn't provide on its own.

```sh
local-review bench --swe-bench                     # use default dataset (bench/swe-bench-lite/)
local-review bench --swe-bench --swe-bench-dataset <dir>   # custom
local-review bench --swe-bench --only claude       # restrict agents
```

Scoring is binary (`caught` / `missed`) in v1 — a partial-credit
LLM-as-judge tier is tracked for a later release. **Error frames
count toward the catch-rate denominator** by design: a reviewer
that crashes catches no bugs, and silently shrinking the denominator
to the surviving subset would inflate the apparent catch rate
exactly when reviewers are flakiest.

`--uplift` and `--repeat > 1` are rejected with `--swe-bench`:
neither concept maps onto binary catch scoring without additional
design.

When a `--swe-bench` run with `--markdown <path>` is committed, the
generated "SWE-bench-lite catch rate" section appears in the
leaderboard markdown next to the F1 / uplift / overhead tables —
one report, two complementary signals. As of the current
`bench/RESULTS.md` snapshot, the SWE-bench section is **not yet
populated** (the harness ships; the section lands on the first
catch-rate run committed against a real dataset, alongside the
real-task curation tracked above).

## Updating the leaderboard

`bench/RESULTS.md` is the human-readable leaderboard. To refresh it
from the current dataset + fixtures:

```sh
local-review bench --replay bench/fixtures \
    --markdown bench/RESULTS.md \
    --out bench-results.json
```

Commit both files. Diffing `bench-results.json` between commits is
the cheapest way to see "did this prompt-pack change move anything?".

## Limitations to call out

* Phase 2 ships **10 cases**. Issue #56 targets 50–100; we add cases
  as real PRs surface them. Numbers from a 10-case bench are
  directional, not authoritative.
* Line-window matching has occasional false-positive matches when a
  reviewer happens to comment on an adjacent unrelated thing. This
  is a known cost of not requiring category match.
* Consistency / Jaccard is **only meaningful for live runs**. In
  replay mode the harness refuses `--repeat > 1` rather than
  silently shipping 1.0 to the leaderboard.
* We don't yet score the **merge** step — only individual agent
  reviews. Phase 3 candidate.
* The finding parser recognises a curated allow-list of
  extensionless filenames (`Dockerfile`, `Makefile`, `Rakefile`,
  `Procfile`, `Gemfile`, `Jenkinsfile`). When a new extensionless
  format becomes common (e.g. `Containerfile`, `BUCK`, `BUILD.bazel`)
  add it to `fileLineRE` in `internal/bench/parse.go` — broader
  extensionless-anywhere matching would mis-match phrases like
  "version: 0.42" in prose.
