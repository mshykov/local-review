# local-review audit dogfood

This directory holds the **deep-codebase audit reports** produced by
running `local-review audit` against `local-review`'s own committed
source tree. It is the trust artifact for the `audit` subcommand
(shipped in v0.10.0) the same way [`bench/RESULTS.md`](../bench/RESULTS.md)
is the trust artifact for `review`.

Two questions this directory answers:

1. **Does `audit` produce real signal on a real codebase?** If the
   reports here are noise or hallucinated, you should not trust the
   tool. We commit the raw output so you can read it before
   adopting.
2. **What does the project actually owe the world right now?** The
   reports double as a public defect list. Findings here are
   prioritised work, not aspirational backlog.

Two topics ship in v0.10.0:

- [`security.md`](security.md) — OWASP-aligned sweep for
  vulnerabilities, hardcoded secrets, missing authorization, weak
  crypto, injection surfaces, path traversal.
- [`tech-debt.md`](tech-debt.md) — dead code, duplicated logic,
  leaky abstractions, inconsistent error handling, architectural
  smells.

## Methodology

Both reports are produced by:

```sh
local-review audit --topic <security|tech-debt> --out audit/<topic>.md
```

Run against the `main` branch at the v0.10.0 tag. Single-LLM by
design (Claude); audit cost is per-package × per-topic and
multi-LLM would multiply spend without obvious quality return at
this scale — see CHANGELOG entry for v0.10.0 for the reasoning.

Under the hood:

- `git ls-files` walks the **committed** source tree. Working-tree-only
  edits don't appear in findings until they're committed.
- Source is grouped **by directory** into one chunk per package, then
  any package whose total file size exceeds the per-chunk cap is
  greedily bin-packed into `pkg [part N/M]` sub-chunks. Per-file
  adjacency is preserved across the split (files A, B, C, D become
  [A,B] + [C,D], not [A,C] + [B,D]) so the LLM can still cross-reference
  within a package.
- Each chunk goes to the LLM with the topic-specific audit pack
  (`internal/prompts/audit/<topic>.md`) as the system prompt. The
  audit packs deliberately suppress the `nit` severity tier —
  whole-codebase reading produces enough signal that nits dilute the
  report.
- Findings are merged into one report per topic. The renderer emits
  markdown by default; `--out foo.json` switches to JSON.

## Reading a report

Findings are grouped by package, severity-sorted within each group
(`critical` → `major` → `warning` → `info`). Each finding carries:

- **File + line** — where the issue lives (or `0` for
  package-level / cross-file findings).
- **Severity** — calibrated to the same scale `review` uses, so
  these reports are directly comparable to diff-time reviews.
- **Note** — what the LLM thinks is wrong and why.
- **Suggestion** (when present) — the LLM's recommended fix or
  next step.

Packages with no findings appear as a one-liner under the
**"Clean packages"** section. Chunks that errored (LLM refused,
timed out, returned malformed output) appear under an
**"Errored chunks"** section with the error string — error frames
DO count, the same way bench errors count: a reviewer that crashes
on your package is not "no findings."

## Triaging the reports

Treat these reports the same way you'd treat any LLM-generated
finding list:

1. **Read top-to-bottom once.** Get a sense of the noise floor on
   this specific codebase. False-positive rate varies a lot by
   topic and language.
2. **Cluster duplicate findings.** The LLM often re-flags the same
   issue from multiple angles within a chunk. One real bug, three
   bullets.
3. **Cross-reference with the diff-time `review`** for files you've
   recently touched. If `review` was clean on a file but `audit`
   flags it, the audit is reporting a pre-existing issue that no
   diff would have surfaced — the audit's reason for existing.
4. **File or fix.** Findings worth keeping become issues or PRs;
   the rest get noted in your own triage doc. Don't delete this
   file's contents to "clean up the report" — the next regen will
   bring them back, and the trust signal lives in seeing what the
   tool actually said.

## Regenerating

Every release that touches `audit` packs, the audit walker, or the
prompt-pack discovery mechanism should regenerate both reports as
part of the release PR. The commands are above. Expect ~20-30 min
per topic on a codebase this size against an authenticated Claude
CLI.

**Cadence in practice.** Patch releases that only refactor code
under existing review-path packages (the v0.10.2 / v0.10.5 / v0.10.6
shape) do NOT regenerate; the trust artifact stays accurate because
no new code surface was added. Regen on minor releases or whenever
the audit walker / chunker / prompts themselves change — that's the
case where stale reports would be misleading.

**Pre-flight probe note** (v0.10.6+). When `audit` runs against an
LLM, it goes through the same pre-flight readiness probe the diff-
review path uses. If the LLM is unavailable, you'll see a vendor-
specific error (e.g. `gemini ✗ timeout after 10s — Error: You have
exhausted your capacity on this model.`) before any chunks are
processed — so a half-finished audit run won't burn tokens on
chunks that were going to fail anyway. `audit` picks the first
authenticated LLM, so this almost always means "claude failed
the probe, audit can't proceed, here's why." Run `local-review
doctor` to investigate.

If you fork this project and want to publish your own reports
here, replace these files with the output of `local-review audit`
on your fork; the methodology section above stays accurate.
