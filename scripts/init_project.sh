#!/usr/bin/env bash
set -euo pipefail
exec "$(cd "$(dirname "$0")/.." && pwd)/harness/template_delivery.py" init "$@"
