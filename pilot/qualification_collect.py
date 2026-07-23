#!/usr/bin/env python3
"""Collect one blind run and refresh report-only smoke progress."""

from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

from qualification_initialize import file_sha256
from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT, codex_command


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def run(*args: str) -> str:
    completed = subprocess.run(
        list(args),
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:2])}): {completed.stderr.strip()}")
    return completed.stdout


def write_atomically(path: pathlib.Path, raw: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as target:
            target.write(raw)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--result", required=True, type=pathlib.Path)
    parser.add_argument("--human-status", choices=("pending", "confirmed", "overturned"), default="pending")
    parser.add_argument("--overturn-reason")
    parser.add_argument("--reviewer")
    parser.add_argument("--review-note")
    parser.add_argument("--transcript", required=True, type=pathlib.Path)
    parser.add_argument("--runner-metadata", required=True, type=pathlib.Path)
    parser.add_argument("--input-tokens", type=int)
    parser.add_argument("--output-tokens", type=int)
    parser.add_argument("--duration-ms", type=int)
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    result = args.result.resolve(strict=True)
    expected_session_root = (workspace / "sessions" / args.run_id).resolve()
    if (
        result.is_symlink()
        or not result.is_file()
        or result.name != "review-result.json"
        or result.parent.name != "output"
        or result.parent.parent.parent != expected_session_root
        or not result.parent.parent.name.startswith("review-")
    ):
        raise ValueError("result must be the finalized output of the selected blind run session")
    session = result.parent.parent
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot collect smoke evidence")
    if (
        baseline.get("qualification_model") != QUALIFICATION_MODEL
        or baseline.get("qualification_reasoning_effort") != QUALIFICATION_REASONING_EFFORT
    ):
        raise ValueError("smoke model or reasoning effort does not match the frozen contract")

    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list):
        raise ValueError("operator manifest is invalid")
    matches = [mapping for mapping in runs if isinstance(mapping, dict) and mapping.get("run_id") == args.run_id]
    if len(matches) != 1:
        raise ValueError("run ID is unknown or duplicated")
    mapping = matches[0]
    if mapping.get("host") != "codex":
        raise ValueError("report-only smoke accepts only local Codex runs")
    task_relative = mapping.get("task")
    if task_relative != f"tasks/{mapping.get('host')}/{args.run_id}.json":
        raise ValueError("operator task mapping is invalid")
    task = load_json(workspace / str(task_relative))
    if task.get("output_root") != str(expected_session_root):
        raise ValueError("blind task output root does not match the selected run")

    metric_values = (args.input_tokens, args.output_tokens, args.duration_ms)
    if not all(value is not None for value in metric_values):
        raise ValueError("report-only smoke requires input tokens, output tokens, and duration")
    if any(value is not None and value < 0 for value in metric_values):
        raise ValueError("metrics must be non-negative")
    if args.human_status == "overturned" and not args.overturn_reason:
        raise ValueError("overturned review requires --overturn-reason")
    if args.human_status != "overturned" and args.overturn_reason:
        raise ValueError("only overturned review accepts --overturn-reason")
    human_fields = (args.reviewer, args.review_note)
    if args.human_status == "pending" and any(human_fields):
        raise ValueError("pending review must not claim a reviewer or review note")
    if args.human_status != "pending" and not all(value and value.strip() for value in human_fields):
        raise ValueError("confirmed or overturned review requires --reviewer and --review-note")

    transcript = args.transcript.resolve(strict=True)
    if transcript.is_symlink() or not transcript.is_file() or expected_session_root not in transcript.parents:
        raise ValueError("transcript must be a regular file under the selected run session root")
    runner_metadata_path = args.runner_metadata.resolve(strict=True)
    if runner_metadata_path.is_symlink() or not runner_metadata_path.is_file() or expected_session_root not in runner_metadata_path.parents:
        raise ValueError("runner metadata must be a regular file under the selected run session root")
    runner_metadata = load_json(runner_metadata_path)
    if (
        runner_metadata.get("run_id") != args.run_id
        or runner_metadata.get("host") != "codex"
        or runner_metadata.get("host_version") != baseline.get("codex_version")
        or runner_metadata.get("model") != QUALIFICATION_MODEL
        or runner_metadata.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or runner_metadata.get("command") != codex_command(expected_session_root)
        or runner_metadata.get("returncode") != 0
        or runner_metadata.get("duration_ms") != args.duration_ms
        or runner_metadata.get("stdout") != transcript.name
    ):
        raise ValueError("runner metadata does not prove a successful Terra/high Codex run")

    binary = workspace / "quality-review"
    record_path = workspace / "replay-records" / f"{args.run_id}.json"
    previous_evidence_path = workspace / "run-evidence" / f"{args.run_id}.json"
    previous_evidence = load_json(previous_evidence_path) if record_path.exists() else None
    if previous_evidence is not None and (
        previous_evidence.get("result_sha256") != file_sha256(result)
        or previous_evidence.get("main_review_sha256") != file_sha256(session / "output" / "main-review.json")
        or previous_evidence.get("markdown_sha256") != file_sha256(result.with_name("review-result.md"))
        or previous_evidence.get("transcript_sha256") != file_sha256(transcript)
        or previous_evidence.get("runner_metadata_sha256") != file_sha256(runner_metadata_path)
    ):
        raise ValueError("re-collection cannot change immutable session evidence")
    if (session / "input" / "repository").exists():
        raise ValueError("finalized smoke session retained its temporary worktree")
    run(str(binary), "validate", str(result))
    result_payload = load_json(result)
    adjudication = result_payload.get("adjudication")
    if not isinstance(adjudication, dict) or adjudication.get("semantic_result") == "INCOMPLETE":
        raise ValueError("selected smoke session did not produce a complete report")
    request = load_json(session / "input" / "review-request.json")
    if result_payload.get("request") != request:
        raise ValueError("result request does not match the frozen session request")
    if (
        request.get("repository") != pathlib.Path(str(task.get("repository"))).name
        or request.get("base_commit") != task.get("base")
        or request.get("target_commit") != task.get("target")
        or request.get("diff_selection_reason") != task.get("diff_reason")
    ):
        raise ValueError("session request does not match the selected blind task")
    session_metadata = load_json(session / "input" / "session-metadata.json")
    if session_metadata.get("repository_root") != task.get("repository"):
        raise ValueError("session metadata does not identify the materialized repository root")
    markdown = result.with_name("review-result.md")
    if markdown.is_symlink() or not markdown.is_file():
        raise ValueError("finalized Markdown report is missing")
    if run(str(binary), "render", str(result)).encode() != markdown.read_bytes():
        raise ValueError("saved Markdown does not match deterministic rendering")

    command = [
        str(binary),
        "replay",
        "record",
        "--cases",
        str(workspace / "private" / "cases.json"),
        "--case-id",
        str(mapping["case_id"]),
        "--host",
        str(mapping["host"]),
        "--run-number",
        str(mapping["run_number"]),
        "--result",
        str(result),
        "--human-status",
        args.human_status,
    ]
    if args.overturn_reason:
        command.extend(["--overturn-reason", args.overturn_reason])
    command.extend(
        [
            "--input-tokens",
            str(args.input_tokens),
            "--output-tokens",
            str(args.output_tokens),
            "--duration-ms",
            str(args.duration_ms),
        ]
    )
    record_raw = run(*command)
    record = json.loads(record_raw)
    if record_path.exists():
        previous = load_json(record_path)
        previous_human = previous.get("human_review")
        if not isinstance(previous_human, dict) or previous_human.get("status") != "pending":
            raise ValueError("a completed human decision cannot be overwritten")
        previous_without_human = {key: value for key, value in previous.items() if key != "human_review"}
        record_without_human = {key: value for key, value in record.items() if key != "human_review"}
        if previous_without_human != record_without_human:
            raise ValueError("re-collection cannot change the observed run evidence")

    replay_directory = workspace / "replay-records"
    replay_directory.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".qualification-replay-", dir=workspace) as staging_raw:
        staging = pathlib.Path(staging_raw)
        for path in replay_directory.glob("*.json"):
            if path.name == record_path.name:
                continue
            if path.is_symlink() or not path.is_file():
                raise ValueError(f"invalid existing replay record: {path.name}")
            shutil.copyfile(path, staging / path.name)
        (staging / record_path.name).write_text(json.dumps(record, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        summary_raw = run(
            str(binary),
            "replay",
            "summarize",
            "--cases",
            str(workspace / "private" / "cases.json"),
            "--records",
            str(staging),
        )
    summary = json.loads(summary_raw)
    write_atomically(record_path, json.dumps(record, indent=2, sort_keys=True) + "\n")
    write_atomically(workspace / "smoke-summary.json", json.dumps(summary, indent=2, sort_keys=True) + "\n")
    evidence = {
        "schema_version": 1,
        "run_id": args.run_id,
        "host": mapping["host"],
        "host_version": baseline["codex_version"],
        "model": QUALIFICATION_MODEL,
        "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
        "task": task_relative,
        "session": session.relative_to(workspace).as_posix(),
        "main_review_sha256": file_sha256(session / "output" / "main-review.json"),
        "result_sha256": file_sha256(result),
        "markdown_sha256": file_sha256(markdown),
        "transcript": transcript.relative_to(workspace).as_posix(),
        "transcript_sha256": file_sha256(transcript),
        "runner_metadata": runner_metadata_path.relative_to(workspace).as_posix(),
        "runner_metadata_sha256": file_sha256(runner_metadata_path),
        "input_tokens": args.input_tokens,
        "output_tokens": args.output_tokens,
        "duration_ms": args.duration_ms,
        "collected_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    }
    write_atomically(
        workspace / "run-evidence" / f"{args.run_id}.json",
        json.dumps(evidence, indent=2, sort_keys=True) + "\n",
    )
    if args.human_status != "pending":
        human_review = {
            "schema_version": 1,
            "run_id": args.run_id,
            "reviewer": args.reviewer,
            "reviewed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "status": args.human_status,
            "note": args.review_note,
            "overturn_reason": args.overturn_reason,
            "result_sha256": evidence["result_sha256"],
        }
        write_atomically(
            workspace / "human-reviews" / f"{args.run_id}.json",
            json.dumps(human_review, indent=2, sort_keys=True) + "\n",
        )
    progress = {
        "schema_version": 1,
        "planned_runs": baseline["planned_runs"],
        "completed_runs": summary["valid_records"],
        "metrics_available": summary["metrics_available"],
        "report_only_smoke_complete": summary["report_only_smoke_complete"],
    }
    write_atomically(workspace / "progress.json", json.dumps(progress, indent=2, sort_keys=True) + "\n")
    json.dump(
        {
            "run_id": args.run_id,
            "record": str(record_path),
            "semantic_result": record["observed"]["semantic_result"],
            "human_status": args.human_status,
            "report_only_smoke_complete": summary["report_only_smoke_complete"],
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
