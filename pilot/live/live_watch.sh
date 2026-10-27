#!/bin/zsh

set -euo pipefail

script_dir=${0:A:h}
quality_repo=${QUALITY_REPO_ROOT:-${script_dir:h:h}}
data_root=${CODE_QUALITY_LIVE_ROOT:-$HOME/AiProject/code-quality-live}
config_file=$data_root/config.json
config_explicit=0
daily_max=${LIVE_DAILY_MAX:-10}
watch_schedule=${LIVE_WATCH_CRON:-17 2 * * *}
adjudicate_schedule=${LIVE_ADJUDICATE_CRON:-43 3 * * 1}
mode=once
umask 077

usage() {
  print -u2 "usage: $0 [--once|--install|--uninstall] [--config <path>] [--data-root <path>] [--max <count>]"
}

while (( $# > 0 )); do
  case "$1" in
    --once) mode=once; shift ;;
    --install) mode=install; shift ;;
    --uninstall) mode=uninstall; shift ;;
    --config)
      (( $# >= 2 )) || { usage; exit 2; }
      config_file=$2
      config_explicit=1
      shift 2
      ;;
    --data-root)
      (( $# >= 2 )) || { usage; exit 2; }
      data_root=$2
      (( config_explicit == 1 )) || config_file=$data_root/config.json
      shift 2
      ;;
    --max)
      (( $# >= 2 )) || { usage; exit 2; }
      daily_max=$2
      shift 2
      ;;
    *) usage; exit 2 ;;
  esac
done

python_bin=${PYTHON_BIN:-$(command -v python3)}

validate_schedule() {
  local value=$1
  "$python_bin" - "$value" <<'PY'
import sys
if len(sys.argv[1].split()) != 5:
    raise SystemExit("cron schedule must contain exactly five fields")
PY
}

initialize_config() {
  mkdir -p "$data_root"
  if [[ -e "$config_file" ]]; then
    return
  fi
  local codex_bin
  codex_bin=${CODEX_BIN:-$(command -v codex)}
  "$python_bin" - "$config_file" "$codex_bin" "$quality_repo" <<'PY'
import json
import os
import pathlib
import sys

destination = pathlib.Path(sys.argv[1])
codex_bin = sys.argv[2]
quality_repo = sys.argv[3]
home = pathlib.Path.home()
document = {
    "schema_version": 1,
    "codex_bin": codex_bin,
    "quality_repo": quality_repo,
    "repositories": [
        {"name": "agent_marketplace", "path": str(home / "AiProject" / "agent_marketplace"), "ref": "HEAD"},
        {"name": "general-agent-ai", "path": str(home / "AiProject" / "general-agent-ai"), "ref": "HEAD"},
        {"name": "code-quality", "path": str(home / "AiProject" / "code-quality"), "ref": "main"},
    ],
}
temporary = destination.with_name("." + destination.name + ".tmp")
temporary.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
os.replace(temporary, destination)
PY
  chmod 600 "$config_file"
}

rewrite_crontab() {
  local action=$1
  local temporary
  temporary=$(mktemp "${TMPDIR:-/tmp}/code-quality-live-cron.XXXXXX")
  local existing
  existing=$(crontab -l 2>/dev/null || true)
  local watch_log=$data_root/logs/live-watch.log
  local adjudicate_log=$data_root/logs/live-adjudicate.log
  "$python_bin" - "$action" "$watch_schedule" "$adjudicate_schedule" "$data_root/bin/live_watch.sh" "$data_root/bin/live_adjudicate.py" "$config_file" "$data_root" "$python_bin" "$watch_log" "$adjudicate_log" "$temporary" "$quality_repo" <<'PY'
import pathlib
import shlex
import subprocess
import sys

(
    action,
    watch_schedule,
    adjudicate_schedule,
    watch_script,
    adjudicate_script,
    config_file,
    data_root,
    python_bin,
    watch_log,
    adjudicate_log,
    output,
    quality_repo,
) = sys.argv[1:]
try:
    current = subprocess.run(["crontab", "-l"], text=True, capture_output=True, check=False).stdout.splitlines()
    kept = []
    inside = False
    for line in current:
        if line == "# BEGIN code-quality-live":
            inside = True
            continue
        if line == "# END code-quality-live":
            inside = False
            continue
        if not inside:
            kept.append(line)
    while kept and not kept[-1].strip():
        kept.pop()
    if action == "install":
        quote = shlex.quote
        kept.extend(
            [
                "# BEGIN code-quality-live",
                f"{watch_schedule} QUALITY_REPO_ROOT={quote(quality_repo)} /bin/zsh {quote(watch_script)} --once --config {quote(config_file)} --data-root {quote(data_root)} >> {quote(watch_log)} 2>&1",
                f"{adjudicate_schedule} {quote(python_bin)} {quote(adjudicate_script)} --config {quote(config_file)} --data-root {quote(data_root)} >> {quote(adjudicate_log)} 2>&1",
                "# END code-quality-live",
            ]
        )
    pathlib.Path(output).write_text("\n".join(kept) + ("\n" if kept else ""), encoding="utf-8")
except Exception:
    pathlib.Path(output).unlink(missing_ok=True)
    raise
PY
  if [[ -s "$temporary" ]]; then
    crontab "$temporary"
  else
    # macOS crontab prompts interactively before installing an empty file.
    # Removing an absent crontab is harmless and keeps uninstall noninteractive.
    crontab -r 2>/dev/null || true
  fi
  rm -f "$temporary"
}

if [[ "$mode" == install ]]; then
  validate_schedule "$watch_schedule"
  validate_schedule "$adjudicate_schedule"
  initialize_config
  mkdir -p "$data_root/logs" "$data_root/state" "$data_root/reviews" "$data_root/adjudications" "$data_root/snapshots" "$data_root/bin"
  for component in live_watch.sh live_adjudicate.py native_records.py; do
    if [[ "$script_dir/$component" != "$data_root/bin/$component" ]]; then
      cp "$script_dir/$component" "$data_root/bin/$component.tmp"
      chmod 700 "$data_root/bin/$component.tmp"
      mv "$data_root/bin/$component.tmp" "$data_root/bin/$component"
    fi
  done
  rewrite_crontab install
  print "installed code-quality live cron"
  print "watch schedule: $watch_schedule"
  print "adjudication schedule: $adjudicate_schedule"
  print "config: $config_file"
  exit 0
fi

if [[ "$mode" == uninstall ]]; then
  rewrite_crontab uninstall
  print "removed code-quality live cron; data retained at $data_root"
  exit 0
fi

if [[ ! "$daily_max" =~ '^[1-9][0-9]*$' ]]; then
  print -u2 "--max must be a positive integer"
  exit 2
fi
if [[ ! -f "$config_file" || -L "$config_file" ]]; then
  print -u2 "config must be a regular non-symlink file: $config_file"
  exit 2
fi

mkdir -p "$data_root/logs" "$data_root/state" "$data_root/reviews" "$data_root/bin" "$data_root/run"
lock_dir=$data_root/run/live-watch.lock
if ! mkdir "$lock_dir" 2>/dev/null; then
  print -u2 "live watch is already running: $lock_dir"
  exit 1
fi
runtime_root=$(mktemp -d "${TMPDIR:-/tmp}/code-quality-live.XXXXXX")
cleanup() {
  rm -rf "$runtime_root"
  rmdir "$lock_dir" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

build_quality_binary() {
  if [[ -n "${LIVE_QUALITY_BINARY:-}" ]]; then
    print -r -- "$LIVE_QUALITY_BINARY"
    return
  fi
  local main_sha
  main_sha=$(git -C "$quality_repo" rev-parse main)
  local destination=$data_root/bin/quality-review-main-$main_sha
  if [[ ! -x "$destination" ]]; then
    local source_dir=$runtime_root/quality-source-$main_sha
    mkdir -p "$source_dir"
    git -C "$quality_repo" archive "$main_sha" | tar -x -C "$source_dir"
    (cd "$source_dir" && go build -ldflags "-s -w -X main.version=main-$main_sha" -o "$destination.tmp" ./cmd/quality-review)
    chmod 700 "$destination.tmp"
    mv "$destination.tmp" "$destination"
  fi
  print -r -- "$destination"
}

quality_binary=$(build_quality_binary)

config_rows() {
  "$python_bin" - "$config_file" <<'PY'
import json
import pathlib
import re
import sys

document = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if document.get("schema_version") != 1 or not isinstance(document.get("repositories"), list):
    raise SystemExit("invalid live config")
for repository in document["repositories"]:
    name = repository.get("name")
    location = repository.get("path")
    ref = repository.get("ref", "HEAD")
    if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9._-]+", name):
        raise SystemExit("invalid repository name")
    if not all(isinstance(value, str) and value and "\t" not in value and "\n" not in value for value in (location, ref)):
        raise SystemExit(f"invalid repository config for {name}")
    print(name, location, ref, sep="\t")
PY
}

read_pending() {
  local state_file=$1
  "$python_bin" - "$state_file" <<'PY'
import json
import pathlib
import sys
source = pathlib.Path(sys.argv[1])
if source.exists():
    document = json.loads(source.read_text(encoding="utf-8"))
    for commit in document.get("pending", []):
        print(commit)
PY
}

read_watermark() {
  local state_file=$1
  "$python_bin" - "$state_file" <<'PY'
import json
import pathlib
import sys
source = pathlib.Path(sys.argv[1])
if source.exists():
    print(json.loads(source.read_text(encoding="utf-8")).get("watermark", ""))
PY
}

write_state() {
  local state_file=$1
  local watermark=$2
  shift 2
  "$python_bin" - "$state_file" "$watermark" "$@" <<'PY'
import json
import os
import pathlib
import sys
destination = pathlib.Path(sys.argv[1])
document = {"schema_version": 1, "watermark": sys.argv[2], "pending": sys.argv[3:]}
temporary = destination.with_name("." + destination.name + ".tmp")
temporary.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
os.replace(temporary, destination)
PY
}

touches_source() {
  local repo_dir=$1
  local commit=$2
  local changed_file
  while IFS= read -r changed_file; do
    case "$changed_file" in
      *.c|*.cc|*.cpp|*.cs|*.ex|*.exs|*.go|*.h|*.hpp|*.java|*.js|*.jsx|*.kt|*.kts|*.m|*.mm|*.php|*.proto|*.py|*.rb|*.rs|*.scala|*.sh|*.sol|*.sql|*.svelte|*.swift|*.ts|*.tsx|*.vue) return 0 ;;
    esac
  done < <(git -C "$repo_dir" diff-tree --no-commit-id --name-only -r "$commit")
  return 1
}

changed_lines() {
  local repo_dir=$1
  local commit=$2
  git -C "$repo_dir" diff-tree --no-commit-id --numstat -r "$commit" | awk '
    $1 == "-" || $2 == "-" { total += 3001; next }
    { total += $1 + $2 }
    END { print total + 0 }
  '
}

setup_clone() {
  local source_repo=$1
  local target=$2
  local clone_dir=$3
  git clone --shared --no-checkout "$source_repo" "$clone_dir" >/dev/null 2>&1
  git -C "$clone_dir" checkout --detach "$target" >/dev/null 2>&1
  git -C "$clone_dir" remote remove origin
  local git_ref
  while IFS= read -r git_ref; do
    [[ -z "$git_ref" ]] || git -C "$clone_dir" update-ref -d "$git_ref"
  done < <(git -C "$clone_dir" for-each-ref --format='%(refname)')
  git -C "$clone_dir" reflog expire --expire=now --all
  git -C "$clone_dir" repack -a -d >/dev/null 2>&1
  rm -f "$clone_dir/.git/objects/info/alternates"
  [[ -z "$(git -C "$clone_dir" for-each-ref --format='%(refname)')" ]]
  [[ -z "$(git -C "$clone_dir" remote)" ]]
  [[ ! -e "$clone_dir/.git/objects/info/alternates" ]]
  git -C "$clone_dir" cat-file -e "$target^{commit}"
}

run_product_lane() {
  local repo_name=$1
  local source_repo=$2
  local base=$3
  local target=$4
  local result_destination=$5
  local archive=$data_root/reviews/$repo_name/$target
  local clone_dir=$archive/source-repository
  mkdir -p "$archive"
  if [[ ! -d "$clone_dir" ]]; then
    setup_clone "$source_repo" "$target" "$clone_dir" || return $?
  fi
  "$python_bin" "$script_dir/native_records.py" \
    --quality-bin "$quality_binary" --repository "$clone_dir" --config "$config_file" \
    --archive "$archive" --repo-name "$repo_name" --base "$base" --target "$target" \
    --result-destination "$result_destination" --record "$result_destination.record.json"

}

append_index() {
  local repo_name=$1
  local sha=$2
  local started_at=$3
  local duration=$4
  local run_status=$5
  local result_file=${6:-}
  local error_message=${7:-}
  "$python_bin" - "$repo_name" "$sha" "$started_at" "$duration" "$run_status" "$result_file" "$error_message" "$temporary_result.record.json" >>"$data_root/index.jsonl" <<'PY'
import datetime as dt
import json
import pathlib
import sys
repo, sha, started_at, duration, status, result_file, error, record_path = sys.argv[1:]
record = {
    "schema_version": 2,
    "repo": repo,
    "sha": sha,
    "reviewed_at": started_at,
    "wall_duration_ms": int(duration) * 1000,
    "stage_metrics": [],
    "semantic_result": None,
    "status": status,
}
if pathlib.Path(record_path).is_file():
    record.update(json.loads(pathlib.Path(record_path).read_text(encoding="utf-8")))
record.setdefault("status", status)
if status != "COMPLETE":
    record.setdefault("error", error)
print(json.dumps(record, sort_keys=True))
PY
}

reviewed_count=0
attempted_count=0
overall_failure=0
while IFS=$'\t' read -r repo_name repo_dir repo_ref; do
  [[ -d "$repo_dir/.git" || -f "$repo_dir/.git" ]] || { print -u2 "skip missing Git repository: $repo_name $repo_dir"; overall_failure=1; continue; }
  state_file=$data_root/state/$repo_name.json
  current_head=$(git -C "$repo_dir" rev-parse "$repo_ref^{commit}")
  watermark=$(read_watermark "$state_file")
  pending_raw=$(read_pending "$state_file")
  pending_commits=()
  if [[ -n "$pending_raw" ]]; then
    pending_commits=("${(@f)pending_raw}")
  fi
  if [[ -z "$watermark" ]]; then
    write_state "$state_file" "$current_head"
    print "initialized $repo_name at $current_head"
    continue
  fi
  if git -C "$repo_dir" merge-base --is-ancestor "$watermark" "$current_head" 2>/dev/null; then
    discovery_base=$watermark
  else
    discovery_base=$(git -C "$repo_dir" merge-base "$watermark" "$current_head" 2>/dev/null || true)
    [[ -n "$discovery_base" ]] || discovery_base=$current_head
  fi
  if [[ "$discovery_base" != "$current_head" ]]; then
    while IFS= read -r commit; do
      [[ -n "$commit" ]] || continue
      parent_line=(${(s: :)$(git -C "$repo_dir" rev-list --parents -n 1 "$commit")})
      (( ${#parent_line} == 2 )) || continue
      touches_source "$repo_dir" "$commit" || continue
      line_count=$(changed_lines "$repo_dir" "$commit")
      (( line_count <= 3000 )) || continue
      if (( ${pending_commits[(Ie)$commit]} == 0 )) && [[ ! -f "$data_root/reviews/$repo_name/$commit/review-result.json" ]]; then
        pending_commits+=("$commit")
      fi
    done < <(git -C "$repo_dir" rev-list --reverse --topo-order "$discovery_base..$current_head")
  fi
  write_state "$state_file" "$current_head" "${pending_commits[@]}"

  while (( ${#pending_commits} > 0 && attempted_count < daily_max )); do
    target=${pending_commits[1]}
    base=$(git -C "$repo_dir" rev-parse "$target^")
    archive_dir=$data_root/reviews/$repo_name/$target
    mkdir -p "$archive_dir"
    temporary_result=$runtime_root/result-$repo_name-$target.json
    started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    start_epoch=$(date +%s)
    attempted_count=$((attempted_count + 1))
    set +e
    run_product_lane "$repo_name" "$repo_dir" "$base" "$target" "$temporary_result"
    lane_exit=$?
    set -e
    duration=$(( $(date +%s) - start_epoch ))
    if (( lane_exit != 0 )); then
      append_index "$repo_name" "$target" "$started_at" "$duration" FAILED "" "product lane exited $lane_exit"
      print -u2 "FAILED $repo_name $target exit=$lane_exit"
      overall_failure=1
      break
    fi
    cp "$temporary_result" "$archive_dir/review-result.json.tmp"
    mv "$archive_dir/review-result.json.tmp" "$archive_dir/review-result.json"
    append_index "$repo_name" "$target" "$started_at" "$duration" COMPLETE "$archive_dir/review-result.json" ""
    pending_commits=("${pending_commits[@]:1}")
    write_state "$state_file" "$current_head" "${pending_commits[@]}"
    reviewed_count=$((reviewed_count + 1))
    print "COMPLETE $repo_name $target"
  done
done < <(config_rows)

print "attempted $attempted_count commit(s), completed $reviewed_count; daily limit $daily_max"
exit "$overall_failure"
