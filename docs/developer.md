> Baseline: `MSH/docs/developer.md` (org common rules). Below: local-review-specific rules.

# Developer

local-review-specific engineering rules. The org baseline covers the general
conventions; this file holds what's particular to this Go CLI.

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
