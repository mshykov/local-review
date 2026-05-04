# Release process

How `local-review` ships releases. Read this before merging anything you want tagged.

## TL;DR

A release happens when a PR with the **`release`** label merges to `main`. The bump type is read from `major` / `minor` / `patch` labels on that same PR (default: `patch`). One GitHub Actions workflow does everything end-to-end:

```
prepare → build (5-platform matrix) → publish → update homebrew tap
```

No manual tagging, no chained workflows.

## Versioning

We use [semantic versioning](https://semver.org/): `vMAJOR.MINOR.PATCH`.

- **MAJOR** — breaking changes (CLI flags removed, config schema rewrite).
- **MINOR** — new user-visible feature, backward compatible.
- **PATCH** — bug fix, doc/config update, dep bump.

While we're pre-1.0, "minor" doesn't promise stability — anything can break. The version number is still a public momentum signal, so don't bump it casually.

## Label cheat sheet

Apply these labels to a PR **only when you actually want a release on merge**.

| Label | Use it for | Don't use it for |
|---|---|---|
| `release` | Mandatory on any PR you want to ship | Doc-only PRs, refactors, internal tweaks |
| `major` | Breaking CLI flag changes, removed commands, config schema rewrites | Anything that doesn't break existing user setups |
| `minor` | A new prompt pack, a new command, a new provider in core, a new flag | Bug fixes, polish, internal refactors |
| `patch` | Bug fixes, doc/config updates, dependency bumps, test additions, or making the default patch bump explicit | New user-visible functionality |

### Common mistakes to avoid

- ❌ **`minor` for "I want to ship this, the change feels notable"** — bumps the published version more than necessary. v0.1.1 → v0.2.0 implies a feature; if the PR is "fix typo in error message," use `patch`.
- ❌ **`release` on every merged PR** — ships a tag for every doc/CI/refactor PR. Wastes version numbers and inflates release frequency. Only label `release` when you specifically want to publish.
- ❌ **No bump label, just `release`, when you intended a feature or breaking release** — defaults to `patch`. Add `minor` or `major` when you need something other than the default patch bump.

## Pipeline

The pipeline lives in [`.github/workflows/release.yml`](../.github/workflows/release.yml). It runs four jobs in order:

1. **prepare** — gates on the `release` label, computes the next version from the bump label (or accepts an explicit version via `workflow_dispatch`).
2. **build** — 5-platform matrix (darwin amd64/arm64, linux amd64/arm64, windows amd64). Each matrix job builds a `local-review` binary, packages it as `.tar.gz` (or `.zip` on Windows), and uploads as an artifact.
3. **publish** — downloads all artifacts, creates the git tag, creates a GitHub Release with auto-generated notes, attaches the binaries.
4. **update-homebrew** — checks out the `mshykov/homebrew-tap` repo, downloads the released `.tar.gz`s, computes SHA256s, updates `Formula/local-review.rb`, commits + pushes.

Why one combined workflow: GitHub doesn't let `GITHUB_TOKEN`-pushed events trigger downstream workflows (anti-loop safety). Splitting tag/build/publish across multiple workflows breaks that chain. Doing it all in one run avoids the issue without needing a PAT.

### Required secrets

- `TAP_GITHUB_TOKEN` — Personal Access Token with `repo` scope, used by the `update-homebrew` job to push to `mshykov/homebrew-tap`. Without this, the formula doesn't update — the rest of the release still works.

## Shipping a release

### Normal path: a PR with labels

1. Open a PR with the changes plus a "Release vX.Y.Z" line in `CHANGELOG.md`.
2. Apply labels: `release` + one of `major` / `minor` / `patch`.
3. Get it merged.
4. The workflow auto-tags, builds binaries, publishes the GitHub Release, and updates the Homebrew formula.
5. Verify:
   - https://github.com/mshykov/local-review/releases — new tag and binaries
   - https://github.com/mshykov/homebrew-tap/blob/main/Formula/local-review.rb — version + SHAs updated
   - `brew upgrade mshykov/tap/local-review` — new version installs

### Manual / ad-hoc path

If you need to ship a specific version without a labeled PR (e.g. emergency, or tagging a specific commit), trigger the workflow manually:

```sh
gh workflow run release.yml -f version=v0.4.2
```

The version must match `^v[0-9]+\.[0-9]+\.[0-9]+$` (validated by the workflow). Everything else (build, publish, Homebrew update) runs the same way.

## Local verification before tagging

```sh
# Build with explicit version so `local-review version` reports correctly
go build -ldflags "-X main.version=v0.4.0" -o local-review ./cmd/local-review
./local-review version
# Should output: local-review v0.4.0

# Run tests
go test -race ./...

# Smoke-test against a real diff
./local-review staged
```

## Troubleshooting

- **No release fired after merge**: check the PR had the `release` label. Doc-only / `paths-ignore` matches (`docs/**`, `**.md`, `examples/**`) skip the workflow entirely; that's intentional.
- **Tag created but no binaries**: check the `build` matrix in the workflow run. Most likely a Go compile error on one of the targets.
- **Homebrew formula didn't update**: check `TAP_GITHUB_TOKEN` is set in repo secrets and the token isn't expired.
- **Wrong version bumped**: see the post-mortem note in the [v0.3.0 changelog entry](../CHANGELOG.md). Don't try to revert public tags; it's destructive and the version waste is purely cosmetic in 0.x.
