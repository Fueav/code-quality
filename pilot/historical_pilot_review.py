#!/usr/bin/env python3
"""Attach one maintainer judgment to an immutable historical observation."""

from __future__ import annotations

import argparse
import datetime
import json
import pathlib
import sys

from historical_pilot import load_json, summarize, validate_manifest, validate_record
from qualification_collect import write_atomically


def boolean(value: str) -> bool:
    return value == "yes"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--reviewer", required=True)
    parser.add_argument("--note", required=True)
    parser.add_argument("--skill-core-issue-found", choices=("yes", "no"))
    parser.add_argument("--builtin-core-issue-found", choices=("yes", "no"))
    parser.add_argument("--skill-report-actionable", choices=("yes", "no"))
    parser.add_argument("--builtin-report-actionable", choices=("yes", "no"))
    parser.add_argument("--skill-estimated-cost-usd", type=float)
    parser.add_argument("--builtin-estimated-cost-usd", type=float)
    args = parser.parse_args()

    if not args.reviewer.strip() or not args.note.strip():
        raise ValueError("reviewer and note must be non-empty")
    if any(cost is not None and cost < 0 for cost in (args.skill_estimated_cost_usd, args.builtin_estimated_cost_usd)):
        raise ValueError("estimated costs must be non-negative")
    workspace = args.workspace.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("profile") != "report_only_historical_pilot":
        raise ValueError("workspace is not a historical pilot")
    manifest = load_json(workspace / "private" / "manifest.json")
    changes = validate_manifest(manifest)
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list):
        raise ValueError("operator manifest is invalid")
    matches = [item for item in runs if isinstance(item, dict) and item.get("run_id") == args.run_id]
    if len(matches) != 1:
        raise ValueError("run ID is unknown or duplicated")
    mapping = matches[0]
    change = changes[str(mapping["change_id"])]
    observation = load_json(workspace / "observations" / f"{args.run_id}.json")
    evidence = load_json(workspace / "run-evidence" / f"{args.run_id}.json")
    if (
        observation.get("run_id") != args.run_id
        or observation.get("change_id") != change.get("id")
        or evidence.get("run_id") != args.run_id
        or not isinstance(observation.get("skill"), dict)
        or not isinstance(observation.get("builtin"), dict)
        or not isinstance(evidence.get("skill"), dict)
        or not isinstance(evidence.get("builtin"), dict)
        or evidence["skill"].get("result_sha256") != observation["skill"].get("result_sha256")
        or evidence["builtin"].get("result_sha256") != observation["builtin"].get("result_sha256")
    ):
        raise ValueError("observation identity or evidence binding is invalid")

    severe = change["ground_truth"] == "severe"
    core_judgments = (args.skill_core_issue_found, args.builtin_core_issue_found)
    if severe and not all(value is not None for value in core_judgments):
        raise ValueError("both core-issue judgments are required only for severe changes")
    if not severe and any(value is not None for value in core_judgments):
        raise ValueError("normal changes must not receive core-issue judgments")
    for lane in ("skill", "builtin"):
        result = observation[lane]["semantic_result"]
        actionable = getattr(args, f"{lane}_report_actionable")
        if (result == "MANUAL_REVIEW") != (actionable is not None):
            raise ValueError(f"{lane} report-actionable is required only for MANUAL_REVIEW")

    record = {
        "schema_version": 1,
        "change_id": change["id"],
        "reviewer": args.reviewer,
        "review_note": args.note,
        "skill": {
            "semantic_result": observation["skill"]["semantic_result"],
            "core_issue_found": boolean(args.skill_core_issue_found) if args.skill_core_issue_found is not None else None,
            "report_actionable": boolean(args.skill_report_actionable) if args.skill_report_actionable is not None else None,
            "input_tokens": observation["skill"]["input_tokens"],
            "output_tokens": observation["skill"]["output_tokens"],
            "duration_ms": observation["skill"]["duration_ms"],
            "estimated_cost_usd": args.skill_estimated_cost_usd,
        },
        "builtin": {
            "semantic_result": observation["builtin"]["semantic_result"],
            "core_issue_found": boolean(args.builtin_core_issue_found) if args.builtin_core_issue_found is not None else None,
            "report_actionable": boolean(args.builtin_report_actionable) if args.builtin_report_actionable is not None else None,
            "input_tokens": observation["builtin"]["input_tokens"],
            "output_tokens": observation["builtin"]["output_tokens"],
            "duration_ms": observation["builtin"]["duration_ms"],
            "estimated_cost_usd": args.builtin_estimated_cost_usd,
        },
    }
    errors = validate_record(record, change)
    if errors:
        raise ValueError("human record is invalid: " + "; ".join(errors))
    record_path = workspace / "records" / f"{change['id']}.json"
    review_path = workspace / "human-reviews" / f"{args.run_id}.json"
    if record_path.exists() or review_path.exists():
        raise ValueError("maintainer judgment is immutable and already exists")
    reviewed_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    write_atomically(record_path, json.dumps(record, indent=2, sort_keys=True) + "\n")
    write_atomically(
        review_path,
        json.dumps(
            {
                "schema_version": 1,
                "run_id": args.run_id,
                "change_id": change["id"],
                "reviewer": args.reviewer,
                "reviewed_at": reviewed_at,
                "note": args.note,
                "skill": {
                    "core_issue_found": record["skill"]["core_issue_found"],
                    "report_actionable": record["skill"]["report_actionable"],
                    "result_sha256": observation["skill"]["result_sha256"],
                },
                "builtin": {
                    "core_issue_found": record["builtin"]["core_issue_found"],
                    "report_actionable": record["builtin"]["report_actionable"],
                    "result_sha256": observation["builtin"]["result_sha256"],
                },
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
    )
    summary = summarize(manifest, workspace / "records")
    write_atomically(workspace / "summary.json", json.dumps(summary, indent=2, sort_keys=True) + "\n")
    write_atomically(
        workspace / "progress.json",
        json.dumps(
            {
                "schema_version": 1,
                "planned_runs": baseline["planned_runs"],
                "executed_runs": len(list((workspace / "observations").glob("*.json"))),
                "reviewed_runs": summary["valid_records"],
                "historical_evidence_complete": summary["historical_evidence_complete"],
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
    )
    json.dump(
        {
            "run_id": args.run_id,
            "change_id": change["id"],
            "record": str(record_path),
            "historical_evidence_complete": summary["historical_evidence_complete"],
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
