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

# ----- Download + install ------------------------------------------------
asset="local-review_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${url}"
curl -fsSL "$url" -o "${tmp}/${asset}"

cd "$tmp"
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
    echo "Note: ${INSTALL_DIR} is not on your PATH."
    echo "Add to your shell rc:"
    echo "    export PATH=\"\$PATH:${INSTALL_DIR}\""
    ;;
esac

"$INSTALL_DIR/local-review" version
