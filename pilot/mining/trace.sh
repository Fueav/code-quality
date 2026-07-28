#!/bin/zsh
set -eu

if (( $# != 2 )); then
  print -u2 "usage: $0 <repo_path> <fix_sha>"
  exit 2
fi

repo=${1:A}
fix_sha=$2
script_dir=${0:A:h}
git -C "$repo" rev-parse --verify "$fix_sha^{commit}" >/dev/null
fix_sha=$(git -C "$repo" rev-parse "$fix_sha^{commit}")
repo_name=${MINING_REPO_NAME:-${repo:t}}

temporary_trace=false
if [[ -n ${MINING_TRACE_DIR:-} ]]; then
  trace_dir=${MINING_TRACE_DIR:A}
  mkdir -p "$trace_dir"
else
  trace_dir=$(mktemp -d "${TMPDIR:-/tmp}/code-quality-trace.XXXXXX")
  temporary_trace=true
fi

source_codex_home=${CODEX_SOURCE_HOME:-${CODEX_HOME:-$HOME/.codex}}
auth_file="$source_codex_home/auth.json"
if [[ ! -f "$auth_file" ]]; then
  print -u2 "missing Codex authentication file: $auth_file"
  exit 2
fi

isolated_home="$trace_dir/codex-home"
mkdir -p "$isolated_home"
cp "$auth_file" "$isolated_home/auth.json"
chmod 600 "$isolated_home/auth.json"

cleanup() {
  rm -f "$isolated_home/auth.json"
  rm -rf "$isolated_home"
  if [[ "$temporary_trace" == true ]]; then
    rm -rf "$trace_dir"
  fi
}
trap cleanup EXIT INT TERM

events="$trace_dir/events.jsonl"
stderr_log="$trace_dir/stderr.log"
last_message="$trace_dir/result.json"
started=$(date +%s)
fix_subject=$(git -C "$repo" show -s --format=%s "$fix_sha")
prompt="$(<"$script_dir/trace-prompt.md")

Repository name: $repo_name
Fix commit: $fix_sha
Fix subject: $fix_subject"

set +e
CODEX_HOME="$isolated_home" codex exec \
  -s read-only \
  -C "$repo" \
  --ignore-user-config \
  --ephemeral \
  --output-schema "$script_dir/mining-result.schema.json" \
  --json \
  --output-last-message "$last_message" \
  "$prompt" >"$events" 2>"$stderr_log"
codex_exit=$?
set -e
elapsed=$(( $(date +%s) - started ))

python3 - "$events" "$trace_dir/cost.json" "$elapsed" "$codex_exit" <<'PY'
import json
from pathlib import Path
import sys

events = Path(sys.argv[1])
usage = {}
if events.exists():
    for line in events.read_text(errors="replace").splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") == "turn.completed" and isinstance(event.get("usage"), dict):
            usage = event["usage"]
payload = {
    "wall_seconds": int(sys.argv[3]),
    "exit_status": int(sys.argv[4]),
    "usage": usage,
}
Path(sys.argv[2]).write_text(json.dumps(payload, indent=2) + "\n")
PY

if (( codex_exit != 0 )); then
  print -u2 "codex trace failed for $fix_sha (exit $codex_exit); see $trace_dir"
  exit "$codex_exit"
fi

python3 - "$last_message" "$repo_name" "$fix_sha" <<'PY'
import json
from pathlib import Path
import sys

path = Path(sys.argv[1])
payload = json.loads(path.read_text())
if payload.get("repo") != sys.argv[2]:
    raise SystemExit(f"trace returned repo={payload.get('repo')!r}, expected {sys.argv[2]!r}")
if payload.get("fix_commit") != sys.argv[3]:
    raise SystemExit("trace returned a different fix_commit")
print(json.dumps(payload, ensure_ascii=False, indent=2))
PY
