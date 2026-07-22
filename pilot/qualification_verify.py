#!/usr/bin/env python3
"""Verify a frozen blind qualification workspace and its task mapping."""

from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

from qualification_initialize import file_sha256, task_markdown, tree_sha256
from qualification_matrix import build_plan, load_cases
from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT, codex_command, codex_metrics


FORBIDDEN_TASK_PATTERN = re.compile(r"positive|counterexample|insufficient|(?:DES|COR|REL|SEC|CHG)-", re.IGNORECASE)


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def run(*args: str, cwd: pathlib.Path | None = None) -> str:
    completed = subprocess.run(
        list(args),
        cwd=cwd,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:2])}): {completed.stderr.strip()}")
    return completed.stdout.strip()


def run_bytes(*args: str) -> bytes:
    completed = subprocess.run(list(args), check=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:2])}): {completed.stderr.decode(errors='replace').strip()}")
    return completed.stdout


def tracked_tree(repository: pathlib.Path, commit: str) -> dict[str, bytes]:
    raw = subprocess.run(
        ["git", "-C", str(repository), "ls-tree", "-rz", "--name-only", commit],
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    result: dict[str, bytes] = {}
    for encoded in raw.split(b"\0"):
        if not encoded:
            continue
        path = encoded.decode()
        result[path] = subprocess.run(
            ["git", "-C", str(repository), "show", f"{commit}:{path}"],
            check=True,
            stdout=subprocess.PIPE,
        ).stdout
    return result


def source_tree(root: pathlib.Path) -> dict[str, bytes]:
    result: dict[str, bytes] = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"fixture contains symlink: {path}")
        if path.is_file():
            result[path.relative_to(root).as_posix()] = path.read_bytes()
    return result


def resolve_relative(root: pathlib.Path, value: object, field: str, *, directory: bool = False) -> pathlib.Path:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{field} must be a non-empty relative path")
    relative = pathlib.PurePosixPath(value)
    if relative.is_absolute() or ".." in relative.parts or "." in relative.parts:
        raise ValueError(f"{field} must be a clean relative POSIX path")
    current = root
    for part in relative.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError(f"{field} must not traverse a symlink")
    if directory and not current.is_dir():
        raise ValueError(f"{field} must identify a directory")
    if not directory and not current.is_file():
        raise ValueError(f"{field} must identify a regular file")
    return current


def write_json_atomically(path: pathlib.Path, value: object) -> None:
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


def percentile_nearest_rank(values: list[int], percentage: int) -> int | None:
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, (len(ordered) * percentage + 99) // 100)
    return ordered[rank - 1]


def verify_replay_evidence(
    workspace: pathlib.Path,
    binary: pathlib.Path,
    cases_path: pathlib.Path,
    baseline: dict[str, object],
    mapping_by_run: dict[str, dict[str, object]],
) -> tuple[dict[str, object], dict[str, object]]:
    replay_directory = workspace / "replay-records"
    evidence_directory = workspace / "run-evidence"
    human_directory = workspace / "human-reviews"
    for name, directory in (
        ("replay-records", replay_directory),
        ("run-evidence", evidence_directory),
        ("human-reviews", human_directory),
    ):
        if directory.is_symlink() or not directory.is_dir():
            raise ValueError(f"{name} must be a non-symlink directory")

    verified_runs: set[str] = set()
    human_statuses = {"pending": 0, "confirmed": 0, "overturned": 0}
    semantic_results: dict[str, int] = {}
    durations: list[int] = []
    total_input_tokens = 0
    total_output_tokens = 0
    for record_path in sorted(replay_directory.glob("*.json")):
        if record_path.is_symlink() or not record_path.is_file():
            raise ValueError(f"invalid replay record: {record_path.name}")
        run_id = record_path.stem
        mapping = mapping_by_run.get(run_id)
        if mapping is None or run_id in verified_runs:
            raise ValueError(f"replay record has an unknown or duplicated run ID: {run_id}")
        verified_runs.add(run_id)
        record = load_json(record_path)
        evidence = load_json(evidence_directory / f"{run_id}.json")
        task_relative = mapping.get("task")
        if evidence.get("schema_version") != 1 or evidence.get("run_id") != run_id:
            raise ValueError(f"run evidence identity is invalid: {run_id}")
        if (
            evidence.get("host") != "codex"
            or evidence.get("host_version") != baseline.get("codex_version")
            or evidence.get("model") != QUALIFICATION_MODEL
            or evidence.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
            or evidence.get("task") != task_relative
        ):
            raise ValueError(f"run evidence host contract is invalid: {run_id}")

        task = load_json(workspace / str(task_relative))
        session = resolve_relative(workspace, evidence.get("session"), f"{run_id}.session", directory=True)
        if session.parent != workspace / "sessions" / run_id or not session.name.startswith("review-"):
            raise ValueError(f"run evidence session mapping is invalid: {run_id}")
        transcript = resolve_relative(workspace, evidence.get("transcript"), f"{run_id}.transcript")
        runner_metadata_path = resolve_relative(
            workspace,
            evidence.get("runner_metadata"),
            f"{run_id}.runner_metadata",
        )
        operator_root = workspace / "sessions" / run_id / "operator"
        if transcript.parent != operator_root or runner_metadata_path.parent != operator_root:
            raise ValueError(f"operator evidence is outside the selected run root: {run_id}")
        runner_metadata = load_json(runner_metadata_path)
        if (
            runner_metadata.get("schema_version") != 1
            or runner_metadata.get("run_id") != run_id
            or runner_metadata.get("host") != "codex"
            or runner_metadata.get("host_version") != baseline.get("codex_version")
            or runner_metadata.get("model") != QUALIFICATION_MODEL
            or runner_metadata.get("reasoning_effort") != QUALIFICATION_REASONING_EFFORT
            or runner_metadata.get("command") != codex_command(workspace / "sessions" / run_id)
            or runner_metadata.get("returncode") != 0
            or not isinstance(runner_metadata.get("attempt"), int)
            or int(runner_metadata["attempt"]) < 1
            or runner_metadata.get("stdout") != transcript.name
        ):
            raise ValueError(f"runner metadata is invalid: {run_id}")
        stderr_name = runner_metadata.get("stderr")
        if not isinstance(stderr_name, str) or pathlib.PurePath(stderr_name).name != stderr_name:
            raise ValueError(f"runner stderr mapping is invalid: {run_id}")
        stderr_path = runner_metadata_path.with_name(stderr_name)
        if stderr_path.is_symlink() or not stderr_path.is_file():
            raise ValueError(f"runner stderr evidence is missing: {run_id}")

        input_tokens, output_tokens = codex_metrics(transcript.read_text(encoding="utf-8"))
        duration_ms = runner_metadata.get("duration_ms")
        if not isinstance(duration_ms, int) or duration_ms < 0:
            raise ValueError(f"runner duration is invalid: {run_id}")
        if (
            evidence.get("input_tokens") != input_tokens
            or evidence.get("output_tokens") != output_tokens
            or evidence.get("duration_ms") != duration_ms
        ):
            raise ValueError(f"run metrics do not match the host transcript: {run_id}")

        input_manifest = session / "input-manifest.json"
        main_review = session / "output" / "main-review.json"
        result_path = session / "output" / "review-result.json"
        markdown_path = session / "output" / "review-result.md"
        for field, path in (
            ("input_manifest", input_manifest),
            ("main_review", main_review),
            ("result", result_path),
            ("markdown", markdown_path),
        ):
            if path.is_symlink() or not path.is_file():
                raise ValueError(f"{run_id}.{field} evidence is missing")
        verifier_review = session / "output" / "verifier-review.json"
        if verifier_review.is_symlink():
            raise ValueError(f"verifier evidence must not be a symlink: {run_id}")
        verifier_sha256 = file_sha256(verifier_review) if verifier_review.is_file() and not verifier_review.is_symlink() else None
        if (
            evidence.get("input_manifest_sha256") != file_sha256(input_manifest)
            or evidence.get("main_review_sha256") != file_sha256(main_review)
            or evidence.get("verifier_review_sha256") != verifier_sha256
            or evidence.get("result_sha256") != file_sha256(result_path)
            or evidence.get("markdown_sha256") != file_sha256(markdown_path)
            or evidence.get("transcript_sha256") != file_sha256(transcript)
            or evidence.get("runner_metadata_sha256") != file_sha256(runner_metadata_path)
        ):
            raise ValueError(f"run evidence digest mismatch: {run_id}")

        run(str(binary), "validate", str(result_path))
        if run_bytes(str(binary), "render", str(result_path)) != markdown_path.read_bytes():
            raise ValueError(f"saved Markdown is not deterministic: {run_id}")
        result_payload = load_json(result_path)
        execution = result_payload.get("execution")
        if not isinstance(execution, dict) or execution.get("host") != "codex":
            raise ValueError(f"result host is invalid: {run_id}")
        if execution.get("verifier_count") == 1:
            if verifier_sha256 is None:
                raise ValueError(f"verifier result is missing: {run_id}")
        elif execution.get("verifier_count") == 0:
            if verifier_sha256 is not None:
                raise ValueError(f"unexpected verifier result exists: {run_id}")
        else:
            raise ValueError(f"result verifier count is invalid: {run_id}")
        request = load_json(session / "input" / "review-request.json")
        if result_payload.get("request") != request:
            raise ValueError(f"result request does not match session input: {run_id}")
        if (
            request.get("repository") != pathlib.Path(str(task.get("repository"))).name
            or request.get("base_commit") != task.get("base")
            or request.get("target_commit") != task.get("target")
            or request.get("diff_selection_reason") != task.get("diff_reason")
        ):
            raise ValueError(f"session request does not match blind task: {run_id}")

        observed = record.get("observed")
        human = record.get("human_review")
        if (
            record.get("case_id") != mapping.get("case_id")
            or record.get("host") != "codex"
            or record.get("run_number") != mapping.get("run_number")
            or not isinstance(observed, dict)
            or not isinstance(human, dict)
            or observed.get("input_tokens") != input_tokens
            or observed.get("output_tokens") != output_tokens
            or observed.get("duration_ms") != duration_ms
        ):
            raise ValueError(f"replay record does not match run evidence: {run_id}")
        human_status = human.get("status")
        if human_status not in human_statuses:
            raise ValueError(f"human review status is invalid: {run_id}")
        human_statuses[str(human_status)] += 1
        record_command = [
            str(binary),
            "replay",
            "record",
            "--cases",
            str(cases_path),
            "--case-id",
            str(mapping["case_id"]),
            "--host",
            "codex",
            "--run-number",
            str(mapping["run_number"]),
            "--result",
            str(result_path),
            "--human-status",
            str(human_status),
            "--input-tokens",
            str(input_tokens),
            "--output-tokens",
            str(output_tokens),
            "--duration-ms",
            str(duration_ms),
        ]
        if human_status == "overturned":
            record_command.extend(["--overturn-reason", str(human.get("overturn_reason"))])
        if json.loads(run(*record_command)) != record:
            raise ValueError(f"replay record is not reproducible: {run_id}")

        human_path = human_directory / f"{run_id}.json"
        if human_status == "pending":
            if human_path.exists():
                raise ValueError(f"pending run has a human review artifact: {run_id}")
        else:
            human_artifact = load_json(human_path)
            if (
                human_artifact.get("schema_version") != 1
                or human_artifact.get("run_id") != run_id
                or human_artifact.get("status") != human_status
                or human_artifact.get("overturn_reason") != human.get("overturn_reason")
                or human_artifact.get("result_sha256") != evidence.get("result_sha256")
                or not isinstance(human_artifact.get("reviewer"), str)
                or not str(human_artifact["reviewer"]).strip()
                or not isinstance(human_artifact.get("note"), str)
                or not str(human_artifact["note"]).strip()
            ):
                raise ValueError(f"human review artifact is invalid: {run_id}")

        semantic_result = str(observed.get("semantic_result"))
        semantic_results[semantic_result] = semantic_results.get(semantic_result, 0) + 1
        durations.append(duration_ms)
        total_input_tokens += input_tokens
        total_output_tokens += output_tokens

    evidence_ids = {path.stem for path in evidence_directory.glob("*.json") if path.is_file() and not path.is_symlink()}
    human_ids = {path.stem for path in human_directory.glob("*.json") if path.is_file() and not path.is_symlink()}
    if evidence_ids != verified_runs:
        raise ValueError("run-evidence directory contains missing or orphaned records")
    expected_human_ids: set[str] = set()
    for path in replay_directory.glob("*.json"):
        human_review = load_json(path).get("human_review")
        if isinstance(human_review, dict) and human_review.get("status") != "pending":
            expected_human_ids.add(path.stem)
    if human_ids != expected_human_ids:
        raise ValueError("human-reviews directory contains missing or orphaned records")

    fresh_summary = json.loads(
        run(
            str(binary),
            "replay",
            "summarize",
            "--cases",
            str(cases_path),
            "--records",
            str(replay_directory),
        )
    )
    if load_json(workspace / "qualification-summary.json") != fresh_summary:
        raise ValueError("saved qualification summary is stale")
    expected_progress = {
        "schema_version": 1,
        "planned_runs": 100,
        "completed_runs": fresh_summary["valid_records"],
        "human_confirmed_runs": human_statuses["confirmed"],
        "metrics_available": fresh_summary["metrics_available"],
        "qualification_complete": fresh_summary["qualification_complete"],
    }
    if load_json(workspace / "progress.json") != expected_progress:
        raise ValueError("saved qualification progress is stale")
    if fresh_summary["qualification_complete"] and (
        fresh_summary["valid_records"] != 100
        or fresh_summary["metrics_available"] != 100
        or human_statuses != {"pending": 0, "confirmed": 100, "overturned": 0}
    ):
        raise ValueError("qualification completion is missing required metrics or human confirmations")

    evidence_summary = {
        "schema_version": 1,
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "source_commit": baseline["source_commit"],
        "rc_version": baseline["rc_version"],
        "policy_version": baseline["policy_version"],
        "host": "codex",
        "host_version": baseline["codex_version"],
        "model": QUALIFICATION_MODEL,
        "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
        "planned_runs": 100,
        "valid_records": fresh_summary["valid_records"],
        "invalid_records": fresh_summary["invalid_records"],
        "cases_covered": fresh_summary["cases_covered"],
        "human_statuses": human_statuses,
        "semantic_results": semantic_results,
        "metrics_available": fresh_summary["metrics_available"],
        "total_input_tokens": total_input_tokens,
        "total_output_tokens": total_output_tokens,
        "duration_ms_p50": percentile_nearest_rank(durations, 50),
        "duration_ms_p95": percentile_nearest_rank(durations, 95),
        "duration_ms_max": max(durations) if durations else None,
        "agent_limit_respected": fresh_summary["agent_limit_respected"],
        "qualification_complete": fresh_summary["qualification_complete"],
    }
    return fresh_summary, evidence_summary


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    parser.add_argument("--write-summary", action="store_true")
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    source = args.source.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot qualify")
    if baseline.get("schema_version") != 1 or baseline.get("planned_runs") != 100:
        raise ValueError("baseline identity is invalid")
    if baseline.get("host_totals") != {"codex": 100}:
        raise ValueError("baseline host schedule is invalid")
    if not isinstance(baseline.get("codex_version"), str) or not baseline["codex_version"]:
        raise ValueError("baseline Codex version is missing")
    if (
        baseline.get("qualification_model") != QUALIFICATION_MODEL
        or baseline.get("qualification_reasoning_effort") != QUALIFICATION_REASONING_EFFORT
    ):
        raise ValueError("baseline model or reasoning effort is invalid")
    source_head = run("git", "rev-parse", "HEAD^{commit}", cwd=source)
    if baseline.get("source_commit") != source_head:
        raise ValueError("workspace source commit does not match the current checkout")
    if run("git", "status", "--porcelain", cwd=source):
        raise ValueError("current source checkout is dirty")

    binary = workspace / "quality-review"
    cases_path = workspace / "private" / "cases.json"
    plugin = workspace / "plugin" / "code-quality"
    if binary.is_symlink() or not binary.is_file():
        raise ValueError("frozen binary must be a regular non-symlink file")
    if cases_path.is_symlink() or not cases_path.is_file():
        raise ValueError("frozen cases must be a regular non-symlink file")
    if file_sha256(binary) != baseline.get("binary_sha256"):
        raise ValueError("binary digest does not match baseline")
    if file_sha256(cases_path) != baseline.get("cases_sha256"):
        raise ValueError("case digest does not match baseline")
    if tree_sha256(source / "pilot" / "fixtures") != baseline.get("fixtures_sha256"):
        raise ValueError("fixture digest does not match baseline")
    if tree_sha256(plugin) != baseline.get("plugin_sha256"):
        raise ValueError("plugin digest does not match baseline")
    version = run(str(binary), "version")
    if version != f"quality-review {baseline.get('rc_version')}":
        raise ValueError("binary version does not match baseline")

    cases = load_cases(cases_path)
    case_by_id = {str(case["id"]): case for case in cases}
    schedule = build_plan(cases, {})
    expected_slots = {
        (str(slot["case_id"]), str(slot["host"]), int(slot["run_number"])): slot
        for slot in schedule["slots"]
    }
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if operator.get("visibility") != "operator_and_human_reviewer_only" or not isinstance(runs, list) or len(runs) != 100:
        raise ValueError("operator manifest is invalid")

    seen_runs: set[str] = set()
    seen_slots: set[tuple[str, str, int]] = set()
    mapping_by_run: dict[str, dict[str, object]] = {}
    repositories: dict[str, tuple[pathlib.Path, str, str]] = {}
    host_totals = {"codex": 0}
    case_run_counts: dict[str, int] = {}
    for index, mapping in enumerate(runs):
        if not isinstance(mapping, dict):
            raise ValueError(f"operator runs[{index}] is invalid")
        run_id = mapping.get("run_id")
        case_id = mapping.get("case_id")
        host = mapping.get("host")
        run_number = mapping.get("run_number")
        if (
            not isinstance(run_id, str)
            or not re.fullmatch(r"run-[0-9a-f]{24}", run_id)
            or run_id in seen_runs
            or case_id not in case_by_id
            or host not in host_totals
            or not isinstance(run_number, int)
        ):
            raise ValueError(f"operator run identity is invalid at index {index}")
        slot_identity = (str(case_id), str(host), run_number)
        expected_slot = expected_slots.get(slot_identity)
        if expected_slot is None or slot_identity in seen_slots:
            raise ValueError(f"operator slot is invalid or duplicated at index {index}")
        if (
            mapping.get("rule_id") != expected_slot["rule_id"]
            or mapping.get("kind") != expected_slot["kind"]
            or mapping.get("expected") != case_by_id[str(case_id)].get("expected")
        ):
            raise ValueError(f"operator expected mapping is invalid for {run_id}")
        seen_runs.add(run_id)
        seen_slots.add(slot_identity)
        mapping_by_run[run_id] = mapping
        host_totals[str(host)] += 1
        case_run_counts[str(case_id)] = case_run_counts.get(str(case_id), 0) + 1

        task_relative = mapping.get("task")
        expected_task_relative = f"tasks/{host}/{run_id}.json"
        if task_relative != expected_task_relative:
            raise ValueError(f"operator task path is invalid for {run_id}")
        task_path = workspace / pathlib.PurePosixPath(task_relative)
        task = load_json(task_path)
        if set(task) != {"schema_version", "run_id", "host", "repository", "base", "target", "diff_reason", "output_root"}:
            raise ValueError(f"blind task fields are invalid for {run_id}")
        if task.get("run_id") != run_id or task.get("host") != host:
            raise ValueError(f"blind task identity does not match operator mapping for {run_id}")
        prompt_path = task_path.with_suffix(".md")
        if prompt_path.is_symlink() or not prompt_path.is_file():
            raise ValueError(f"blind task prompt is invalid for {run_id}")
        prompt = prompt_path.read_text(encoding="utf-8")
        if FORBIDDEN_TASK_PATTERN.search(prompt) or FORBIDDEN_TASK_PATTERN.search(task_path.read_text(encoding="utf-8")):
            raise ValueError(f"blind task leaks expected identity for {run_id}")

        repository = pathlib.Path(str(task["repository"])).resolve(strict=True)
        expected_root = (workspace / "repositories").resolve()
        repository_id = mapping.get("repository_id")
        if (
            not isinstance(repository_id, str)
            or not re.fullmatch(r"repo-[0-9a-f]{24}", repository_id)
            or repository.parent != expected_root
            or repository.name != repository_id
        ):
            raise ValueError(f"repository mapping is invalid for {run_id}")
        base = str(task["base"])
        target = str(task["target"])
        if (
            task.get("diff_reason") != f"blind qualification committed increment {run_id}"
            or task.get("output_root") != str(workspace / "sessions" / run_id)
        ):
            raise ValueError(f"blind task paths are invalid for {run_id}")
        expected_prompt = task_markdown(
            task,
            workspace / "plugin" / "code-quality" / "skills" / "code-quality" / "SKILL.md",
            binary,
        )
        if prompt != expected_prompt:
            raise ValueError(f"blind task prompt does not match the frozen template for {run_id}")
        previous = repositories.setdefault(str(case_id), (repository, base, target))
        if previous != (repository, base, target):
            raise ValueError(f"case maps to multiple repositories: {case_id}")

    if host_totals != {"codex": 100}:
        raise ValueError("host schedule is not Codex-only")
    if seen_slots != set(expected_slots):
        raise ValueError("operator schedule does not match the frozen matrix")
    for case_id, case in case_by_id.items():
        required = 3 if case.get("kind") == "positive" else 1
        if case_run_counts.get(case_id) != required:
            raise ValueError(f"run count is invalid for {case_id}")
        repository, base, target = repositories[case_id]
        fixture = source / "pilot" / "fixtures" / case_id.lower()
        if tracked_tree(repository, base) != source_tree(fixture / "base"):
            raise ValueError(f"base repository does not match fixture: {case_id}")
        if tracked_tree(repository, target) != source_tree(fixture / "target"):
            raise ValueError(f"target repository does not match fixture: {case_id}")

    eval_result = json.loads(run(str(binary), "eval", "--cases", str(cases_path)))
    if (
        eval_result.get("total_cases") != 60
        or eval_result.get("passed_cases") != 60
        or eval_result.get("failed_cases") != 0
        or eval_result.get("matrix_complete") is not True
    ):
        raise ValueError("frozen deterministic eval did not pass")

    replay_summary, evidence_summary = verify_replay_evidence(
        workspace,
        binary,
        cases_path,
        baseline,
        mapping_by_run,
    )
    if args.write_summary:
        write_json_atomically(workspace / "evidence-summary.json", evidence_summary)
    result = {
        "schema_version": 1,
        "source_commit": source_head,
        "tasks_verified": len(runs),
        "repositories_verified": len(repositories),
        "host_totals": host_totals,
        "deterministic_cases_passed": eval_result["passed_cases"],
        "valid_replay_records": replay_summary["valid_records"],
        "human_confirmed_runs": evidence_summary["human_statuses"]["confirmed"],
        "metrics_available": replay_summary["metrics_available"],
        "qualification_complete": replay_summary["qualification_complete"],
        "evidence_summary": str(workspace / "evidence-summary.json") if args.write_summary else None,
        "status": "valid",
    }
    json.dump(result, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
