# SWE-bench-lite catch-rate dataset

A [SWE-bench-lite](https://www.swebench.com/lite.html)-shaped dataset adapted
into the local-review bench format. **Day-1 ships synthetic examples** in the
upstream task shape; real-task curation from the upstream dataset is in
progress (target N=10 before v0.10.0 ships — see "Current status" below).
Each task ships a **bug-introducing diff** (the SWE-bench fix patch, reverse-
applied) plus a list of keyword phrases the reviewer's findings should mention
to count as a "catch."

Headline question this dataset answers: **does local-review actually catch
real bugs in real codebases that we did not author?** The original
`bench/dataset/` is hand-labelled by us; the SWE-bench tasks come from
production issues filed against well-known Python projects (django, requests,
sympy, …). Catch rates on this dataset are the closest thing to an external
credibility signal we have.

## Scoring (v1)

Binary `caught` / `missed` per `(task, LLM)` pair, by **case-insensitive
substring match** between the LLM's review markdown and any phrase in the
task's `expected_keywords`. Strict on purpose:

- If a finding mentions the bug class verbatim (e.g. "off-by-one"), it
  counts.
- If a finding only flags the file or function but doesn't describe the
  underlying bug, it does NOT count.

False positives are NOT a concern here — the bench's existing
labelled-dataset scoring covers noise/precision. SWE-bench mode asks one
question: did the reviewer surface the real bug? Adjacent findings are fine.

Future versions may add an LLM-as-judge tier for partial credit (currently
out of scope; tracked for a later release).

## Layout

```text
bench/swe-bench-lite/
├── README.md                    # this file
├── <task-id>/
│   ├── case.yaml                # task metadata + expected_keywords
│   └── diff.patch               # bug-introducing diff (reverse of the SWE-bench fix patch)
└── ...
```

## `case.yaml` schema

```yaml
id: <stable-id>                  # also the directory name; must match [A-Za-z0-9_-][A-Za-z0-9._-]*
source: <upstream-reference>     # e.g. "princeton-nlp/SWE-bench-lite#django__django-12345"
language: <lang-id>              # "python", "javascript", … (matches internal/lang ids)
title: <one-line summary>
description: |                   # optional longer narrative
  ...
expected_keywords:               # case-insensitive substring matches; ANY = caught
  - "off-by-one"
  - "range end is exclusive"
  - "missing last item"
expected_files:                  # optional; not yet used by scoring (reserved for partial credit)
  - <path/in/repo>
```

## Adding a new task

1. Pick a SWE-bench-lite task you want to include. Note the repo, base
   commit, and fix patch.
2. Reverse-apply the fix patch to produce the bug-introducing diff (the
   diff a developer would have written to introduce the bug). Save as
   `<task-id>/diff.patch`.
3. Write `<task-id>/case.yaml` with the task metadata and 3–6
   `expected_keywords` derived from the original issue's problem statement.
   Pick phrases distinctive enough that an unrelated finding won't trigger
   them by accident, but generic enough that a reasonable LLM finding
   *would* mention them.
4. Run `local-review bench --swe-bench --only claude` against your task
   and verify the catch / miss assessment matches what you'd expect.

## Current status: example tasks only

The day-1 dataset ships **synthetic examples in real SWE-bench-lite shape**
— they exercise the loader, scorer, and reporter, but they are NOT
extracted from the upstream SWE-bench-lite dataset yet. Real task curation
(target: N=10 from the upstream dataset) is tracked as a follow-up
commit before the v0.10.0 release.
