#!/usr/bin/env python3
"""Collect one immutable blind historical-model observation."""

from __future__ import annotations

import argparse
import datetime
import json
import pathlib
import sys

from qualification_collect import load_json, run, write_atomically
from qualification_initialize import file_sha256
from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT, codex_command


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--result", required=True, type=pathlib.Path)
    parser.add_argument("--transcript", required=True, type=pathlib.Path)
    parser.add_argument("--runner-metadata", required=True, type=pathlib.Path)
    parser.add_argument("--input-tokens", required=True, type=int)
    parser.add_argument("--output-tokens", required=True, type=int)
    parser.add_argument("--duration-ms", required=True, type=int)
    args = parser.parse_args()

    if min(args.input_tokens, args.output_tokens, args.duration_ms) < 0:
        raise ValueError("metrics must be non-negative")
    workspace = args.workspace.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if (
        baseline.get("profile") != "report_only_historical_pilot"
        or baseline.get("source_dirty") is not False
        or baseline.get("development_only") is not False
        or baseline.get("qualification_model") != QUALIFICATION_MODEL
        or baseline.get("qualification_reasoning_effort") != QUALIFICATION_REASONING_EFFORT
    ):
        raise ValueError("workspace is not a frozen formal historical pilot")

    operator = load_json(workspace / "operator-manifest.json")
    mappings = operator.get("runs")
    if not isinstance(mappings, list):
        raise ValueError("operator manifest is invalid")
    matches = [item for item in mappings if isinstance(item, dict) and item.get("run_id") == args.run_id]
    if len(matches) != 1:
        raise ValueError("run ID is unknown or duplicated")
    mapping = matches[0]
    task_relative = mapping.get("task")
    if mapping.get("host") != "codex" or task_relative != f"tasks/codex/{args.run_id}.json":
        raise ValueError("operator task mapping is invalid")
    task = load_json(workspace / str(task_relative))

    expected_root = (workspace / "sessions" / args.run_id).resolve()
    result = args.result.resolve(strict=True)
    if (
        result.is_symlink()
        or not result.is_file()
        or result.name != "review-result.json"
        or result.parent.name != "output"
        or result.parent.parent.parent != expected_root
        or not result.parent.parent.name.startswith("review-")
    ):
        raise ValueError("result is outside the selected historical run")
    session = result.parent.parent
    if task.get("output_root") != str(expected_root):
        raise ValueError("task output root is invalid")

    transcript = args.transcript.resolve(strict=True)
    runner_metadata_path = args.runner_metadata.resolve(strict=True)
    if (
        transcript.is_symlink()
        or not transcript.is_file()
        or expected_root not in transcript.parents
        or runner_metadata_path.is_symlink()
        or not runner_metadata_path.is_file()
        or expected_root not in runner_metadata_path.parents
    ):
        raise ValueError("runner evidence is outside the selected historical run")
    runner = load_json(runner_metadata_path)
    if (
        runner.get("run_id") != args.run_id
        or runner.get("host") != "codex"
        or runner.get("host_version") != baseline.get("codex_version")
        or runner.get("model") != QUALIFICATION_MODEL
        or runner.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or runner.get("command") != codex_command(expected_root)
        or runner.get("returncode") != 0
        or runner.get("duration_ms") != args.duration_ms
        or runner.get("stdout") != transcript.name
    ):
        raise ValueError("runner metadata does not prove a successful Terra/high Codex run")

    binary = workspace / "quality-review"
    finalized = json.loads(run(str(binary), "finalize", "--session", str(session)))
    if finalized.get("status") not in {"COMPLETE", "INCOMPLETE"} or pathlib.Path(str(finalized.get("result_path"))).resolve() != result:
        raise ValueError("historical session did not finalize to the selected result")
    run(str(binary), "validate", str(result))
    payload = load_json(result)
    request = load_json(session / "input" / "review-request.json")
    if payload.get("request") != request:
        raise ValueError("result request does not match its frozen session")
    if (
        request.get("repository") != pathlib.Path(str(task.get("repository"))).name
        or request.get("base_commit") != task.get("base")
        or request.get("target_commit") != task.get("target")
        or request.get("diff_selection_reason") != task.get("diff_reason")
    ):
        raise ValueError("session request does not match its blind task")
    markdown = result.with_name("review-result.md")
    if markdown.is_symlink() or not markdown.is_file() or run(str(binary), "render", str(result)).encode() != markdown.read_bytes():
        raise ValueError("historical Markdown report is missing or stale")

    observation_path = workspace / "observations" / f"{args.run_id}.json"
    evidence_path = workspace / "run-evidence" / f"{args.run_id}.json"
    if observation_path.exists() or evidence_path.exists():
        raise ValueError("historical run evidence is immutable and already exists")
    findings = payload.get("findings")
    execution = payload.get("execution")
    adjudication = payload.get("adjudication")
    if not isinstance(findings, list) or not isinstance(execution, dict) or not isinstance(adjudication, dict):
        raise ValueError("validated result has an invalid structure")
    rule_ids = sorted(
        {
            str(item["candidate"]["rule_id"])
            for item in findings
            if isinstance(item, dict)
            and isinstance(item.get("candidate"), dict)
            and isinstance(item["candidate"].get("rule_id"), str)
        }
    )
    main_review = session / "output" / "main-review.json"
    main_review_sha = file_sha256(main_review) if main_review.is_file() and not main_review.is_symlink() else None
    verifier = session / "output" / "verifier-review.json"
    verifier_sha = file_sha256(verifier) if verifier.is_file() and not verifier.is_symlink() else None
    observation = {
        "schema_version": 1,
        "run_id": args.run_id,
        "change_id": mapping["change_id"],
        "project_id": mapping["project_id"],
        "semantic_result": adjudication["semantic_result"],
        "finding_count": len(findings),
        "rule_ids": rule_ids,
        "agent_count": execution["agent_count"],
        "verifier_count": execution["verifier_count"],
        "missing_context": payload.get("missing_context", []),
        "uninspected_scope": payload.get("uninspected_scope", []),
        "input_tokens": args.input_tokens,
        "output_tokens": args.output_tokens,
        "duration_ms": args.duration_ms,
        "result_sha256": file_sha256(result),
    }
    evidence = {
        "schema_version": 1,
        "run_id": args.run_id,
        "change_id": mapping["change_id"],
        "host": "codex",
        "host_version": baseline["codex_version"],
        "model": QUALIFICATION_MODEL,
        "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
        "task": task_relative,
        "session": session.relative_to(workspace).as_posix(),
        "input_manifest_sha256": file_sha256(session / "input-manifest.json"),
        "main_review_sha256": main_review_sha,
        "verifier_review_sha256": verifier_sha,
        "result_sha256": observation["result_sha256"],
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
    write_atomically(observation_path, json.dumps(observation, indent=2, sort_keys=True) + "\n")
    write_atomically(evidence_path, json.dumps(evidence, indent=2, sort_keys=True) + "\n")

    executed = len(list((workspace / "observations").glob("*.json")))
    reviewed = len(list((workspace / "records").glob("*.json")))
    progress = {
        "schema_version": 1,
        "planned_runs": baseline["planned_runs"],
        "executed_runs": executed,
        "reviewed_runs": reviewed,
        "historical_evidence_complete": False,
    }
    write_atomically(workspace / "progress.json", json.dumps(progress, indent=2, sort_keys=True) + "\n")
    json.dump(
        {
            "run_id": args.run_id,
            "change_id": mapping["change_id"],
            "semantic_result": observation["semantic_result"],
            "observation": str(observation_path),
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
