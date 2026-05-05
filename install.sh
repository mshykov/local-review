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
  if command -v sha256sum >/dev/null 2>&1; then
    # GNU coreutils (Linux, Homebrew on macOS).
    if ! grep " ${asset}\$" "$checksums" | sha256sum -c -; then
      echo "❌ checksum mismatch for ${asset}" >&2
      echo "   refusing to install — possible tampering or corrupted download" >&2
      exit 1
    fi
  elif command -v shasum >/dev/null 2>&1; then
    # macOS default: shasum -a 256 -c reads <hash>  <file> lines.
    if ! grep " ${asset}\$" "$checksums" | shasum -a 256 -c -; then
      echo "❌ checksum mismatch for ${asset}" >&2
      echo "   refusing to install — possible tampering or corrupted download" >&2
      exit 1
    fi
  else
    echo "⚠️  no sha256sum / shasum binary found — skipping integrity check" >&2
    echo "   install one of: coreutils (sha256sum) or perl (shasum) for tamper resistance" >&2
  fi
else
  echo "⚠️  no checksums.txt for ${VERSION} — skipping integrity check" >&2
  echo "   (releases before v0.6.0 don't ship a manifest; upgrade to v0.6+ for verified installs)" >&2
  cd "$tmp"
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
