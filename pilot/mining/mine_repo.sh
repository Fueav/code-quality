#!/bin/zsh
set -eu

if (( $# != 1 )); then
  print -u2 "usage: $0 <repo_path>"
  exit 2
fi

source_repo=${1:A}
script_dir=${0:A:h}
repo_name=${source_repo:t}
workers=${MINING_WORKERS:-4}
if (( workers < 1 || workers > 4 )); then
  print -u2 "MINING_WORKERS must be between 1 and 4"
  exit 2
fi

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
run_dir=${MINING_RUN_DIR:-$script_dir/runs/$repo_name-$timestamp}
run_dir=${run_dir:A}
clone_dir="$run_dir/repo"
results_dir="$run_dir/results"
traces_dir="$run_dir/traces"
mkdir -p "$results_dir" "$traces_dir"

if [[ ! -d "$clone_dir/.git" ]]; then
  git clone --quiet --no-hardlinks --single-branch "$source_repo" "$clone_dir"
  git -C "$clone_dir" remote remove origin
fi

"$script_dir/prefilter.sh" "$clone_dir" >"$run_dir/candidates.txt"
candidate_count=$(wc -l <"$run_dir/candidates.txt" | tr -d ' ')
print -u2 "[$repo_name] $candidate_count candidates; concurrency=$workers"

typeset -a pids
pids=()
launched=0
while IFS= read -r line; do
  [[ -n "$line" ]] || continue
  sha=${line#KEEP }
  sha=${sha%% ::*}
  if [[ -s "$results_dir/$sha.json" ]]; then
    rm -f "$traces_dir/$sha/FAILED"
    continue
  fi
  (
    trace_dir="$traces_dir/$sha"
    mkdir -p "$trace_dir"
    if MINING_REPO_NAME="$repo_name" MINING_TRACE_DIR="$trace_dir" "$script_dir/trace.sh" "$clone_dir" "$sha" >"$results_dir/$sha.tmp"; then
      mv "$results_dir/$sha.tmp" "$results_dir/$sha.json"
      rm -f "$trace_dir/FAILED"
    else
      rm -f "$results_dir/$sha.tmp"
      print -r -- "$sha" >"$trace_dir/FAILED"
    fi
  ) </dev/null &
  pids+=($!)
  launched=$(( launched + 1 ))

  if (( ${#pids} >= workers )); then
    for pid in $pids; do wait "$pid" || true; done
    pids=()
  fi
done <"$run_dir/candidates.txt"
for pid in $pids; do wait "$pid" || true; done

python3 "$script_dir/aggregate.py" \
  --repo "$clone_dir" \
  --repo-name "$repo_name" \
  --results-dir "$results_dir" \
  --output "$run_dir/targets.json" \
  --stats-output "$run_dir/stats.json"

python3 - "$run_dir" "$candidate_count" <<'PY'
import json
from pathlib import Path
import sys

run = Path(sys.argv[1])
totals = {"input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0, "reasoning_output_tokens": 0}
wall = 0
attempted = 0
failed = 0
for cost_path in run.glob("traces/*/cost.json"):
    attempted += 1
    cost = json.loads(cost_path.read_text())
    wall += int(cost.get("wall_seconds", 0))
    for key in totals:
        totals[key] += int(cost.get("usage", {}).get(key, 0))
failed = sum(
    1
    for marker in run.glob("traces/*/FAILED")
    if not (run / "results" / f"{marker.parent.name}.json").is_file()
)
payload = {
    "candidate_count": int(sys.argv[2]),
    "attempted_traces": attempted,
    "failed_traces": failed,
    "summed_trace_wall_seconds": wall,
    "token_usage": totals,
}
(run / "cost-summary.json").write_text(json.dumps(payload, indent=2) + "\n")
PY

print -r -- "$run_dir"
