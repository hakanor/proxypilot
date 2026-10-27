#!/bin/sh
# Installs the latest ProxyPilot release into ~/.local/bin (or $PROXYPILOT_BIN).
# Verifies the archive against the release's checksums.txt before installing.
#   curl -fsSL https://raw.githubusercontent.com/hakanor/proxypilot/main/install.sh | sh
set -eu

REPO="hakanor/proxypilot"
BIN_DIR="${PROXYPILOT_BIN:-$HOME/.local/bin}"
VERSION="${PROXYPILOT_VERSION:-}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin) arch=universal ;;
  linux) ;;
  *) echo "unsupported OS: $os (download a release manually)" >&2; exit 1 ;;
esac

if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n1)
fi
[ -n "$VERSION" ] || { echo "could not find the latest version" >&2; exit 1; }

name="proxypilot_${VERSION}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v$VERSION"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $name"
curl -fsSL -o "$tmp/$name" "$base/$name"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

want=$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$name" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $name (want $want, got $got)" >&2
  exit 1
fi

tar -xzf "$tmp/$name" -C "$tmp"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/proxypilot" "$BIN_DIR/proxypilot"
echo "Installed proxypilot $VERSION to $BIN_DIR/proxypilot"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Add $BIN_DIR to your PATH to run it as 'proxypilot'." ;;
esac
echo "Next: proxypilot ui"
