#!/usr/bin/env python3
"""Summarize human annotations from a live report-only pilot."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import sys
import tempfile


JUDGMENTS = ("adopted", "noise", "confirmed_unseen")
DIMENSIONS = ("D1", "D2", "D3", "D4")


def non_empty(value: object) -> bool:
    return isinstance(value, str) and bool(value.strip())


def ratio(numerator: int, denominator: int) -> float | None:
    return numerator / denominator if denominator else None


def load_annotations(directory: pathlib.Path) -> list[dict[str, object]]:
    if directory.is_symlink():
        raise ValueError("annotations must be a non-symlink directory")
    root = directory.resolve(strict=True)
    if not root.is_dir():
        raise ValueError("annotations must be a non-symlink directory")
    records: list[dict[str, object]] = []
    seen_reports: set[str] = set()
    for path in sorted(root.iterdir()):
        if path.suffix != ".json":
            continue
        if path.is_symlink() or not path.is_file():
            raise ValueError(f"annotation must be a regular file: {path.name}")
        value = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(value, dict):
            raise ValueError(f"annotation must be an object: {path.name}")
        validate_annotation(value, path.name)
        report_id = str(value["report_id"])
        if report_id in seen_reports:
            raise ValueError(f"report_id is duplicated: {report_id}")
        seen_reports.add(report_id)
        records.append(value)
    return records


def validate_annotation(record: dict[str, object], source: str = "annotation") -> None:
    if (
        record.get("schema_version") != 1
        or not non_empty(record.get("report_id"))
        or not non_empty(record.get("project"))
        or record.get("review_rounds") not in (1, 2)
        or not isinstance(record.get("findings"), list)
    ):
        raise ValueError(f"{source}: report identity is invalid")
    seen_findings: set[str] = set()
    for index, finding in enumerate(record["findings"]):
        if not isinstance(finding, dict):
            raise ValueError(f"{source}: findings[{index}] must be an object")
        finding_id = finding.get("finding_id")
        locations = finding.get("code_locations")
        if (
            not non_empty(finding_id)
            or finding_id in seen_findings
            or finding.get("dimension") not in DIMENSIONS
            or not isinstance(locations, list)
            or len(locations) == 0
            or not non_empty(finding.get("description"))
            or finding.get("judgment") not in JUDGMENTS
            or not non_empty(finding.get("note"))
        ):
            raise ValueError(f"{source}: findings[{index}] is invalid")
        seen_findings.add(str(finding_id))
        for location_index, location in enumerate(locations):
            if not isinstance(location, dict):
                raise ValueError(f"{source}: findings[{index}].code_locations[{location_index}] is invalid")
            path = location.get("path")
            line = location.get("line")
            if (
                not non_empty(path)
                or pathlib.PurePath(str(path)).is_absolute()
                or ".." in pathlib.PurePath(str(path)).parts
                or not isinstance(line, int)
                or isinstance(line, bool)
                or line < 1
            ):
                raise ValueError(f"{source}: findings[{index}].code_locations[{location_index}] is invalid")


def summarize(records: list[dict[str, object]]) -> dict[str, object]:
    for index, record in enumerate(records):
        validate_annotation(record, f"records[{index}]")
    findings = [finding for record in records for finding in record["findings"]]
    judgment_counts = {judgment: 0 for judgment in JUDGMENTS}
    dimension_counts = {dimension: 0 for dimension in DIMENSIONS}
    for finding in findings:
        judgment_counts[str(finding["judgment"])] += 1
        dimension_counts[str(finding["dimension"])] += 1
    zero_reports = [record for record in records if not record["findings"]]
    single_round_zero = sum(1 for record in zero_reports if record["review_rounds"] == 1)
    rereviewed_zero = sum(1 for record in zero_reports if record["review_rounds"] == 2)
    return {
        "schema_version": 1,
        "profile": "live_report_only_annotations",
        "reports": {
            "total": len(records),
            "rereviewed": sum(1 for record in records if record["review_rounds"] == 2),
            "zero_findings": {
                "total": len(zero_reports),
                "ratio": ratio(len(zero_reports), len(records)),
                "single_round": single_round_zero,
                "rereviewed": rereviewed_zero,
            },
        },
        "findings": {
            "total": len(findings),
            "judgments": {
                judgment: {"count": count, "ratio": ratio(count, len(findings))}
                for judgment, count in judgment_counts.items()
            },
            "dimensions": dimension_counts,
        },
    }


def write_atomically(path: pathlib.Path, contents: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as target:
            target.write(contents)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--annotations", required=True, type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    summary = summarize(load_annotations(args.annotations))
    raw = json.dumps(summary, indent=2, sort_keys=True) + "\n"
    if args.output:
        write_atomically(args.output.resolve(), raw)
    else:
        sys.stdout.write(raw)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
