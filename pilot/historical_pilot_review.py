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
    parser.add_argument("--core-issue-found", choices=("yes", "no"))
    parser.add_argument("--high-risk-confirmed", choices=("yes", "no"))
    parser.add_argument("--report-actionable", choices=("yes", "no"))
    parser.add_argument("--estimated-cost-usd", type=float)
    args = parser.parse_args()

    if not args.reviewer.strip() or not args.note.strip():
        raise ValueError("reviewer and note must be non-empty")
    if args.estimated_cost_usd is not None and args.estimated_cost_usd < 0:
        raise ValueError("estimated cost must be non-negative")
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
        or evidence.get("result_sha256") != observation.get("result_sha256")
    ):
        raise ValueError("observation identity or evidence binding is invalid")

    severe = change["ground_truth"] == "severe"
    result = observation.get("semantic_result")
    finding_report = result in {"BLOCK", "MANUAL_REVIEW"}
    if severe != (args.core_issue_found is not None):
        raise ValueError("core-issue-found is required only for severe changes")
    if (result == "BLOCK") != (args.high_risk_confirmed is not None):
        raise ValueError("high-risk-confirmed is required only for BLOCK")
    if finding_report != (args.report_actionable is not None):
        raise ValueError("report-actionable is required only for finding-bearing reports")

    record = {
        "schema_version": 1,
        "change_id": change["id"],
        "semantic_result": result,
        "core_issue_found": boolean(args.core_issue_found) if args.core_issue_found is not None else None,
        "high_risk_confirmed": boolean(args.high_risk_confirmed) if args.high_risk_confirmed is not None else None,
        "report_actionable": boolean(args.report_actionable) if args.report_actionable is not None else None,
        "reviewer": args.reviewer,
        "review_note": args.note,
        "input_tokens": observation["input_tokens"],
        "output_tokens": observation["output_tokens"],
        "duration_ms": observation["duration_ms"],
        "estimated_cost_usd": args.estimated_cost_usd,
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
                "core_issue_found": record["core_issue_found"],
                "high_risk_confirmed": record["high_risk_confirmed"],
                "report_actionable": record["report_actionable"],
                "result_sha256": observation["result_sha256"],
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
