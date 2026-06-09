# Developer

Engineering conventions for contributing to `local-review` — a single-binary Go
CLI. Read this top to bottom before your first change.

## General engineering principles

These apply to every change, and they're what the project's own reviewer (and human
reviewers) hold contributions to:

- **Surgical changes only.** Touch what the task requires — no drive-by
  formatting, comment polish, or "while I'm here" refactors. Spotted something else
  worth fixing? Surface it separately.
- **Read before you write.** Before changing a shared helper, contract, or
  user-visible string, grep **all** callers in the same pass — sibling code, doc
  comments, README/CHANGELOG, prompt templates, tests. Cross-file drift is the most
  common review finding here.
- **Keep comments and doc strings current.** A comment describing behavior you just
  changed is a bug — update it in the same edit. Comment the *why* (intent,
  constraints, trade-offs), never restate the *what*. One-line doc comments on
  exported symbols only.
- **Fail loud, fail closed.** Refuse invalid input rather than silently passing it
  through; check error / exit codes explicitly; `TrimSpace` before emptiness checks.
  "Completed" is wrong if anything was skipped silently.
- **Match the codebase's conventions even if you disagree.** Conformance > taste. If
  a convention seems harmful, raise it in the PR — don't fork silently.
- **Surface conflicts; don't average them.** When two patterns contradict, pick the
  more recent / more tested one, explain why, and flag the other for cleanup.
- **Writing a v2 next to a v1?** Enumerate v1's invariants explicitly and re-exercise
  each in v2 — filters, caps, output formats, glob / edge behavior don't carry over
  for free.

## Toolchain & basics

- **Go 1.23+**, `git` in PATH.
- `gofmt -s` and `go vet` must be clean before pushing.
- New logic needs a unit test — see [testing.md](testing.md).

## Hard architecture constraints

These are non-negotiable; a change that violates one will be rejected in review.

- **No vendor SDKs in `internal/llm/` — raw HTTP only.** Keeps the binary small
  and removes the supply-chain attack surface of pulling in provider SDKs. See
  [security.md](security.md).
- **No telemetry.** Privacy first; the tool phones home to nothing.
- **Git CLI integration only — never go-git.** All git access shells out to the
  `git` binary (`internal/git/`).
- **Exit codes:**
  - `0` = no blocking findings.
  - `2` = `major` or `critical` findings present — this is the pre-commit gate.
  - other non-zero = tool failure (hooks ignore so commits still go through).

## Danger zone

`cmd/local-review/runner.go` and `cmd/local-review/doctor.go` historically
account for ~40% of all reviewer findings. They're orchestration files where
multiple concepts meet. Apply extra care: read before you write, and grep all
callers of any shared helper or contract you touch.

## Pre-push self-review (mandatory)

Before pushing any branch with code changes, run:

```sh
./local-review review main        # multi-LLM
./local-review review main --only claude   # if multi-LLM is too slow
```

Address every `major` / `critical` finding before pushing. We eat our own dog
food — if the tool produces noise on this codebase, that's a bug to file or fix.

Skip the self-review **only** for pure docs / website / trivial-config changes.

## Definition of done

- Builds clean (`go build ./...`), `gofmt -s` + `go vet` clean, `go test -race ./...`
  passes.
- No secrets or personal data committed (see [security.md](security.md)).
- Docs and comments updated in the same change as the behavior they describe.
- If you skipped a test, deferred a check, or made an assumption — **say so** in the
  PR description. "Done except X" beats "done" with a silent gap.
