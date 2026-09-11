#!/usr/bin/env bash
set -euo pipefail
exec python3 -I -B -S "$(dirname "$0")/native_checks.py"
