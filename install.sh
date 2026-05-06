#!/usr/bin/env sh
# local-review installer — downloads the right pre-built binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/mshykov/local-review/main/install.sh | VERSION=v0.1.0 sh
#
# Override the install dir with INSTALL_DIR (default: ~/.local/bin).
set -eu

REPO="mshykov/local-review"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# ----- Detect os/arch in Go release naming -------------------------------
uname_s=$(uname -s)
uname_m=$(uname -m)

case "$uname_s" in
  Darwin) os="darwin" ;;
  Linux)  os="linux" ;;
  *) echo "unsupported OS: $uname_s" >&2; exit 1 ;;
esac

case "$uname_m" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $uname_m" >&2; exit 1 ;;
esac

# ----- Resolve version ---------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o '"tag_name": *"[^"]*"' \
    | head -n1 \
    | sed -E 's/.*"([^"]+)"/\1/')
  if [ -z "$VERSION" ]; then
    echo "failed to resolve latest version" >&2
    exit 1
  fi
fi

# ----- Download + verify + install ---------------------------------------
asset="local-review_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
checksums="local-review_${VERSION}_checksums.txt"
checksums_url="https://github.com/${REPO}/releases/download/${VERSION}/${checksums}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${url}"
curl -fsSL "$url" -o "${tmp}/${asset}"

# SHA-256 verification before extract. The checksums manifest ships
# alongside the tarball in every v0.6+ release; for older versions
# (no manifest) we fall through with a loud warning so the user can
# still install but knows verification didn't run.
echo "Downloading ${checksums_url}"
if curl -fsSL "$checksums_url" -o "${tmp}/${checksums}" 2>/dev/null; then
  cd "$tmp"
  # Extract the manifest line for this exact asset before piping. POSIX
  # sh has no `pipefail`, so `grep <asset> | sha256sum -c -` would have
  # exited 0 even when grep matched nothing — sha256sum -c on empty
  # stdin returns 0, so a malformed / wrong manifest would skip
  # verification and proceed to extract a possibly-tampered tarball.
  # Capturing first lets us fail loud when the asset isn't listed.
  #
  # awk does an exact last-field equality match. Using `grep` here was
  # subtly broken: BRE treats the literal `.` characters in `.tar.gz`
  # as "any character," so a manifest line for `local-reviewxtar.gz`
  # could match `local-review.tar.gz` and slip a wrong-asset hash past
  # the checksum step.
  manifest_line=$(awk -v a="$asset" '$NF == a' "$checksums")
  if [ -z "$manifest_line" ]; then
    echo "❌ ${asset} not listed in ${checksums}" >&2
    echo "   refusing to install — manifest is malformed or built for a different asset set" >&2
    exit 1
  fi
  # Use `printf '%s\n'` rather than `echo` to feed the verifier:
  # POSIX `echo` semantics differ across shells (some interpret backslash
  # escapes by default, busybox's accepts -e/-n flags), so a manifest
  # line with backslashes or one starting with `-` could be mangled or
  # consumed as flags. printf '%s\n' is uniformly literal.
  if command -v sha256sum >/dev/null 2>&1; then
    # GNU coreutils (Linux, Homebrew on macOS).
    if ! printf '%s\n' "$manifest_line" | sha256sum -c -; then
      echo "❌ checksum mismatch for ${asset}" >&2
      echo "   refusing to install — possible tampering or corrupted download" >&2
      exit 1
    fi
  elif command -v shasum >/dev/null 2>&1; then
    # macOS default: shasum -a 256 -c reads <hash>  <file> lines.
    if ! printf '%s\n' "$manifest_line" | shasum -a 256 -c -; then
      echo "❌ checksum mismatch for ${asset}" >&2
      echo "   refusing to install — possible tampering or corrupted download" >&2
      exit 1
    fi
  else
    echo "⚠️  no sha256sum / shasum binary found — skipping integrity check" >&2
    echo "   install one of: coreutils (sha256sum) or perl (shasum) for tamper resistance" >&2
  fi
else
  # Manifest fetch failed. Pre-fix we always fell through to install-
  # without-verification with just a warning, so any release packaging
  # error or transient network issue silently turned into an
  # unverified install. Default to fail-loud; let users opt out
  # explicitly only when they know they're installing a legacy
  # release that genuinely doesn't ship a manifest.
  if [ "${INSTALL_REVIEW_SKIP_VERIFICATION:-}" = "1" ]; then
    echo "⚠️  ${checksums} unavailable for ${VERSION} — skipping integrity check" >&2
    echo "   (INSTALL_REVIEW_SKIP_VERIFICATION=1 — only safe for releases <v0.6.0)" >&2
    cd "$tmp"
  else
    echo "❌ failed to fetch ${checksums_url}" >&2
    echo "   refusing to install — the checksum manifest may be unavailable due to a network issue or a release packaging error" >&2
    echo "   if you're installing a release older than v0.6.0 (which doesn't ship a manifest), re-run with:" >&2
    echo "     curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | INSTALL_REVIEW_SKIP_VERIFICATION=1 sh" >&2
    exit 1
  fi
fi

tar -xzf "$asset"

mkdir -p "$INSTALL_DIR"
mv local-review "$INSTALL_DIR/local-review"
chmod +x "$INSTALL_DIR/local-review"

echo
echo "Installed local-review ${VERSION} to ${INSTALL_DIR}/local-review"
echo

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    shell_rc=""
    case "${SHELL:-}" in
      */zsh)  shell_rc="$HOME/.zshrc" ;;
      */bash)
        # macOS bash uses ~/.bash_profile by convention; Linux uses ~/.bashrc
        if [ "$(uname -s)" = "Darwin" ]; then
          shell_rc="$HOME/.bash_profile"
        else
          shell_rc="$HOME/.bashrc"
        fi
        ;;
      */fish) shell_rc="$HOME/.config/fish/config.fish" ;;
    esac

    echo "⚠️  ${INSTALL_DIR} is not on your PATH — local-review won't be found until you fix that."
    echo
    echo "Run this one-liner to fix it now:"
    echo
    if [ "${shell_rc##*/}" = "config.fish" ]; then
      echo "    fish_add_path \"${INSTALL_DIR}\""
    elif [ -n "$shell_rc" ]; then
      echo "    echo 'export PATH=\"\$PATH:${INSTALL_DIR}\"' >> \"${shell_rc}\" && source \"${shell_rc}\""
    else
      echo "    export PATH=\"\$PATH:${INSTALL_DIR}\"   # then add this line to your shell rc"
    fi
    echo
    ;;
esac

"$INSTALL_DIR/local-review" version
