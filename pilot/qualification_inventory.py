#!/usr/bin/env python3
"""Report whether every eval case has a reviewable base/target fixture."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys


def regular_files(root: pathlib.Path) -> dict[str, bytes]:
    files: dict[str, bytes] = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise ValueError(f"fixture contains symlink: {relative}")
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError(f"fixture contains unsupported path: {relative}")
        files[relative] = path.read_bytes()
    return files


def validate_fixture(root: pathlib.Path, language: object) -> list[str]:
    errors: list[str] = []
    base = root / "base"
    target = root / "target"
    if not base.is_dir() or not target.is_dir():
        return ["requires base/ and target/ directories"]
    try:
        base_files = regular_files(base)
        target_files = regular_files(target)
    except ValueError as error:
        return [str(error)]
    if not base_files or not target_files:
        errors.append("base and target must each contain a regular file")
    if base_files == target_files:
        errors.append("base and target must differ")
    required_suffix = {"go": ".go", "json": ".json", "sql": ".sql"}.get(language)
    if required_suffix and not any(path.endswith(required_suffix) for path in set(base_files) | set(target_files)):
        errors.append(f"requires at least one {required_suffix} file")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cases", type=pathlib.Path, default=pathlib.Path("evals/cases.json"))
    parser.add_argument("--fixtures", type=pathlib.Path, default=pathlib.Path("pilot/fixtures"))
    args = parser.parse_args()

    cases_path = args.cases.resolve(strict=True)
    fixtures_root = args.fixtures.resolve(strict=True)
    payload = json.loads(cases_path.read_text(encoding="utf-8"))
    cases = payload.get("cases")
    if not isinstance(cases, list):
        raise ValueError("cases.json must contain a cases array")

    seen: set[str] = set()
    ready: list[str] = []
    missing: list[str] = []
    invalid: dict[str, list[str]] = {}
    for index, case in enumerate(cases):
        if not isinstance(case, dict) or not isinstance(case.get("id"), str) or not case["id"]:
            raise ValueError(f"cases[{index}].id is invalid")
        case_id = case["id"]
        if case_id in seen:
            raise ValueError(f"duplicate case id: {case_id}")
        seen.add(case_id)
        fixture = case.get("fixture")
        if not isinstance(fixture, dict):
            raise ValueError(f"{case_id}.fixture is invalid")
        root = fixtures_root / case_id.lower()
        if not root.is_dir():
            missing.append(case_id)
            continue
        errors = validate_fixture(root, fixture.get("language"))
        if errors:
            invalid[case_id] = errors
        else:
            ready.append(case_id)

    report = {
        "schema_version": 1,
        "total_cases": len(cases),
        "ready_fixtures": len(ready),
        "missing_fixtures": missing,
        "invalid_fixtures": invalid,
        "matrix_ready": len(ready) == len(cases) and not missing and not invalid,
    }
    json.dump(report, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0 if report["matrix_ready"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
