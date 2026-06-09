> Baseline: `MSH/docs/security.md` (org common rules). Below: local-review-specific rules.

# Security

local-review-specific security rules. The org baseline covers general secret
hygiene; this file holds the threat-model specifics of this BYOK code reviewer.

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

The home-directory layer (`~/.local-review.yml`) is trusted; the repo layer is
not, unless `LOCAL_REVIEW_TRUST_REPO_CONFIG=1` is set. Non-sensitive fields
(model / timeout / enabled / prompts / review) still merge from the repo layer.

## Symlink-escape protection

`pathInsideDir` resolves symlinks (`EvalSymlinks`, with a deepest-existing-
ancestor walk-up) **before** the containment check, and fails closed on resolve
errors. A lexical-only check would admit symlink-escape paths
(e.g. `evil-link → /etc`).

## Supply chain

No vendor SDKs — raw HTTP only in `internal/llm/`, keeping the dependency
surface (and thus supply-chain risk) minimal. See [developer.md](developer.md).
