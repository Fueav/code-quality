#!/bin/sh
# Installs one pinned quality-review CLI and the matching host plugin.
set -eu

VERSION="${1:-}"
HOST="${2:-}"
REPO="Fueav/code-quality"
MARKETPLACE="fueav-code-quality"
PLUGIN="code-quality@${MARKETPLACE}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

case "$INSTALL_DIR" in
	/*) ;;
	*) INSTALL_DIR="$(pwd -P)/${INSTALL_DIR}" ;;
esac

case "$VERSION" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) echo "usage: bootstrap.sh <vX.Y.Z> <codex|claude>" >&2; exit 2 ;;
esac
case "$HOST" in
	codex | claude) ;;
	*) echo "usage: bootstrap.sh <vX.Y.Z> <codex|claude>" >&2; exit 2 ;;
esac

release_base="${QUALITY_REVIEW_RELEASE_BASE:-https://github.com/${REPO}/releases/download/${VERSION}}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --retry 3 --retry-delay 2 "${release_base}/install.sh" -o "${tmp}/install.sh"
QUALITY_REVIEW_RELEASE_BASE="$release_base" INSTALL_DIR="$INSTALL_DIR" sh "${tmp}/install.sh" "$VERSION"

install_dir_abs=$(CDPATH= cd "$INSTALL_DIR" && pwd -P)
review_bin="${install_dir_abs}/quality-review"
if [ ! -x "$review_bin" ]; then
	echo "quality-review was not installed at ${review_bin}" >&2
	exit 1
fi
installed_version=$("$review_bin" version)
if [ "$installed_version" != "quality-review ${VERSION}" ]; then
	echo "installed CLI version mismatch: ${installed_version}" >&2
	exit 1
fi

case "$HOST" in
	codex)
		command -v codex >/dev/null 2>&1 || { echo "codex is not installed" >&2; exit 1; }
		if marketplace_json=$(codex plugin marketplace list --json 2>/dev/null) && printf '%s' "$marketplace_json" | grep -F "$MARKETPLACE" >/dev/null 2>&1; then
			codex plugin remove "$PLUGIN" >/dev/null 2>&1 || true
			codex plugin marketplace remove "$MARKETPLACE" >/dev/null
		fi
		codex plugin marketplace add "$REPO" --ref "$VERSION"
		codex plugin add "$PLUGIN"
		doctor_host="codex"
		;;
	claude)
		command -v claude >/dev/null 2>&1 || { echo "claude is not installed" >&2; exit 1; }
		claude plugin marketplace add "https://github.com/Fueav/code-quality.git#${VERSION}"
		if installed_plugins=$(claude plugin list 2>/dev/null) && printf '%s' "$installed_plugins" | grep -F "$PLUGIN" >/dev/null 2>&1; then
			claude plugin update "$PLUGIN" --scope user
		else
			claude plugin install "$PLUGIN" --scope user
		fi
		doctor_host="claude-code"
		;;
esac

printf 'QUALITY_REVIEW_BIN=%s\n' "$review_bin"
printf 'QUALITY_REVIEW_HOST=%s\n' "$doctor_host"
printf 'NEXT_COMMAND="%s" doctor --host %s --repo .\n' "$review_bin" "$doctor_host"
if [ "$HOST" = "claude" ]; then
	printf 'CLAUDE_RELOAD=/reload-plugins\n'
fi
