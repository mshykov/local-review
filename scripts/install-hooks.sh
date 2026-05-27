#!/usr/bin/env bash
# Installs a local pre-commit hook that blocks committing secrets and
# personal data. Run once after cloning:  ./scripts/install-hooks.sh
#
# The hook does two things on every `git commit`:
#   1. gitleaks scan of the STAGED diff (secrets: tokens, keys,
#      passwords, private keys). Honors .gitleaks.toml.
#   2. greps the STAGED CONTENT of changed files against a GITIGNORED
#      personal denylist (.git-personal-denylist) — your own IPs /
#      hostnames / emails / names that must never reach a public repo.
#      The denylist stays on your machine (it's gitignored), so listing
#      your real values is safe.
#
# CI (.github/workflows/secret-scan.yml) enforces the gitleaks half
# regardless; this hook is the fast local backstop that also covers the
# personal-data half CI can't (CI is public, it can't hold your denylist).
#
# If a pre-commit hook already exists, it is preserved as
# `pre-commit.local` and CHAINED (run first) so other safeguards
# (formatters, linters) keep working.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
hooks_dir="$(git rev-parse --git-path hooks)"   # worktree-safe
hook="$hooks_dir/pre-commit"
chained="$hooks_dir/pre-commit.local"

mkdir -p "$hooks_dir"

if [ -e "$hook" ] && ! grep -q "local-review secret/PII pre-commit" "$hook" 2>/dev/null; then
  if [ -e "$chained" ]; then
    # Ambiguous: a foreign pre-commit AND a pre-commit.local both exist.
    # We can't chain a second one without clobbering the first, so
    # fail-closed and let the human resolve it — never silently disable
    # an existing hook.
    echo "✗ Both a non-local-review pre-commit hook and $chained already exist." >&2
    echo "  Refusing to overwrite (that would disable one of them). Resolve manually:" >&2
    echo "    - merge the logic you want into $chained, then re-run this installer, or" >&2
    echo "    - remove whichever hook is stale." >&2
    exit 1
  fi
  mv "$hook" "$chained"
  chmod +x "$chained" 2>/dev/null || true
  echo "Existing pre-commit hook preserved as $chained (it will be run first / chained)."
fi

cat > "$hook" <<'HOOK'
#!/usr/bin/env bash
# local-review secret/PII pre-commit  (installed by scripts/install-hooks.sh)
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
self_dir="$(cd "$(dirname "$0")" && pwd)"
fail=0

# 0. Chain a previously-installed hook, if any (run it first).
if [ -x "$self_dir/pre-commit.local" ]; then
  "$self_dir/pre-commit.local" "$@" || exit $?
fi

# 1. Secrets — gitleaks on the staged diff.
if command -v gitleaks >/dev/null 2>&1; then
  if ! gitleaks git --staged --config "$repo_root/.gitleaks.toml" --redact --no-banner --exit-code 1; then
    echo "✗ gitleaks found a secret in your staged changes (see above). Commit blocked."
    fail=1
  fi
else
  echo "⚠ gitleaks not installed — skipping the secret scan locally."
  echo "  Install: go install github.com/zricethezav/gitleaks/v8@v8.30.1   (or: brew install gitleaks)"
  echo "  CI will still enforce it, but you'll only find out after pushing."
fi

# 2. Personal data — grep the STAGED CONTENT (not the raw diff) of
#    added/modified files against your gitignored denylist. Scanning the
#    staged blob, not the patch, means a commit that REMOVES a leaked
#    value isn't blocked by the removed (`-`) line — you can clean up.
denylist="$repo_root/.git-personal-denylist"
if [ -f "$denylist" ]; then
  staged_content=""
  while IFS= read -r -d '' f; do
    staged_content+="$(git show ":$f" 2>/dev/null || true)"$'\n'
  done < <(git diff --cached --name-only --diff-filter=ACMR -z)

  while IFS= read -r raw || [ -n "$raw" ]; do
    line="${raw%%#*}"                                        # strip trailing comment
    line="$(printf '%s' "$line" | sed 's/[[:space:]]*$//')"  # rstrip
    [ -z "$line" ] && continue
    if printf '%s' "$staged_content" | grep -F -q -- "$line"; then
      echo "✗ staged changes contain a denylisted personal value: '$line'"
      echo "  (from .git-personal-denylist) — use a neutral example instead. Commit blocked."
      fail=1
    fi
  done < "$denylist"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "Commit aborted. Fix the above, re-stage, and commit again."
  echo "To bypass in a genuine emergency: git commit --no-verify  (NOT recommended)."
  exit 1
fi
HOOK

chmod +x "$hook"
echo "✓ Installed pre-commit hook → $hook"

# Seed the personal denylist from the template on first install.
if [ ! -f "$repo_root/.git-personal-denylist" ] && [ -f "$repo_root/.git-personal-denylist.example" ]; then
  cp "$repo_root/.git-personal-denylist.example" "$repo_root/.git-personal-denylist"
  echo "✓ Created .git-personal-denylist (gitignored) — add your own IPs / emails / names to it."
fi

if ! command -v gitleaks >/dev/null 2>&1; then
  echo
  echo "NOTE: gitleaks isn't installed. For the secret-scan half of the hook:"
  echo "  go install github.com/zricethezav/gitleaks/v8@v8.30.1   (or: brew install gitleaks)"
fi
