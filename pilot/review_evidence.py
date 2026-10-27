#!/usr/bin/env python3
"""Offline summaries and contract-bound verification of a frozen review benchmark."""

from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
import statistics
import subprocess
import sys

from live.native_records import GROUP_FIELDS, METRIC_FIELDS, TOKEN_FIELDS, contained, digest, integer, read_json, record_from_result, validate_result


def distribution(values: list[int | None]) -> dict:
    known = sorted(value for value in values if integer(value))
    return {"known": len(known), "unknown": len(values) - len(known),
            "p50": statistics.median(known) if known else None,
            "p95": known[max(0, math.ceil(len(known) * .95) - 1)] if known else None,
            "sum": sum(known) if known else None}


def metric_total(record: dict, field: str, stage: str | None = None) -> int | None:
    rows = record.get("stage_metrics", [])
    selected = [row for row in rows if stage is None or row.get("stage") == stage]
    # No restricted attempt is a known zero only for a complete native run.
    if not selected:
        return 0 if stage == "RESTRICTED" and record.get("status") == "COMPLETE" and rows else None
    values = [row.get(field) for row in selected]
    return sum(values) if all(integer(value) for value in values) else None


def summarize(records: list[dict]) -> dict:
    groups: dict[tuple, list] = {}
    for record in records:
        if record.get("schema_version") != 2:
            raise ValueError("records must use current schema 2; legacy records need explicit migration")
        groups.setdefault(tuple(record.get(key) for key in GROUP_FIELDS), []).append(record)
    output = []
    samples = logical_reviews = observations = failed_observations = waiting_checks = 0
    for identity, history in sorted(groups.items(), key=lambda item: repr(item[0])):
        waiting = sum(row.get("status") == "WAITING_OPERATOR" for row in history)
        executions = [row for row in history if row.get("status") != "WAITING_OPERATOR"]
        # A resumed session includes prior attempts. Independent sessions still incur cost,
        # even when their deterministic review key is identical.
        latest = {}
        for index, row in enumerate(executions):
            latest[("session", row["session_dir"]) if row.get("session_dir") else ("row", index)] = row
        rows = list(latest.values())
        review_count = len({("review", row["review_key"]) if row.get("review_key") else ("row", index) for index, row in enumerate(rows)})
        samples += len(rows)
        logical_reviews += review_count
        observations += len(executions)
        waiting_checks += waiting
        failures = sum(row.get("status") != "COMPLETE" or row.get("semantic_result") not in ("PASS", "BLOCK") for row in executions)
        failed_observations += failures
        if not rows:
            continue
        completed = [row for row in rows if row.get("status") == "COMPLETE" and row.get("semantic_result") in ("PASS", "BLOCK")]
        severe = [row for row in rows if row.get("expected_result") == "BLOCK"]
        clean = [row for row in rows if row.get("expected_result") == "PASS"]
        output.append({
            **dict(zip(GROUP_FIELDS, identity)), "samples": len(rows), "logical_reviews": review_count, "completed": len(completed),
            "failed": len(rows) - len(completed),
            "observations": len(executions), "waiting_checks": waiting,
            "failed_observations": failures,
            "quality": {
                "label_scope": "EXPECTED_GATE_ONLY",
                "severe_samples": len(severe), "clean_samples": len(clean),
                "severe_retained": sum(row in completed and row.get("semantic_result") == "BLOCK" for row in severe) if severe else None,
                "false_blocks": sum(row in completed and row.get("semantic_result") == "BLOCK" for row in clean) if clean else None,
                "effectiveness": "LABELED_SUITE_ONLY" if severe and clean else "UNKNOWN",
            },
            "wall_duration_ms": distribution([row.get("wall_duration_ms") for row in rows]),
            "native_duration_ms": distribution([metric_total(row, "duration_ms", "NATIVE") for row in rows]),
            "restricted_duration_ms": distribution([metric_total(row, "duration_ms", "RESTRICTED") for row in rows]),
            **{f"{stage.lower()}_{field}": distribution([metric_total(row, field, stage) for row in rows])
               for stage in ("NATIVE", "RESTRICTED") for field in ("process_duration_ms", "freeze_duration_ms")},
            "tokens": {field: distribution([metric_total(row, field) for row in rows]) for field in TOKEN_FIELDS},
        })
    return {"schema_version": 1, "samples": samples, "logical_reviews": logical_reviews, "observations": observations,
            "failed_observations": failed_observations, "waiting_checks": waiting_checks, "groups": output}


def artifact(reference: dict, root: Path) -> Path:
    if not isinstance(reference, dict) or not isinstance(reference.get("path"), str):
        raise ValueError("artifact path and digest are required")
    raw = Path(reference["path"])
    path = raw if raw.is_absolute() else root / raw
    resolved = path.resolve(strict=True)
    if not resolved.is_relative_to(root.resolve(strict=True)):
        raise ValueError("artifact is outside the frozen evidence bundle")
    boundary = root.resolve(strict=True)
    for component in (path, *path.parents):
        if component.resolve() == boundary:
            break
        if component.is_symlink():
            raise ValueError("artifact symlinks are not allowed")
    if not path.is_file() or digest(path) != reference.get("sha256"):
        raise ValueError("artifact digest mismatch")
    return path


def verify_failed_record(record: dict, case: dict, suite: dict, expected_contract: str, root: Path) -> None:
    """Retain an incomplete execution as failed evidence, never as a clean result."""
    if record.get("semantic_result") is not None or record.get("result_sha256"):
        raise ValueError("failed execution must not assert a result")
    if (not case.get("review_key") or record.get("review_key") != case["review_key"] or
            case.get("contract_digest") != expected_contract or
            record.get("contract_digest") != expected_contract or record.get("current_head") != case["target"] or
            record.get("review_scope") != "FULL" or record.get("execution_profile") != "production-ci"):
        raise ValueError("failed execution identity does not match the frozen benchmark")
    config = suite.get("review_config", {})
    if not record.get("tool_version") or any(not config.get(key) or record.get(field) != config[key]
            for key, field in (("host", "provider"), ("model", "model"), ("reasoning_effort", "reasoning_effort"))):
        raise ValueError("failed execution configuration does not match the frozen benchmark")
    session = contained(Path(record.get("session_dir", "")), root)
    if record.get("wall_duration_ms") is not None and not integer(record["wall_duration_ms"], 1):
        raise ValueError("failed execution wall duration is invalid")
    published = session / "output/review-result.json"
    if published.exists() or published.is_symlink():
        raise ValueError("failed execution hides a published result")
    stages = record.get("stage_metrics")
    if not isinstance(stages, list) or not 1 <= len(stages) <= 3:
        raise ValueError("failed execution needs actual stage metrics")
    for index, row in enumerate(stages):
        stage, attempt = ("NATIVE", 1) if index == 0 else ("RESTRICTED", index)
        if not isinstance(row, dict) or row.get("stage") != stage or not integer(row.get("attempt"), 1) or row["attempt"] != attempt:
            raise ValueError("failed execution stage identity mismatch")
        path = artifact({"path": row.get("artifact_path"), "sha256": row.get("artifact_sha256")}, root)
        expected = session / ("output/native-run-metrics.json" if index == 0 else
                              f"output/restricted-attempts/{attempt:04d}/restricted-adjudication-metrics.json")
        if path.resolve() != expected.resolve():
            raise ValueError("failed execution metrics belong to another session")
        raw = read_json(path)
        if raw.get("schema_version") != 2 or raw.get("stage") != stage or not integer(raw.get("attempt"), 1) or raw["attempt"] != attempt:
            raise ValueError("failed execution metric artifact identity mismatch")
        for field in METRIC_FIELDS:
            value = raw.get(field)
            if value != row.get(field) or (value is not None and not integer(value)):
                raise ValueError("failed execution record differs from stage metrics")
        if not integer(raw.get("duration_ms"), 1):
            raise ValueError("failed execution actual stage duration is missing")


def verify(suite_path: Path, evidence_path: Path, expected_contract: str, quality_bin: Path, allow_incomplete: bool = False) -> dict:
    """Caller supplies trusted labels/contract. Hashes bind evidence, not its human provenance."""
    suite = read_json(suite_path)
    evidence = read_json(evidence_path)
    if suite.get("schema_version") != 1 or not isinstance(suite.get("cases"), list):
        raise ValueError("invalid frozen benchmark suite")
    cases = {}
    for case in suite["cases"]:
        if not isinstance(case, dict) or not case.get("id") or case["id"] in cases or case.get("expected_result") not in ("PASS", "BLOCK"):
            raise ValueError("case identities and expected results must be unique and explicit")
        if not case.get("base") or not case.get("target"):
            raise ValueError("each benchmark case must bind exact base and target commits")
        cases[case["id"]] = case
    if {case["expected_result"] for case in cases.values()} != {"PASS", "BLOCK"}:
        raise ValueError("benchmark needs severe and clean cases")
    if evidence.get("schema_version") != 1 or evidence.get("kind") != "model":
        raise ValueError("structural fixtures cannot qualify model effectiveness")
    if not expected_contract or evidence.get("contract_digest") != expected_contract:
        raise ValueError("evaluation contract does not match the requested release contract")
    if evidence.get("suite_sha256") != digest(suite_path):
        raise ValueError("evaluation does not bind the frozen benchmark")
    if not isinstance(evidence.get("runs"), list):
        raise ValueError("model runs are missing")
    records, seen, seen_records = [], set(), set()
    root = evidence_path.parent.resolve()
    for run in evidence["runs"]:
        if not isinstance(run, dict):
            raise ValueError("invalid benchmark run")
        case_id = run.get("case_id")
        if case_id not in cases or case_id in seen:
            raise ValueError("unknown or duplicate benchmark case")
        seen.add(case_id)
        record_path = artifact(run.get("record"), root)
        if record_path.resolve() in seen_records:
            raise ValueError("benchmark cases must have unique records")
        seen_records.add(record_path.resolve())
        record = read_json(record_path)
        if record.get("schema_version") != 2 or record.get("origin") != "production_cli":
            raise ValueError("model evidence must come from the production CLI adapter")
        case = cases[case_id]
        if allow_incomplete and record.get("status") == "FAILED" and not record.get("result_path"):
            verify_failed_record(record, case, suite, expected_contract, root)
            records.append({**record, "case_id": case_id, "expected_result": case["expected_result"]})
            continue
        result_path = artifact({"path": record.get("result_path"), "sha256": record.get("result_sha256")}, root)
        validation = subprocess.run([str(quality_bin), "validate", str(result_path)], text=True, capture_output=True, check=False)
        if validation.returncode:
            raise ValueError(f"native result validation failed for {case_id}")
        result = read_json(result_path)
        validate_result(result)
        if result["contract_digest"] != expected_contract or result["contract"]["execution_profile"] != "production-ci":
            raise ValueError("result uses an unexpected review contract")
        if result["review_scope"] != "FULL":
            raise ValueError("full-review qualification requires FULL scope")
        if result["current_head"] != case["target"] or result["request"]["base_commit"] != case["base"]:
            raise ValueError("result commit range does not match the benchmark")
        expected = record_from_result(result, result_path.parent.parent, record.get("wall_duration_ms"))
        for key in (*GROUP_FIELDS, "review_key", "session_dir", "status", "semantic_result", "stage_metrics"):
            if expected.get(key) != record.get(key):
                raise ValueError(f"record does not match frozen result/metrics: {key}")
        if record["status"] != "COMPLETE" or not integer(record.get("wall_duration_ms"), 1):
            raise ValueError("incomplete execution cannot qualify effectiveness")
        for stage in record["stage_metrics"]:
            if not integer(stage.get("duration_ms"), 1):
                raise ValueError("actual model stage metrics are missing")
            artifact({"path": stage.get("artifact_path"), "sha256": stage.get("artifact_sha256")}, root)
        records.append({**record, "case_id": case_id, "expected_result": case["expected_result"]})
    if seen != set(cases):
        raise ValueError("benchmark execution is incomplete")
    completed = [row for row in records if row["status"] == "COMPLETE"]
    incomplete = [row["case_id"] for row in records if row["status"] == "FAILED"]
    mismatches = [row["case_id"] for row in completed if row["semantic_result"] != row["expected_result"]]
    if incomplete and not mismatches and {row["semantic_result"] for row in completed} != {"PASS", "BLOCK"}:
        raise ValueError("limited evidence requires completed matching severe and clean cases")
    summary = summarize(records)
    if incomplete:
        for group in summary["groups"]:
            if group["failed"]:
                group["quality"]["effectiveness"] = "INCOMPLETE_LABELED_SUITE"
    return {"schema_version": 1, "status": "BLOCK" if mismatches else ("LIMITED" if incomplete else "PASS"),
            "claim": "FROZEN_SUITE_INCOMPLETE" if incomplete else "FROZEN_SUITE_ONLY",
            "contract_digest": expected_contract, "suite_sha256": digest(suite_path),
            "case_count": len(cases), "completed_cases": [row["case_id"] for row in completed],
            "failed_cases": incomplete, "incomplete_cases": incomplete,
            "mismatches": mismatches, "summary": summary}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    summary = commands.add_parser("summarize")
    summary.add_argument("--records", required=True, type=Path, help="native schema-2 JSONL records")
    check = commands.add_parser("verify")
    check.add_argument("--suite", required=True, type=Path)
    check.add_argument("--evidence", required=True, type=Path)
    check.add_argument("--expected-contract", required=True)
    check.add_argument("--quality-bin", required=True, type=Path)
    check.add_argument("--allow-incomplete", action="store_true", help="retain verified failed executions as LIMITED, never as PASS")
    args = parser.parse_args()
    try:
        if args.command == "summarize":
            records = [json.loads(line) for line in args.records.read_text().splitlines() if line.strip()]
            output = summarize(records)
        else:
            output = verify(args.suite, args.evidence, args.expected_contract, args.quality_bin, args.allow_incomplete)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(json.dumps({"schema_version": 1, "status": "HOLD", "reason": str(error)}))
        return 1
    json.dump(output, sys.stdout, indent=2, sort_keys=True)
    print()
    return 1 if output.get("status") == "BLOCK" else 0


if __name__ == "__main__":
    raise SystemExit(main())
