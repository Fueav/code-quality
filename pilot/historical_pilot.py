#!/usr/bin/env python3
"""Validate and summarize the 3-project, 30-change report-only pilot."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import statistics
import sys


VALID_RESULTS = {"PASS", "MANUAL_REVIEW", "INCOMPLETE"}
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
    if not isinstance(projects, list) or len(projects) < 1:
        raise ValueError("historical pilot requires at least one project")
    if not isinstance(changes, list) or len(changes) < 8:
        raise ValueError("historical pilot requires at least 8 changes")

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

    if label_counts["severe"] < 4 or label_counts["normal"] < 4:
        raise ValueError("historical pilot requires at least 4 severe and 4 normal changes")
    if represented_projects != project_ids:
        raise ValueError("every project must contribute at least one historical change")
    return result


def validate_record(record: dict[str, object], change: dict[str, object]) -> list[str]:
    errors: list[str] = []
    if record.get("schema_version") != 1 or record.get("change_id") != change.get("id"):
        errors.append("record identity is invalid")
    if not non_empty(record.get("reviewer")) or not non_empty(record.get("review_note")):
        errors.append("maintainer review is missing")
    for lane in ("skill", "builtin"):
        result = record.get(lane)
        if not isinstance(result, dict):
            errors.append(f"{lane} result is missing")
            continue
        semantic_result = result.get("semantic_result")
        if semantic_result not in VALID_RESULTS or (lane == "builtin" and semantic_result == "INCOMPLETE"):
            errors.append(f"{lane}.semantic_result is invalid")
        for field in ("input_tokens", "output_tokens", "duration_ms"):
            value = result.get(field)
            if not isinstance(value, int) or isinstance(value, bool) or value < 0:
                errors.append(f"{lane}.{field} is invalid")
        cost = result.get("estimated_cost_usd")
        if cost is not None and (not isinstance(cost, (int, float)) or isinstance(cost, bool) or cost < 0):
            errors.append(f"{lane}.estimated_cost_usd is invalid")
        core_issue_found = result.get("core_issue_found")
        if change.get("ground_truth") == "severe":
            if not isinstance(core_issue_found, bool):
                errors.append(f"severe change requires {lane}.core_issue_found")
        elif core_issue_found is not None:
            errors.append(f"normal change must use null {lane}.core_issue_found")
        actionable = result.get("report_actionable")
        if semantic_result == "MANUAL_REVIEW":
            if not isinstance(actionable, bool):
                errors.append(f"{lane} MANUAL_REVIEW requires report_actionable")
        elif actionable is not None:
            errors.append(f"{lane} PASS or INCOMPLETE must use null report_actionable")
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
    def lane_summary(lane: str) -> dict[str, object]:
        lane_records = {change_id: record[lane] for change_id, record in records.items()}
        manual_ids = [change_id for change_id, item in lane_records.items() if item["semantic_result"] == "MANUAL_REVIEW"]
        manual_severe = sum(1 for change_id in severe_ids if change_id in manual_ids)
        manual_normal = sum(1 for change_id in normal_ids if change_id in manual_ids)
        found = sum(1 for change_id in severe_ids if lane_records.get(change_id, {}).get("core_issue_found") is True)
        actionable = sum(1 for change_id in manual_ids if lane_records[change_id]["report_actionable"] is True)
        completed = sum(1 for item in lane_records.values() if item["semantic_result"] != "INCOMPLETE")
        durations = [int(item["duration_ms"]) for item in lane_records.values()]
        costs = [float(item["estimated_cost_usd"]) for item in lane_records.values() if item.get("estimated_cost_usd") is not None]
        return {
            "manual_review_findings": {
                "total": len(manual_ids),
                "rate": ratio(len(manual_ids), len(changes)),
                "severe": manual_severe,
                "severe_rate": ratio(manual_severe, len(severe_ids)),
                "normal": manual_normal,
                "normal_rate": ratio(manual_normal, len(normal_ids)),
            },
            "severe_issue_discovery": {"found": found, "total": len(severe_ids), "rate": ratio(found, len(severe_ids))},
            "report_actionability": {"actionable": actionable, "finding_reports": len(manual_ids), "rate": ratio(actionable, len(manual_ids))},
            "execution": {
                "completed": completed,
                "completion_rate": ratio(completed, len(changes)),
                "input_tokens": sum(int(item["input_tokens"]) for item in lane_records.values()),
                "output_tokens": sum(int(item["output_tokens"]) for item in lane_records.values()),
                "duration_ms_p50": int(statistics.median(durations)) if durations else None,
                "duration_ms_p95": percentile(durations, 0.95),
                "duration_ms_max": max(durations) if durations else None,
                "estimated_cost_usd_available": len(costs),
                "estimated_cost_usd_total": sum(costs) if costs else None,
            },
        }

    skill = lane_summary("skill")
    builtin = lane_summary("builtin")
    both = skill_only = builtin_only = neither = 0
    builtin_misses_by_skill: list[str] = []
    for change_id in severe_ids:
        skill_found = records.get(change_id, {}).get("skill", {}).get("core_issue_found") is True
        builtin_found = records.get(change_id, {}).get("builtin", {}).get("core_issue_found") is True
        if skill_found and builtin_found:
            both += 1
        elif skill_found:
            skill_only += 1
        elif builtin_found:
            builtin_only += 1
            builtin_misses_by_skill.append(change_id)
        else:
            neither += 1
    complete = len(records) == len(changes) and not missing and not errors
    caught_up = None
    if complete:
        caught_up = (
            builtin_only == 0
            and skill["severe_issue_discovery"]["found"] >= builtin["severe_issue_discovery"]["found"]
            and skill["manual_review_findings"]["normal_rate"] <= builtin["manual_review_findings"]["normal_rate"]
        )

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
        "historical_evidence_complete": complete,
        "skill": skill,
        "builtin": builtin,
        "comparison": {
            "severe_core_issue_pairs": {
                "both": both,
                "skill_only": skill_only,
                "builtin_only": builtin_only,
                "neither": neither,
                "builtin_misses_by_skill": builtin_misses_by_skill,
            },
            "caught_up_to_builtin_review": caught_up,
            "criterion": "complete evidence; no severe issue found only by builtin; skill severe discovery is not lower; skill normal MANUAL_REVIEW rate is not higher",
        },
        "decision": "project maintainers decide live report-only entry; this comparison never enables blocking",
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
