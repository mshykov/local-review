# local-review benchmark suite

This directory holds the **review-quality benchmark** introduced in
issue #56. It gives the project a reproducible signal — precision,
recall, F1, noise rate — for prompt and model changes, instead of
shipping releases on vibes.

This is **Phase 1**: a small hand-curated dataset, an in-CLI scorer,
and a deterministic replay mode for CI. Phase 2 grows the dataset and
adds consistency runs; Phase 3 adds cross-tool comparisons (CodeRabbit,
Copilot review, etc.) and an LLM-as-judge backstop. See the issue for
the long-term roadmap.

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

## Limitations to call out

* The dataset is small (Phase 1 ships ~4 cases). Numbers are
  illustrative, not authoritative.
* Line-window matching has occasional false-positive matches when a
  reviewer happens to comment on an adjacent unrelated thing. This
  is a known cost of not requiring category match.
* We don't yet score the **merge** step — only individual agent
  reviews. Phase 3 candidate.
* The finding parser recognises a curated allow-list of
  extensionless filenames (`Dockerfile`, `Makefile`, `Rakefile`,
  `Procfile`, `Gemfile`, `Jenkinsfile`). When a new extensionless
  format becomes common (e.g. `Containerfile`, `BUCK`, `BUILD.bazel`)
  add it to `fileLineRE` in `internal/bench/parse.go` — broader
  extensionless-anywhere matching would mis-match phrases like
  "version: 0.42" in prose.
