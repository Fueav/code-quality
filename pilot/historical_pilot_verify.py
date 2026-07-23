#!/usr/bin/env python3
"""Verify a frozen historical pilot workspace and its immutable evidence."""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys

from historical_pilot import load_json, summarize, validate_manifest, validate_record
from historical_pilot_initialize import builtin_task_markdown, task_markdown
from qualification_collect import write_atomically
from qualification_initialize import file_sha256, tree_sha256
from qualification_run import (
    QUALIFICATION_MODEL,
    QUALIFICATION_REASONING_EFFORT,
    builtin_codex_command,
    builtin_result,
    codex_command,
    codex_metrics,
)
from qualification_verify import resolve_relative, run, run_bytes


def git(repository: pathlib.Path, *arguments: str) -> str:
    return run("git", "-C", str(repository), *arguments)


def expected_observation(
    run_id: str,
    mapping: dict[str, object],
    payload: dict[str, object],
    input_tokens: int,
    output_tokens: int,
    duration_ms: int,
    result_sha256: str,
    builtin_payload: dict[str, object],
    builtin_input_tokens: int,
    builtin_output_tokens: int,
    builtin_duration_ms: int,
    builtin_result_sha256: str,
) -> dict[str, object]:
    findings = payload["findings"]
    execution = payload["execution"]
    adjudication = payload["adjudication"]
    rule_ids = sorted(
        {
            str(item["candidate"]["rule_id"])
            for item in findings
            if isinstance(item, dict)
            and isinstance(item.get("candidate"), dict)
            and isinstance(item["candidate"].get("rule_id"), str)
        }
    )
    return {
        "schema_version": 1,
        "run_id": run_id,
        "change_id": mapping["change_id"],
        "project_id": mapping["project_id"],
        "skill": {
            "semantic_result": adjudication["semantic_result"],
            "finding_count": len(findings),
            "rule_ids": rule_ids,
            "agent_count": execution["agent_count"],
            "verifier_count": execution["verifier_count"],
            "missing_context": payload.get("missing_context", []),
            "uninspected_scope": payload.get("uninspected_scope", []),
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "duration_ms": duration_ms,
            "result_sha256": result_sha256,
        },
        "builtin": {
            "semantic_result": "MANUAL_REVIEW" if builtin_payload["findings"] else "PASS",
            "finding_count": len(builtin_payload["findings"]),
            "findings": builtin_payload["findings"],
            "input_tokens": builtin_input_tokens,
            "output_tokens": builtin_output_tokens,
            "duration_ms": builtin_duration_ms,
            "result_sha256": builtin_result_sha256,
        },
    }


def verify_run(
    workspace: pathlib.Path,
    binary: pathlib.Path,
    baseline: dict[str, object],
    mapping: dict[str, object],
) -> None:
    run_id = str(mapping["run_id"])
    observation = load_json(workspace / "observations" / f"{run_id}.json")
    evidence = load_json(workspace / "run-evidence" / f"{run_id}.json")
    task = load_json(workspace / str(mapping["task"]))
    skill_evidence = evidence.get("skill")
    builtin_evidence = evidence.get("builtin")
    if not isinstance(skill_evidence, dict) or not isinstance(builtin_evidence, dict):
        raise ValueError(f"paired run evidence is missing: {run_id}")
    session = resolve_relative(workspace, skill_evidence.get("session"), f"{run_id}.session", directory=True)
    transcript = resolve_relative(workspace, skill_evidence.get("transcript"), f"{run_id}.transcript")
    runner_path = resolve_relative(workspace, skill_evidence.get("runner_metadata"), f"{run_id}.runner_metadata")
    runner = load_json(runner_path)
    expected_root = workspace / "sessions" / run_id
    if session.parent != expected_root or not session.name.startswith("review-"):
        raise ValueError(f"session mapping is invalid: {run_id}")
    if (
        evidence.get("schema_version") != 1
        or evidence.get("run_id") != run_id
        or evidence.get("change_id") != mapping.get("change_id")
        or evidence.get("host") != "codex"
        or evidence.get("host_version") != baseline.get("codex_version")
        or evidence.get("model") != QUALIFICATION_MODEL
        or evidence.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or evidence.get("task") != mapping.get("task")
    ):
        raise ValueError(f"run evidence identity is invalid: {run_id}")
    if (
        runner.get("run_id") != run_id
        or runner.get("host") != "codex"
        or runner.get("host_version") != baseline.get("codex_version")
        or runner.get("model") != QUALIFICATION_MODEL
        or runner.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or runner.get("command") != codex_command(expected_root)
        or runner.get("returncode") != 0
        or runner.get("stdout") != transcript.name
    ):
        raise ValueError(f"runner metadata is invalid: {run_id}")
    input_tokens, output_tokens = codex_metrics(transcript.read_text(encoding="utf-8"))
    duration_ms = runner.get("duration_ms")
    if not isinstance(duration_ms, int) or duration_ms < 0:
        raise ValueError(f"runner duration is invalid: {run_id}")
    if (
        skill_evidence.get("input_tokens") != input_tokens
        or skill_evidence.get("output_tokens") != output_tokens
        or skill_evidence.get("duration_ms") != duration_ms
    ):
        raise ValueError(f"run metrics are invalid: {run_id}")

    result = session / "output" / "review-result.json"
    markdown = result.with_name("review-result.md")
    main_review = result.with_name("main-review.json")
    for path in (result, markdown):
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"run evidence file is missing: {run_id}: {path.name}")
    main_review_sha = file_sha256(main_review) if main_review.is_file() and not main_review.is_symlink() else None
    if (
        skill_evidence.get("main_review_sha256") != main_review_sha
        or skill_evidence.get("result_sha256") != file_sha256(result)
        or skill_evidence.get("markdown_sha256") != file_sha256(markdown)
        or skill_evidence.get("transcript_sha256") != file_sha256(transcript)
        or skill_evidence.get("runner_metadata_sha256") != file_sha256(runner_path)
    ):
        raise ValueError(f"run evidence digest mismatch: {run_id}")
    run(str(binary), "validate", str(result))
    if run_bytes(str(binary), "render", str(result)) != markdown.read_bytes():
        raise ValueError(f"rendered report is stale: {run_id}")
    payload = load_json(result)
    if (session / "input" / "repository").exists():
        raise ValueError(f"finalized session retained its temporary worktree: {run_id}")
    request = load_json(session / "input" / "review-request.json")
    if payload.get("request") != request:
        raise ValueError(f"result request mismatch: {run_id}")
    if (
        request.get("repository") != pathlib.Path(str(task["repository"])).name
        or request.get("base_commit") != task.get("base")
        or request.get("target_commit") != task.get("target")
        or request.get("diff_selection_reason") != task.get("diff_reason")
    ):
        raise ValueError(f"task request mismatch: {run_id}")
    metadata = load_json(session / "input" / "session-metadata.json")
    if metadata.get("repository_root") != task.get("repository"):
        raise ValueError(f"session repository root mismatch: {run_id}")

    builtin_result_path = resolve_relative(workspace, builtin_evidence.get("result"), f"{run_id}.builtin_result")
    builtin_transcript = resolve_relative(workspace, builtin_evidence.get("transcript"), f"{run_id}.builtin_transcript")
    builtin_runner_path = resolve_relative(workspace, builtin_evidence.get("runner_metadata"), f"{run_id}.builtin_runner")
    builtin_runner = load_json(builtin_runner_path)
    builtin_worktree = workspace / "sessions" / run_id / "operator" / f"builtin-attempt-{builtin_runner.get('attempt')}.worktree"
    if (
        builtin_runner.get("schema_version") != 1
        or builtin_runner.get("run_id") != run_id
        or builtin_runner.get("lane") != "builtin"
        or builtin_runner.get("host") != "codex"
        or builtin_runner.get("host_version") != baseline.get("codex_version")
        or builtin_runner.get("model") != QUALIFICATION_MODEL
        or builtin_runner.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or builtin_runner.get("command")
        != builtin_codex_command(
            builtin_worktree,
            str(task["target"]),
            workspace / "builtin-review.schema.json",
        )
        or builtin_runner.get("returncode") != 0
        or builtin_runner.get("stdout") != builtin_transcript.name
        or builtin_worktree.exists()
    ):
        raise ValueError(f"built-in runner metadata is invalid: {run_id}")
    builtin_input_tokens, builtin_output_tokens = codex_metrics(builtin_transcript.read_text(encoding="utf-8"))
    builtin_duration_ms = builtin_runner.get("duration_ms")
    if not isinstance(builtin_duration_ms, int) or builtin_duration_ms < 0:
        raise ValueError(f"built-in runner duration is invalid: {run_id}")
    builtin_payload = load_json(builtin_result_path)
    if builtin_result(builtin_transcript.read_text(encoding="utf-8")) != builtin_payload:
        raise ValueError(f"built-in result does not match its transcript: {run_id}")
    if (
        builtin_evidence.get("result_sha256") != file_sha256(builtin_result_path)
        or builtin_evidence.get("transcript_sha256") != file_sha256(builtin_transcript)
        or builtin_evidence.get("runner_metadata_sha256") != file_sha256(builtin_runner_path)
        or builtin_evidence.get("input_tokens") != builtin_input_tokens
        or builtin_evidence.get("output_tokens") != builtin_output_tokens
        or builtin_evidence.get("duration_ms") != builtin_duration_ms
    ):
        raise ValueError(f"built-in evidence mismatch: {run_id}")
    expected = expected_observation(
        run_id,
        mapping,
        payload,
        input_tokens,
        output_tokens,
        duration_ms,
        file_sha256(result),
        builtin_payload,
        builtin_input_tokens,
        builtin_output_tokens,
        builtin_duration_ms,
        file_sha256(builtin_result_path),
    )
    if observation != expected:
        raise ValueError(f"saved observation is stale: {run_id}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    parser.add_argument("--write-summary", action="store_true")
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    source = args.source.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if (
        baseline.get("schema_version") != 1
        or baseline.get("profile") != "report_only_historical_pilot"
        or baseline.get("source_dirty") is not False
        or baseline.get("development_only") is not False
        or baseline.get("qualification_model") != QUALIFICATION_MODEL
        or baseline.get("qualification_reasoning_effort") != QUALIFICATION_REASONING_EFFORT
        or baseline.get("builtin_baseline") != "ordinary_codex_review_without_skill"
        or baseline.get("builtin_schema_sha256") != file_sha256(workspace / "builtin-review.schema.json")
    ):
        raise ValueError("baseline identity is invalid")
    if run("git", "rev-parse", "HEAD^{commit}", cwd=source) != baseline.get("source_commit"):
        raise ValueError("source checkout no longer matches the frozen commit")
    if run("git", "status", "--porcelain=v1", "--untracked-files=all", cwd=source):
        raise ValueError("source checkout is dirty")
    binary = workspace / "quality-review"
    plugin = workspace / "plugin" / "code-quality"
    if file_sha256(binary) != baseline.get("binary_sha256") or tree_sha256(plugin) != baseline.get("plugin_sha256"):
        raise ValueError("frozen binary or Skill digest mismatch")

    manifest_path = workspace / "private" / "manifest.json"
    if file_sha256(manifest_path) != baseline.get("manifest_sha256"):
        raise ValueError("frozen historical manifest digest mismatch")
    manifest = load_json(manifest_path)
    changes = validate_manifest(manifest)
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list) or len(runs) != len(changes) or baseline.get("planned_runs") != len(changes):
        raise ValueError("historical run matrix size is invalid")
    if baseline.get("host_totals") != {"codex": len(changes)}:
        raise ValueError("historical host schedule is invalid")
    seen_runs: set[str] = set()
    seen_changes: set[str] = set()
    mappings: dict[str, dict[str, object]] = {}
    for mapping in runs:
        if not isinstance(mapping, dict):
            raise ValueError("operator run mapping is invalid")
        run_id = mapping.get("run_id")
        change_id = mapping.get("change_id")
        if (
            not isinstance(run_id, str)
            or run_id in seen_runs
            or not isinstance(change_id, str)
            or change_id in seen_changes
            or change_id not in changes
            or mapping.get("host") != "codex"
            or mapping.get("task") != f"tasks/codex/{run_id}.json"
        ):
            raise ValueError("operator run identity is invalid")
        seen_runs.add(run_id)
        seen_changes.add(change_id)
        mappings[run_id] = mapping
        change = changes[change_id]
        if (
            mapping.get("project_id") != change.get("project_id")
            or mapping.get("ground_truth") != change.get("ground_truth")
            or mapping.get("labeler") != change.get("labeler")
            or mapping.get("label_note") != change.get("label_note")
        ):
            raise ValueError(f"operator label mapping is invalid: {run_id}")
        task_path = workspace / str(mapping["task"])
        task = load_json(task_path)
        repository = workspace / "repositories" / str(mapping["repository_id"])
        if (
            task.get("schema_version") != 1
            or task.get("run_id") != run_id
            or task.get("host") != "codex"
            or task.get("repository") != str(repository)
            or task.get("base") != change.get("base_commit")
            or task.get("target") != change.get("target_commit")
            or task.get("diff_reason") != f"blind historical committed change {run_id}"
            or task.get("output_root") != str(workspace / "sessions" / run_id)
        ):
            raise ValueError(f"blind task mapping is invalid: {run_id}")
        prompt_path = task_path.with_suffix(".md")
        prompt = prompt_path.read_text(encoding="utf-8")
        expected_prompt = task_markdown(
            task,
            workspace / "plugin" / "code-quality" / "skills" / "code-quality" / "SKILL.md",
            binary,
        )
        if prompt != expected_prompt:
            raise ValueError(f"blind task prompt is stale: {run_id}")
        builtin_prompt_path = task_path.with_name(f"{run_id}.builtin.md")
        builtin_prompt = builtin_prompt_path.read_text(encoding="utf-8")
        if builtin_prompt != builtin_task_markdown(task):
            raise ValueError(f"blind built-in task prompt is stale: {run_id}")
        serialized = json.dumps(task, sort_keys=True) + prompt + builtin_prompt
        if change_id in serialized or str(change["project_id"]) in serialized or str(change["ground_truth"]) in serialized:
            raise ValueError(f"blind task leaks private identity: {run_id}")
        if repository.is_symlink() or not repository.is_dir():
            raise ValueError(f"materialized repository is invalid: {run_id}")
        if (
            git(repository, "rev-parse", "HEAD") != task["target"]
            or git(repository, "rev-parse", f"{task['target']}^1") != task["base"]
            or git(repository, "rev-parse", f"{task['base']}^{{tree}}") != mapping["base_tree"]
            or git(repository, "rev-parse", f"{task['target']}^{{tree}}") != mapping["target_tree"]
            or git(repository, "status", "--porcelain=v1", "--untracked-files=all")
        ):
            raise ValueError(f"materialized repository content is invalid: {run_id}")
        registered_worktrees = [
            line.removeprefix("worktree ")
            for line in git(repository, "worktree", "list", "--porcelain").splitlines()
            if line.startswith("worktree ")
        ]
        if registered_worktrees != [str(repository)]:
            raise ValueError(f"temporary review worktree was not removed: {run_id}")
        fsck = subprocess.run(
            ["git", "-C", str(repository), "fsck", "--no-reflogs", "--unreachable"],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        if fsck.returncode != 0 or any("commit" in line for line in (fsck.stdout + fsck.stderr).splitlines()):
            raise ValueError(f"materialized repository exposes an unexpected commit: {run_id}")

    observation_ids = {path.stem for path in (workspace / "observations").glob("*.json") if path.is_file()}
    evidence_ids = {path.stem for path in (workspace / "run-evidence").glob("*.json") if path.is_file()}
    if observation_ids != evidence_ids or not observation_ids.issubset(seen_runs):
        raise ValueError("observation and run-evidence directories are inconsistent")
    for run_id in sorted(observation_ids):
        verify_run(workspace, binary, baseline, mappings[run_id])

    fresh_summary = summarize(manifest, workspace / "records")
    record_ids = {path.stem for path in (workspace / "records").glob("*.json") if path.is_file()}
    human_ids = {path.stem for path in (workspace / "human-reviews").glob("*.json") if path.is_file()}
    reviewed_runs: set[str] = set()
    for run_id, mapping in mappings.items():
        change_id = str(mapping["change_id"])
        if change_id not in record_ids:
            continue
        reviewed_runs.add(run_id)
        record = load_json(workspace / "records" / f"{change_id}.json")
        observation = load_json(workspace / "observations" / f"{run_id}.json")
        human = load_json(workspace / "human-reviews" / f"{run_id}.json")
        errors = validate_record(record, changes[change_id])
        skill_record = record.get("skill")
        builtin_record = record.get("builtin")
        skill_observation = observation.get("skill")
        builtin_observation = observation.get("builtin")
        skill_human = human.get("skill")
        builtin_human = human.get("builtin")
        if errors or (
            not isinstance(skill_record, dict)
            or not isinstance(builtin_record, dict)
            or not isinstance(skill_observation, dict)
            or not isinstance(builtin_observation, dict)
            or not isinstance(skill_human, dict)
            or not isinstance(builtin_human, dict)
            or any(
                record[lane].get(field) != observation[lane].get(field)
                for lane in ("skill", "builtin")
                for field in ("semantic_result", "input_tokens", "output_tokens", "duration_ms")
            )
            or human.get("run_id") != run_id
            or human.get("change_id") != change_id
            or human.get("reviewer") != record.get("reviewer")
            or human.get("note") != record.get("review_note")
            or any(
                human[lane].get(field) != record[lane].get(field)
                for lane in ("skill", "builtin")
                for field in ("core_issue_found", "report_actionable")
            )
            or any(
                human[lane].get("result_sha256") != observation[lane].get("result_sha256")
                for lane in ("skill", "builtin")
            )
        ):
            raise ValueError(f"maintainer record is invalid: {change_id}")
    if record_ids != {str(mappings[run_id]["change_id"]) for run_id in reviewed_runs} or human_ids != reviewed_runs:
        raise ValueError("maintainer records contain missing or orphaned artifacts")
    if (workspace / "summary.json").exists() and load_json(workspace / "summary.json") != fresh_summary:
        raise ValueError("saved historical summary is stale")
    expected_progress = {
        "schema_version": 1,
        "planned_runs": len(changes),
        "executed_runs": len(observation_ids),
        "reviewed_runs": fresh_summary["valid_records"],
        "historical_evidence_complete": fresh_summary["historical_evidence_complete"],
    }
    if load_json(workspace / "progress.json") != expected_progress:
        raise ValueError("saved historical progress is stale")
    if args.write_summary:
        write_atomically(workspace / "summary.json", json.dumps(fresh_summary, indent=2, sort_keys=True) + "\n")
    json.dump(
        {
            "schema_version": 1,
            "status": "valid",
            "source_commit": baseline["source_commit"],
            "projects": len(manifest["projects"]),
            "planned_runs": len(changes),
            "executed_runs": len(observation_ids),
            "reviewed_runs": fresh_summary["valid_records"],
            "historical_evidence_complete": fresh_summary["historical_evidence_complete"],
            "summary": str(workspace / "summary.json") if args.write_summary else None,
        },
        sys.stdout,
        indent=2,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
