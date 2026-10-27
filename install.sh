#!/bin/sh
# quality-review installer.
# Downloads the matching release binary from GitHub, verifies its checksum,
# and atomically installs it to ~/.local/bin (override with INSTALL_DIR).
#
#   curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- v0.1.2      # pin a version
set -eu
umask 077

REPO="Fueav/code-quality"
BIN="quality-review"
VERSION="${1:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

if [ "$VERSION" != latest ] && ! printf '%s\n' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$'; then
	echo "invalid release version: $VERSION" >&2
	exit 2
fi

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

mkdir -p "$INSTALL_DIR"
INSTALL_DIR=$(CDPATH= cd "$INSTALL_DIR" && pwd -P)
if [ -L "$INSTALL_DIR/$BIN" ] || { [ -e "$INSTALL_DIR/$BIN" ] && [ ! -f "$INSTALL_DIR/$BIN" ]; }; then
	echo "refusing to replace a non-regular quality-review binary" >&2
	exit 1
fi
lock="$INSTALL_DIR/.quality-review-upgrade.lock"
mkdir "$lock" 2>/dev/null || { echo "another installation is active, or its lock requires inspection: $lock" >&2; exit 1; }
tmp=''
switched=0
complete=0
backup=''
cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if [ "$switched" = 1 ] && [ "$complete" = 0 ]; then
		if ! sh "$backup/rollback.sh" --locked; then
			echo "rollback failed; run: sh \"$backup/rollback.sh\"" >&2
		fi
		status=1
	fi
	if [ -n "$tmp" ]; then rm -rf "$tmp"; fi
	rmdir "$lock"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
tmp=$(mktemp -d "$INSTALL_DIR/.quality-review-stage.XXXXXX")

digest() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

echo "Downloading ${asset} (${VERSION})..."
curl -fsSL --retry 3 --retry-delay 2 "${base}/checksums.txt" -o "${tmp}/checksums.txt"
expected=$(awk -v asset="$asset" '$2 == asset {print $1}' "${tmp}/checksums.txt")
if ! printf '%s\n' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$' || [ "$(printf '%s\n' "$expected" | wc -l | tr -d ' ')" != 1 ]; then
	echo "missing, duplicate, or invalid checksum for ${asset}" >&2
	exit 1
fi
expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
if [ -x "$INSTALL_DIR/$BIN" ] && [ "$(digest "$INSTALL_DIR/$BIN")" = "$expected" ]; then
	installed_version=$("$INSTALL_DIR/$BIN" version)
	if [ "$VERSION" = latest ] || [ "$installed_version" = "quality-review $VERSION" ]; then
		printf 'QUALITY_REVIEW_BIN=%s\n' "$INSTALL_DIR/$BIN"
		printf 'INSTALL_STATUS=UNCHANGED\n'
		complete=1
		exit 0
	fi
fi
curl -fsSL --retry 3 --retry-delay 2 "${base}/${asset}" -o "${tmp}/${BIN}"
actual=$(digest "${tmp}/${BIN}")
if [ "$expected" != "$actual" ]; then
	echo "checksum mismatch for ${asset}: expected ${expected}, got ${actual}" >&2
	exit 1
fi

chmod 755 "${tmp}/${BIN}"
installed_version=$("${tmp}/${BIN}" version)
if ! printf '%s\n' "$installed_version" | grep -Eq '^quality-review v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$' ||
	{ [ "$VERSION" != latest ] && [ "$installed_version" != "quality-review $VERSION" ]; }; then
	echo "downloaded binary has an unexpected version: $installed_version" >&2
	exit 1
fi

rollback_root="$INSTALL_DIR/.quality-review-rollbacks"
if [ -L "$rollback_root" ]; then echo "rollback directory must not be a symlink" >&2; exit 1; fi
mkdir -p "$rollback_root"
chmod 700 "$rollback_root"
backup=$(mktemp -d "$rollback_root/cli.XXXXXX")
printf '%s\n' "$INSTALL_DIR" > "$backup/install-dir"
printf '%s\n' "$actual" > "$backup/new-sha256"
printf '%s\n' "$base" > "$backup/source"
printf '%s\n' "$installed_version" > "$backup/new-version"
if [ -f "$INSTALL_DIR/$BIN" ]; then
	cp -p "$INSTALL_DIR/$BIN" "$backup/quality-review"
	digest "$backup/quality-review" > "$backup/old-sha256"
fi
cat > "$backup/rollback.sh" <<'ROLLBACK'
#!/bin/sh
set -eu
root=$(CDPATH= cd "$(dirname "$0")" && pwd -P)
IFS= read -r install_dir < "$root/install-dir"
stage=''
own_lock=0
cleanup() {
	if [ -n "$stage" ]; then rm -f "$stage"; fi
	if [ "$own_lock" = 1 ]; then rmdir "$install_dir/.quality-review-upgrade.lock"; fi
}
trap cleanup EXIT
if [ "${1:-}" != --locked ]; then
	mkdir "$install_dir/.quality-review-upgrade.lock" 2>/dev/null || { echo 'another installation is active; rollback cannot proceed' >&2; exit 1; }
	own_lock=1
fi
digest() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | awk '{print $1}'; }
target="$install_dir/quality-review"
test ! -L "$target" || { echo 'refusing to roll back a symlink' >&2; exit 1; }
if [ -e "$target" ]; then
	current=$(digest "$target")
	if [ "$current" != "$(cat "$root/new-sha256")" ] && { [ ! -f "$root/old-sha256" ] || [ "$current" != "$(cat "$root/old-sha256")" ]; }; then
		echo 'refusing to overwrite a different installation; inspect retained rollback evidence' >&2; exit 1
	fi
fi
if [ -f "$root/quality-review" ]; then
	test "$(digest "$root/quality-review")" = "$(cat "$root/old-sha256")"
	stage=$(mktemp "$install_dir/.quality-review-restore.XXXXXX")
	cp -p "$root/quality-review" "$stage"
	mv -f "$stage" "$target"
else
	rm -f "$target"
fi
printf 'INSTALL_STATUS=ROLLED_BACK\n'
ROLLBACK
chmod 700 "$backup/rollback.sh"
switched=1
mv -f "${tmp}/${BIN}" "${INSTALL_DIR}/${BIN}"
test "$(digest "$INSTALL_DIR/$BIN")" = "$actual"
test "$("$INSTALL_DIR/$BIN" version)" = "$installed_version"
complete=1
echo "Installed ${BIN} -> ${INSTALL_DIR}/${BIN}"
echo "QUALITY_REVIEW_BIN=${INSTALL_DIR}/${BIN}"
printf 'QUALITY_REVIEW_ROLLBACK=%s\n' "$backup/rollback.sh"

case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) ;;
	*) echo "NOTE: add ${INSTALL_DIR} to your PATH to use ${BIN}." ;;
esac
printf '%s\n' "$installed_version"
