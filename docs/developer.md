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

- **Go 1.26+**, `git` in PATH.
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

If the `claude` CLI itself can't run the self-review in your environment (e.g. a
sandboxed or nested-agent context where the CLI errors out), fall back to the
full `go test -race ./...` + `-tags e2e` suite, CI, and CodeRabbit — and **say so
in the PR** so reviewers know the dogfood pass was skipped and why. The safety net
for danger-zone changes is environment-dependent; don't pretend it ran.

## Definition of done

- Builds clean (`go build ./...`), `gofmt -s` + `go vet` clean, `go test -race ./...`
  passes.
- No secrets or personal data committed (see [security.md](security.md)).
- Docs and comments updated in the same change as the behavior they describe.
- If you skipped a test, deferred a check, or made an assumption — **say so** in the
  PR description. "Done except X" beats "done" with a silent gap.

## Lessons learned (workflow & tooling)

Distilled from a multi-PR tech-debt + migration sweep. These are the practices that
paid off and the traps that cost time — read them before a big refactor or a
CI/infra change.

### Keep doing

- **One purpose per PR.** A large effort (docs fix, CI tooling, two package
  extractions, an orchestrator decomposition) split into separate focused PRs each
  merges clean, reviews fast, and reverts surgically. Bundling would have made every
  one of those harder.
- **Verify before you push, every time.** `gofmt -s` + `go vet` + `go test -race
  ./...` + `go test -tags e2e ./e2e/...` locally. CI is the backstop, not the first
  signal — finding a break after a 2-minute CI cycle is the slow path.
- **Behavior-preserving refactors stay behavior-preserving.** Move the tests *with*
  the code, carry every invariant-documenting comment across, and lean on the
  existing suite as the safety net. A pure relocation changes call sites and file
  layout — never logic. When a function only reads one field of a struct param
  (`sf.only`), pass the field, not the struct — it decouples the extract-ee cleanly.
- **Quantify refactor claims with a tool; don't assert them.** `go run
  github.com/uudashr/gocognit/cmd/gocognit@latest -over 15 <file>` confirmed the
  orchestrator dropped from cognitive-complexity 41 → 15. "It's simpler now" is not
  evidence.
- **Diagnose CI failures from the source, not by guessing.** The SonarCloud API
  (`/api/qualitygates/project_status`, `/api/duplications/show`,
  `/api/issues/search`) pinpoints the exact failing condition, file, and line in
  seconds — far faster than re-reading the dashboard or re-pushing speculative fixes.
- **Order outward-facing migrations so nothing breaks mid-flight.** For the
  custom-domain move: add the DNS record → verify it resolves → *then* merge the
  `CNAME` file. The reverse leaves the site unreachable while DNS propagates.

### Watch out for (cost us time this round)

- **SonarCloud Automatic Analysis doesn't reliably re-analyze PR pushes.** PR-level
  gate "failures" were stale analyses of an *earlier* commit; the identical code was
  green on `main` after merge. Fixed by switching Sonar to CI-based analysis on
  `pull_request` (`.github/workflows/sonar.yml`) and disabling Automatic Analysis in
  the project settings (the two are mutually exclusive) — PR pushes with access to
  `SONAR_TOKEN` now get a fresh gate computed from their exact commit. Fork and
  Dependabot PRs skip the scan because that secret is not exposed to those PR
  contexts. Note: Sonar is still not a *required* check, so a red gate surfaces a
  finding without blocking merge.
- **Extraction relocates coverage; it does not create it.** Moving already-tested
  logic out of `cmd` *lowers* `cmd`'s coverage % — numerator and denominator both
  leave. The real win is a smaller danger zone with the logic in a cohesive, tested
  package. Frame it that way; only "raise package X's coverage" by adding *new* tests
  for *untested* code (the IO orchestrator, not the pure helpers).
- **Table-driven Go tests trip Sonar's copy-paste detector.** Repeated
  `{name, input, want}` case structs read as duplication on new code. `*_test.go` is
  exempt in `sonar-project.properties` (same rationale as the `init.go` provider
  table and the S3776 test-file exemption) — expect a new table test to need this;
  don't contort it to satisfy CPD.
- **A full cognitive-complexity sweep is worth it once `.golangci.yml` exists to keep
  it clean.** An earlier pass here decomposed only the worst offender and left ~20
  functions over budget as "existing-code, doesn't fail the new-code gate" — true at
  the time, but it meant the debt just sat there until each function was touched for
  an unrelated reason, at which point the SAME extraction had to happen anyway, under
  more time pressure. A later sweep fixed all 22 (mechanical extraction, no logic
  changes, one PR per risk tier — danger zone / internal source / test files) and
  added the `gocognit` CI gate below so the count stays at zero going forward instead
  of slowly regrowing. Do the one-time sweep; don't leave a "mostly fixed" pile for
  the next person.
- **`golangci-lint` with only `gocognit` enabled closes the gap SonarCloud's own
  new-code-only gate leaves.** `.github/workflows/ci.yml`'s "Complexity" step runs it
  on every push/PR against the WHOLE repo, not just the diff — a regression is caught
  at the PR that introduces it, not months later when SonarCloud finally flags it
  because someone touched an adjacent line. `.golangci.yml` deliberately enables only
  `gocognit` (not `default: standard` or any broader preset) — a wave of unrelated
  new findings on an unrelated PR is exactly the kind of noise that erodes trust in a
  gate. It also exempts `*_test.go`, mirroring `sonar-project.properties`'s own S3776
  exemption for tests (see the note above) — local tooling and Sonar's gate should
  agree, not fight each other.
- **Extracting an untested function's internals turns "always was uncovered" into
  "new code at 0%" in a diff-based coverage gate.** SonarCloud's new-code-coverage
  gate (≥80%) failed twice during the complexity sweep — not because the refactor
  broke anything, but because splitting a function that had ZERO direct test
  coverage (e.g. `runDoctor`, `Walk`) into named helpers puts those helpers' bodies
  into the PR's diff, and a diff-coverage tool can't see that the parent was already
  just as untested on `main`. The fix is to add real tests for the newly-legible
  pieces (which is a genuine improvement, not gate-satisfying theater) — write the
  characterization tests against the CURRENT code first, confirm they pass, then
  refactor, then confirm they still pass unchanged. Check coverage per extracted
  function (`go test -coverprofile=... && go tool cover -func=...`) before pushing,
  not after Sonar tells you.
- **Branch before you edit.** Several refactors started with edits on `main` and had
  to be moved onto a feature branch (`git checkout -b` carries the working tree, so
  it's recoverable) — but starting on the branch is one less thing to get wrong.
- **`go get -tool <module>@<version>` alone doesn't produce a `go mod tidy`-clean
  `go.sum`.** Adding `govulncheck` / `gitleaks` as `tool` directives (closing the
  Sonar "unpredictable dependency version" finding on `go install x@version` in CI)
  passed `go build` / `go vet` / `go test` locally but still failed CI's `go mod tidy
  && git diff --exit-code go.mod go.sum` step — `go mod tidy` pulls in a tool's
  test-only transitive dependencies too, which `go get -tool` alone doesn't. Always
  run `go mod tidy` (not just build/vet/test) after adding or bumping a `tool`
  directive, and commit whatever it changes, before pushing.
- **A tool with a large dependency tree can bloat `go.sum` far more than the value it
  adds as a `tool` directive.** `golangci-lint` alone would have added ~650 lines to
  `go.sum` (it bundles ~100 linters) for a dev-only CI tool — a real cost against
  "no vendor SDKs... keeps the dependency surface... minimal" even though it's a
  `tool`, not a runtime dependency. Used the SHA-pinned `golangci-lint-action`
  instead: still fully reproducible (a real commit SHA, not a floating tag) without
  the `go.sum` cost. `go.mod` tool directives are the right call for a single, small
  binary (govulncheck, gitleaks); a SHA-pinned Action is the right call for a large
  one. Weigh both before defaulting to "no vendor SDK, so always `go tool`."
