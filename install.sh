#!/bin/sh
# quality-review installer.
# Downloads the matching release binary from GitHub, verifies its checksum,
# and installs it to ~/.local/bin (override with INSTALL_DIR).
#
#   curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- v0.1.2      # pin a version
set -eu

REPO="Fueav/code-quality"
BIN="quality-review"
VERSION="${1:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
	darwin | linux) ;;
	*) echo "unsupported OS: $os" >&2; exit 1 ;;
esac

asset="${BIN}_${os}_${arch}"
if [ -n "${QUALITY_REVIEW_RELEASE_BASE:-}" ]; then
	base="$QUALITY_REVIEW_RELEASE_BASE"
elif [ "$VERSION" = "latest" ]; then
	base="https://github.com/${REPO}/releases/latest/download"
else
	base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${asset} (${VERSION})..."
curl -fsSL --retry 3 --retry-delay 2 "${base}/${asset}" -o "${tmp}/${BIN}"
curl -fsSL --retry 3 --retry-delay 2 "${base}/checksums.txt" -o "${tmp}/checksums.txt"

expected=$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')
if [ -z "$expected" ]; then
	echo "no checksum for ${asset} in checksums.txt" >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "${tmp}/${BIN}" | awk '{print $1}')
else
	actual=$(shasum -a 256 "${tmp}/${BIN}" | awk '{print $1}')
fi
if [ "$expected" != "$actual" ]; then
	echo "checksum mismatch for ${asset}: expected ${expected}, got ${actual}" >&2
	exit 1
fi

mkdir -p "$INSTALL_DIR"
mv "${tmp}/${BIN}" "${INSTALL_DIR}/${BIN}"
chmod +x "${INSTALL_DIR}/${BIN}"
echo "Installed ${BIN} -> ${INSTALL_DIR}/${BIN}"
echo "QUALITY_REVIEW_BIN=${INSTALL_DIR}/${BIN}"

case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) ;;
	*) echo "NOTE: add ${INSTALL_DIR} to your PATH to use ${BIN}." ;;
esac
"${INSTALL_DIR}/${BIN}" version 2>/dev/null || true
