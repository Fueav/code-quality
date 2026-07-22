#!/usr/bin/env python3
"""Verify a frozen blind qualification workspace and its task mapping."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import subprocess
import sys

from qualification_initialize import file_sha256, task_markdown, tree_sha256
from qualification_matrix import build_plan, load_cases


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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    source = args.source.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot qualify")
    if baseline.get("schema_version") != 1 or baseline.get("planned_runs") != 100:
        raise ValueError("baseline identity is invalid")
    if baseline.get("host_totals") != {"claude-code": 50, "codex": 50}:
        raise ValueError("baseline host schedule is invalid")
    if not all(isinstance(baseline.get(field), str) and baseline[field] for field in ("claude_code_version", "codex_version")):
        raise ValueError("baseline host versions are missing")
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
    repositories: dict[str, tuple[pathlib.Path, str, str]] = {}
    host_totals = {"claude-code": 0, "codex": 0}
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

    if host_totals != {"claude-code": 50, "codex": 50}:
        raise ValueError("host schedule is not balanced")
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

    result = {
        "schema_version": 1,
        "source_commit": source_head,
        "tasks_verified": len(runs),
        "repositories_verified": len(repositories),
        "host_totals": host_totals,
        "deterministic_cases_passed": eval_result["passed_cases"],
        "status": "valid",
    }
    json.dump(result, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
