#!/usr/bin/env python3
"""Filter trace results and emit deterministic, deduplicated dataset targets."""

from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path
import subprocess
from typing import Any


def git(repo: Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        text=True,
        capture_output=True,
    )
    return completed.stdout.strip()


def resolve_commit(repo: Path, value: object) -> str | None:
    if not isinstance(value, str) or not value:
        return None
    try:
        return git(repo, "rev-parse", "--verify", f"{value}^{{commit}}")
    except subprocess.CalledProcessError:
        return None


def changed_lines(repo: Path, commit: str) -> int:
    output = git(repo, "show", "--format=", "--numstat", "--no-renames", commit)
    total = 0
    for line in output.splitlines():
        fields = line.split("\t", 2)
        if len(fields) < 2:
            continue
        for field in fields[:2]:
            if field.isdigit():
                total += int(field)
    return total


def reject_reason(repo: Path, row: dict[str, Any]) -> tuple[str | None, str | None, int | None]:
    if row.get("scope") != "IN_SCOPE":
        return "out_of_scope", None, None
    if row.get("material") is not True:
        return "material_false", None, None
    if row.get("static_detectable") is not True:
        return "static_detectable_false", None, None
    introducing = resolve_commit(repo, row.get("introducing_commit"))
    if introducing is None:
        return "introducing_commit_unlocatable", None, None
    size = changed_lines(repo, introducing)
    if size > 3000:
        return "introducing_commit_too_large", introducing, size
    return None, introducing, size


def defect(row: dict[str, Any]) -> dict[str, Any]:
    return {
        "fix_commit": row["fix_commit"],
        "rule_id": row["rule_id"],
        "difficulty": row["difficulty"],
        "defect_class": row["defect_class"],
        "defect": row["defect"],
        "defect_class_basis": row["defect_class_basis"],
        "evidence_chain": row["evidence_chain"],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--repo-name")
    parser.add_argument("--results-dir", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--stats-output", required=True, type=Path)
    args = parser.parse_args()

    repo = args.repo.resolve()
    repo_name = args.repo_name or repo.name
    result_files = sorted(args.results_dir.glob("*.json"))
    rejections: Counter[str] = Counter()
    accepted: list[tuple[dict[str, Any], str, int]] = []

    for path in result_files:
        try:
            row = json.loads(path.read_text())
            if not isinstance(row, dict):
                raise ValueError("result root must be an object")
        except (json.JSONDecodeError, OSError, ValueError):
            rejections["invalid_result"] += 1
            continue
        reason, introducing, size = reject_reason(repo, row)
        if reason:
            rejections[reason] += 1
            continue
        assert introducing is not None and size is not None
        accepted.append((row, introducing, size))

    groups: dict[str, list[tuple[dict[str, Any], int]]] = defaultdict(list)
    for row, introducing, size in accepted:
        groups[introducing].append((row, size))

    targets: list[dict[str, Any]] = []
    deduplicated = 0
    for introducing in sorted(groups):
        rows = sorted(
            groups[introducing],
            key=lambda item: (-int(item[0]["difficulty"]), str(item[0]["fix_commit"])),
        )
        primary, size = rows[0]
        deduplicated += len(rows) - 1
        targets.append(
            {
                "repo": primary.get("repo") or repo_name,
                "introducing_commit": introducing,
                "introducing_commit_changed_lines": size,
                "defect_class": primary["defect_class"],
                "defect_class_basis": primary["defect_class_basis"],
                "defects": [defect(row) for row, _ in rows],
                "max_difficulty": max(int(row["difficulty"]) for row, _ in rows),
            }
        )

    stats = {
        "repo": repo_name,
        "result_files": len(result_files),
        "accepted_results": len(accepted),
        "deduplicated_results": deduplicated,
        "output_targets": len(targets),
        "rejections": dict(sorted(rejections.items())),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.stats_output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(targets, ensure_ascii=False, indent=2) + "\n")
    args.stats_output.write_text(json.dumps(stats, ensure_ascii=False, indent=2) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
