#!/usr/bin/env python3
"""Verify the canonical pilot summary against strict CLI validation and rendering."""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys


def resolve_regular_file(root: pathlib.Path, value: object, field: str) -> pathlib.Path:
    if not isinstance(value, str) or not value or pathlib.PurePosixPath(value).is_absolute():
        raise ValueError(f"{field} must be a non-empty relative POSIX path")
    relative = pathlib.PurePosixPath(value)
    if ".." in relative.parts or "." in relative.parts:
        raise ValueError(f"{field} must be a clean relative path")
    current = root
    for part in relative.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError(f"{field} must not traverse a symlink: {value}")
    if not current.is_file():
        raise ValueError(f"{field} does not identify a regular file: {value}")
    return current


def run_cli(binary: pathlib.Path, *args: str) -> bytes:
    completed = subprocess.run(
        [str(binary), *args],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if completed.returncode != 0:
        detail = completed.stderr.decode(errors="replace").strip()
        raise ValueError(f"quality-review {' '.join(args[:1])} failed: {detail}")
    return completed.stdout


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--summary",
        type=pathlib.Path,
        default=pathlib.Path(".code-quality/pilot-run-v4/pilot-summary.json"),
    )
    parser.add_argument("--quality-review", required=True, type=pathlib.Path)
    args = parser.parse_args()

    summary_path = args.summary.resolve(strict=True)
    binary = args.quality_review.resolve(strict=True)
    if not binary.is_file():
        raise ValueError("--quality-review must identify a regular file")
    summary = json.loads(summary_path.read_text(encoding="utf-8"))
    if summary.get("schema_version") != 1 or summary.get("pilot") != "representative_host_session":
        raise ValueError("pilot summary identity is invalid")
    cases = summary.get("cases")
    expected_names = {
        "des-003-positive",
        "des-003-counterexample",
        "des-003-insufficient",
        "harness-evidence",
    }
    if not isinstance(cases, list) or len(cases) != len(expected_names):
        raise ValueError("pilot summary must contain the four canonical cases")
    if summary.get("human_review_status") != "pending":
        raise ValueError("canonical pilot human_review_status must remain pending")

    root = summary_path.parent
    names: set[str] = set()
    for index, case in enumerate(cases):
        if not isinstance(case, dict):
            raise ValueError(f"cases[{index}] must be an object")
        name = case.get("name")
        if not isinstance(name, str) or not name or name in names:
            raise ValueError(f"cases[{index}].name is missing or duplicated")
        names.add(name)
        result_path = resolve_regular_file(root, case.get("result_path"), f"cases[{index}].result_path")
        markdown_path = resolve_regular_file(root, case.get("markdown_path"), f"cases[{index}].markdown_path")
        run_cli(binary, "validate", str(result_path))
        rendered = run_cli(binary, "render", str(result_path))
        if rendered != markdown_path.read_bytes():
            raise ValueError(f"saved Markdown does not match deterministic rendering: {name}")
        result = json.loads(result_path.read_text(encoding="utf-8"))
        execution = result.get("execution", {})
        expected = {
            "semantic_result": result.get("adjudication", {}).get("semantic_result"),
            "agent_count": execution.get("agent_count"),
            "verifier_count": execution.get("verifier_count"),
            "inspected_context_count": len(result.get("inspected_context", [])),
        }
        for field, actual in expected.items():
            if case.get(field) != actual:
                raise ValueError(f"summary mismatch for {name}.{field}")
    if names != expected_names:
        raise ValueError("pilot summary case names do not match the canonical set")

    json.dump(
        {"summary": str(summary_path), "cases_verified": len(cases), "status": "valid"},
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
