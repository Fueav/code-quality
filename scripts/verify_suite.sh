#!/usr/bin/env bash
set -euo pipefail
exec "$(cd "$(dirname "$0")/.." && pwd)/harness/suite_conformance.py" gate
