#!/usr/bin/env bash
set -euo pipefail

release_version="${VERSION:-$(git describe --tags --abbrev=0 HEAD)}"
exec make release-check VERSION="$release_version"
