#!/usr/bin/env python3
"""Run missing opaque report-only smoke tasks with local Codex workers."""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime
import json
import os
import pathlib
import subprocess
import sys
import tempfile

from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def write_json_atomically(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as target:
            json.dump(value, target, indent=2, sort_keys=True)
            target.write("\n")
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def pending_runs(workspace: pathlib.Path, limit: int | None) -> list[str]:
    baseline = load_json(workspace / "baseline.json")
    profile = baseline.get("profile")
    if profile == "report_only_smoke":
        completed_directory = "replay-records"
    elif profile == "report_only_historical_pilot":
        completed_directory = "observations"
    else:
        raise ValueError("workspace profile is not runnable")
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list):
        raise ValueError("operator manifest is invalid")
    result: list[str] = []
    for mapping in runs:
        if not isinstance(mapping, dict):
            raise ValueError("operator run mapping is invalid")
        run_id = mapping.get("run_id")
        if not isinstance(run_id, str) or mapping.get("host") != "codex":
            raise ValueError("operator run identity is not Codex-only")
        if (workspace / completed_directory / f"{run_id}.json").is_file():
            continue
        if limit is None or len(result) < limit:
            result.append(run_id)
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--limit", type=int)
    parser.add_argument("--workers", type=int, default=1)
    parser.add_argument("--timeout-seconds", type=int, default=1200)
    args = parser.parse_args()

    if args.limit is not None and args.limit < 1:
        raise ValueError("--limit must be positive")
    if args.workers < 1 or args.workers > 4:
        raise ValueError("--workers must be between 1 and 4")
    if args.timeout_seconds < 1:
        raise ValueError("--timeout-seconds must be positive")
    workspace = args.workspace.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("profile") not in {"report_only_smoke", "report_only_historical_pilot"}:
        raise ValueError("workspace profile is not runnable")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot run formal report-only evidence")
    run_ids = pending_runs(workspace, args.limit)
    scheduled = len(run_ids)
    log_directory = workspace / "batch-logs"
    log_directory.mkdir(parents=True, exist_ok=True)
    runner = pathlib.Path(__file__).with_name("qualification_run.py")
    progress_path = workspace / "batch-progress.json"
    completed: list[str] = []
    failures: list[dict[str, object]] = []

    def snapshot() -> None:
        write_json_atomically(
            progress_path,
            {
                "schema_version": 1,
                "host": "codex",
                "model": QUALIFICATION_MODEL,
                "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
                "workers": args.workers,
                "scheduled_runs": scheduled,
                "completed_runs": len(completed),
                "failed_runs": len(failures),
                "remaining_runs": scheduled - len(completed) - len(failures),
                "failures": failures,
                "updated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            },
        )

    def execute(run_id: str) -> tuple[str, subprocess.CompletedProcess[str], pathlib.Path]:
        command = [
            sys.executable,
            str(runner),
            "--workspace",
            str(workspace),
            "--run-id",
            run_id,
            "--timeout-seconds",
            str(args.timeout_seconds),
        ]
        result = subprocess.run(command, check=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        stdout_path = log_directory / f"{run_id}.stdout.json"
        stderr_path = log_directory / f"{run_id}.stderr.log"
        stdout_path.write_text(result.stdout, encoding="utf-8")
        stderr_path.write_text(result.stderr, encoding="utf-8")
        return run_id, result, stderr_path

    snapshot()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as executor:
        futures = [executor.submit(execute, run_id) for run_id in run_ids]
        for future in concurrent.futures.as_completed(futures):
            run_id, result, stderr_path = future.result()
            if result.returncode == 0:
                completed.append(run_id)
                event = {"run_id": run_id, "host": "codex", "status": "completed"}
            else:
                failure = {
                    "run_id": run_id,
                    "host": "codex",
                    "returncode": result.returncode,
                    "stderr": stderr_path.relative_to(workspace).as_posix(),
                }
                failures.append(failure)
                event = {"run_id": run_id, "host": "codex", "status": "failed", "returncode": result.returncode}
            snapshot()
            print(json.dumps(event, sort_keys=True), flush=True)
    result = {
        "schema_version": 1,
        "host": "codex",
        "model": QUALIFICATION_MODEL,
        "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
        "scheduled_runs": scheduled,
        "completed_runs": len(completed),
        "failed_runs": len(failures),
        "status": "complete" if not failures else "completed_with_failures",
    }
    json.dump(result, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0 if not failures else 1


if __name__ == "__main__":
    raise SystemExit(main())
