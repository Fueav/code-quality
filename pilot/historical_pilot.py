#!/usr/bin/env python3
"""Validate and summarize the 3-project, 30-change report-only pilot."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import statistics
import sys


VALID_RESULTS = {"PASS", "MANUAL_REVIEW", "BLOCK", "INCOMPLETE"}
VALID_LABELS = {"severe", "normal"}


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def non_empty(value: object) -> bool:
    return isinstance(value, str) and bool(value.strip())


def valid_commit(value: object) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{40}", value) is not None


def validate_manifest(manifest: dict[str, object]) -> dict[str, dict[str, object]]:
    if manifest.get("schema_version") != 1 or manifest.get("profile") != "report_only_historical_pilot":
        raise ValueError("historical pilot manifest identity is invalid")
    projects = manifest.get("projects")
    changes = manifest.get("changes")
    if not isinstance(projects, list) or len(projects) != 3:
        raise ValueError("historical pilot requires exactly three projects")
    if not isinstance(changes, list) or len(changes) < 30:
        raise ValueError("historical pilot requires at least 30 changes")

    project_ids: set[str] = set()
    for index, project in enumerate(projects):
        if (
            not isinstance(project, dict)
            or not non_empty(project.get("id"))
            or not non_empty(project.get("repository"))
            or not non_empty(project.get("maintainer"))
            or project["id"] in project_ids
        ):
            raise ValueError(f"projects[{index}] is invalid")
        project_ids.add(str(project["id"]))

    result: dict[str, dict[str, object]] = {}
    label_counts = {"severe": 0, "normal": 0}
    represented_projects: set[str] = set()
    for index, change in enumerate(changes):
        if not isinstance(change, dict):
            raise ValueError(f"changes[{index}] is invalid")
        change_id = change.get("id")
        project_id = change.get("project_id")
        label = change.get("ground_truth")
        if (
            not non_empty(change_id)
            or change_id in result
            or project_id not in project_ids
            or label not in VALID_LABELS
            or not valid_commit(change.get("base_commit"))
            or not valid_commit(change.get("target_commit"))
            or change.get("base_commit") == change.get("target_commit")
            or not non_empty(change.get("labeler"))
            or not non_empty(change.get("label_note"))
        ):
            raise ValueError(f"changes[{index}] is invalid")
        result[str(change_id)] = change
        label_counts[str(label)] += 1
        represented_projects.add(str(project_id))

    if label_counts["severe"] < 15 or label_counts["normal"] < 15:
        raise ValueError("historical pilot requires at least 15 severe and 15 normal changes")
    if represented_projects != project_ids:
        raise ValueError("every project must contribute at least one historical change")
    return result


def validate_record(record: dict[str, object], change: dict[str, object]) -> list[str]:
    errors: list[str] = []
    result = record.get("semantic_result")
    if record.get("schema_version") != 1 or record.get("change_id") != change.get("id"):
        errors.append("record identity is invalid")
    if result not in VALID_RESULTS:
        errors.append("semantic_result is invalid")
    if not non_empty(record.get("reviewer")) or not non_empty(record.get("review_note")):
        errors.append("maintainer review is missing")
    for field in ("input_tokens", "output_tokens", "duration_ms"):
        value = record.get(field)
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            errors.append(f"{field} is invalid")
    cost = record.get("estimated_cost_usd")
    if cost is not None and (not isinstance(cost, (int, float)) or isinstance(cost, bool) or cost < 0):
        errors.append("estimated_cost_usd is invalid")

    core_issue_found = record.get("core_issue_found")
    if change.get("ground_truth") == "severe":
        if not isinstance(core_issue_found, bool):
            errors.append("severe change requires core_issue_found")
    elif core_issue_found is not None:
        errors.append("normal change must use null core_issue_found")

    high_risk_confirmed = record.get("high_risk_confirmed")
    if result == "BLOCK":
        if not isinstance(high_risk_confirmed, bool):
            errors.append("BLOCK requires high_risk_confirmed")
    elif high_risk_confirmed is not None:
        errors.append("non-BLOCK result must use null high_risk_confirmed")

    actionable = record.get("report_actionable")
    if result in {"BLOCK", "MANUAL_REVIEW"}:
        if not isinstance(actionable, bool):
            errors.append("finding-bearing report requires report_actionable")
    elif actionable is not None:
        errors.append("PASS or INCOMPLETE must use null report_actionable")
    return errors


def percentile(values: list[int], percentage: float) -> int | None:
    if not values:
        return None
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, int((len(ordered) - 1) * percentage + 0.999999)))
    return ordered[index]


def ratio(numerator: int, denominator: int) -> float | None:
    return numerator / denominator if denominator else None


def summarize(manifest: dict[str, object], records_directory: pathlib.Path) -> dict[str, object]:
    changes = validate_manifest(manifest)
    if records_directory.is_symlink() or not records_directory.is_dir():
        raise ValueError("records must be a non-symlink directory")

    records: dict[str, dict[str, object]] = {}
    errors: list[str] = []
    for path in sorted(records_directory.iterdir()):
        if path.suffix != ".json":
            continue
        try:
            record = load_json(path)
        except (ValueError, json.JSONDecodeError) as error:
            errors.append(f"{path.name}: {error}")
            continue
        change_id = record.get("change_id")
        if not isinstance(change_id, str) or change_id not in changes or change_id in records:
            errors.append(f"{path.name}: unknown or duplicate change_id")
            continue
        record_errors = validate_record(record, changes[change_id])
        if record_errors:
            errors.extend(f"{path.name}: {message}" for message in record_errors)
            continue
        records[change_id] = record

    missing = sorted(set(changes) - set(records))
    severe_ids = [change_id for change_id, change in changes.items() if change["ground_truth"] == "severe"]
    normal_ids = [change_id for change_id, change in changes.items() if change["ground_truth"] == "normal"]
    severe_found = sum(1 for change_id in severe_ids if records.get(change_id, {}).get("core_issue_found") is True)
    block_records = [record for record in records.values() if record["semantic_result"] == "BLOCK"]
    confirmed_blocks = sum(1 for record in block_records if record["high_risk_confirmed"] is True)
    false_blocks = sum(1 for record in block_records if record["high_risk_confirmed"] is False)
    false_blocks_on_normal = sum(
        1
        for change_id in normal_ids
        if records.get(change_id, {}).get("semantic_result") == "BLOCK"
        and records[change_id].get("high_risk_confirmed") is False
    )
    finding_records = [
        record for record in records.values() if record["semantic_result"] in {"BLOCK", "MANUAL_REVIEW"}
    ]
    actionable = sum(1 for record in finding_records if record["report_actionable"] is True)
    completed = sum(1 for record in records.values() if record["semantic_result"] != "INCOMPLETE")
    durations = [int(record["duration_ms"]) for record in records.values()]
    costs = [float(record["estimated_cost_usd"]) for record in records.values() if record.get("estimated_cost_usd") is not None]

    return {
        "schema_version": 1,
        "profile": "report_only_historical_pilot",
        "projects": len(manifest["projects"]),
        "planned_changes": len(changes),
        "severe_changes": len(severe_ids),
        "normal_changes": len(normal_ids),
        "valid_records": len(records),
        "missing_records": missing,
        "record_errors": errors,
        "historical_evidence_complete": len(records) == len(changes) and not missing and not errors,
        "severe_issue_discovery": {
            "found": severe_found,
            "total": len(severe_ids),
            "rate": ratio(severe_found, len(severe_ids)),
        },
        "high_risk_results": {
            "total": len(block_records),
            "confirmed": confirmed_blocks,
            "false": false_blocks,
            "precision": ratio(confirmed_blocks, len(block_records)),
            "false_rate_on_normal_changes": ratio(false_blocks_on_normal, len(normal_ids)),
        },
        "report_actionability": {
            "actionable": actionable,
            "finding_reports": len(finding_records),
            "rate": ratio(actionable, len(finding_records)),
        },
        "execution": {
            "completed": completed,
            "completion_rate": ratio(completed, len(changes)),
            "input_tokens": sum(int(record["input_tokens"]) for record in records.values()),
            "output_tokens": sum(int(record["output_tokens"]) for record in records.values()),
            "duration_ms_p50": int(statistics.median(durations)) if durations else None,
            "duration_ms_p95": percentile(durations, 0.95),
            "duration_ms_max": max(durations) if durations else None,
            "estimated_cost_usd_available": len(costs),
            "estimated_cost_usd_total": sum(costs) if costs else None,
        },
        "decision": "project maintainers decide live report-only entry; this summary never enables blocking",
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--records", required=True, type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()

    summary = summarize(load_json(args.manifest.resolve(strict=True)), args.records.resolve(strict=True))
    raw = json.dumps(summary, indent=2, sort_keys=True) + "\n"
    if args.output:
        output = args.output.resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(raw, encoding="utf-8")
    else:
        sys.stdout.write(raw)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
