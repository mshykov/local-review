# Security

Security model and rules for `local-review` — a local BYOK code reviewer that, by
design, handles untrusted input (the diffs and repositories it reviews).

## Secret & personal-data scanning

- A **pre-commit hook runs `gitleaks`**; CI enforces the same via
  `.github/workflows/secret-scan.yml`.
- The gitignored `.git-personal-denylist` backstops personal values (names,
  emails, IPs) that aren't secrets but still must not land in history.
- Never commit real secrets or personal data, even in fixtures or docs. Use
  neutral examples: RFC 5737 ranges (`192.0.2.x`, `198.51.100.x`),
  `test@example.com`, `ghp_example`.

## Untrusted repo config

The repo-level `.local-review.yml` is an **untrusted, attacker-controllable**
config layer — it ships inside any repo you review (a hostile commit in CI, a
fresh clone of someone else's code). Security-sensitive LLM fields are stripped
from untrusted layers before merge, each with a stderr warning:

- `cli_path` — would otherwise allow arbitrary command execution via
  `exec.LookPath` + `exec.CommandContext`.
- `base_url` — would otherwise point a provider agent at a hostile endpoint that
  receives the diff/source (exfiltration).
- `api_key` — secret-in-repo.
- `api_key_env` — would otherwise let a repo redirect which env var is read as an
  agent's credential, exfiltrating an arbitrary env var as that agent's auth token.
- `prompts.pack_dir` (absolute only) — `resolveRelativePaths` containment-checks
  only *relative* paths, so a checked-in absolute path would read
  `<dir>/<lang>.md` from an attacker-chosen location into every LLM prompt.
  Relative pack_dirs survive (resolved against the repo, symlink-safe-contained).
- `storage.base_path` — CWD/absolute-resolved by the storage layer, so an
  untrusted value directs `MkdirAll` + report writes (partially LLM-authored
  content) at an attacker-chosen directory.

The home-directory layer (`~/.local-review.yml`) is trusted; the repo layer is
not, unless `LOCAL_REVIEW_TRUST_REPO_CONFIG=1` is set. The house-rules fields
(model / timeout / enabled / prompts.prepend/append / review) still merge from
the repo layer — they are the advertised "Customise for your team" feature —
but they are **not** risk-free from a hostile repo: `prompts.prepend/append`
splice repo-authored text into every reviewer's *system prompt*, `review.exclude`
can glob away the entire diff (`"**"` → "No changes to review" → exit 0), and
`llms.*.enabled` can thin out or re-enable agents. When an untrusted layer sets
any of them, a `NOTE: repo config ... shapes this review` line on stderr gives
the operator a visible signal to inspect the file before trusting a clean result.

## Symlink-escape protection

`pathInsideDir` resolves symlinks (`EvalSymlinks`, with a deepest-existing-
ancestor walk-up) **before** the containment check, and fails closed on resolve
errors. A lexical-only check would admit symlink-escape paths
(e.g. `evil-link → /etc`).

## Logging & error hygiene

- Never log sensitive auth data — API keys, tokens, session material. Auth-miss
  errors name the *configured env var* (e.g. `api_key_env: OPENAI_API_KEY`), never the
  key value.
- Don't surface raw internal errors that might embed a secret; map to an actionable
  message instead.

## Handling untrusted input

The diff and source under review are attacker-controllable — treat them as data, not
instructions:

- Validate untrusted input at the boundary; reject malformed config rather than
  coercing it.
- Validate URL protocols on any user-supplied endpoint — only `http(s)`.
- Where a reviewer CLI exposes agentic tools (e.g. Copilot), it is invoked with tools
  disabled, so a prompt-injecting diff can't drive its shell / file / network tools.

## Supply chain

No vendor SDKs — raw HTTP only in `internal/llm/`, keeping the dependency surface (and
thus supply-chain risk) minimal. See [developer.md](developer.md). Dependencies are
kept current: Dependabot opens update PRs and `gitleaks` runs in CI on pull requests
and pushes to `main`.

- **CI/install tools go in `go.mod` as a `tool` directive, never `go install
  x@version`.** `go get -tool <module>@<version>` (Go 1.24+) pins the version AND
  checksum-verifies its full dependency graph in `go.sum` — the same integrity
  guarantee the main build gets. A bare `go install x@version` inside a workflow step
  has no lock file backing it; SonarCloud flags this (Security, "dependency versions
  are not predictable"). `go tool <name>` then builds-and-runs it from the module
  cache — no separate install step. See `.github/workflows/{govulncheck,secret-scan}.yml`
  for the pattern. Exception: a tool with a large dependency tree (e.g.
  `golangci-lint`, ~100 bundled linters) can cost more in `go.sum` bloat than it's
  worth as a `tool` directive — a SHA-pinned GitHub Action is the better call there
  (see `.github/workflows/ci.yml`'s "Complexity" step). Don't add `go mod tidy` to
  the "later" pile either — `go get -tool` alone doesn't produce a tidy-clean
  `go.sum` (it misses the tool's test-only transitive deps); run `go mod tidy` and
  commit its output in the same change.
- **Every `curl`/`wget` call that follows redirects must pin the protocol.**
  `curl -L` (implied by `-fsSL`) follows redirects; without `--proto '=https'
  --proto-redir '=https'`, a compromised or misconfigured server anywhere on the
  redirect chain could downgrade the request to plain `http` mid-flight — the
  `https://` in the *initial* URL doesn't protect against that on its own.
  SonarCloud flags any un-pinned `curl -fsSL` as a Security finding ("not enforcing
  HTTPS"). This applies to every occurrence of a URL, including ones a user is meant
  to copy-paste (a documented `curl ... | sh` onboarding command is the highest-value
  one to pin — it fetches executable code and pipes it straight into a shell). See
  `install.sh` and `.github/workflows/release.yml`'s `update-homebrew` job for the
  pattern; grep the repo for every `curl` when adding a new one so a fresh copy
  doesn't miss the flags.
