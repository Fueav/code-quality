#!/usr/bin/env python3
"""Build the balanced 100-run host-session qualification schedule."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys


def load_cases(path: pathlib.Path) -> list[dict[str, object]]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    cases = payload.get("cases")
    if not isinstance(cases, list):
        raise ValueError("cases.json must contain a cases array")
    result: list[dict[str, object]] = []
    seen: set[str] = set()
    for index, case in enumerate(cases):
        if not isinstance(case, dict) or not isinstance(case.get("id"), str):
            raise ValueError(f"cases[{index}] is invalid")
        case_id = case["id"]
        if not case_id or case_id in seen:
            raise ValueError(f"case id is missing or duplicated: {case_id}")
        seen.add(case_id)
        result.append(case)
    return result


def load_records(directory: pathlib.Path | None) -> dict[tuple[str, str, int], dict[str, object]]:
    if directory is None:
        return {}
    records: dict[tuple[str, str, int], dict[str, object]] = {}
    for path in sorted(directory.iterdir()):
        if path.suffix != ".json" or not path.is_file() or path.is_symlink():
            continue
        record = json.loads(path.read_text(encoding="utf-8"))
        identity = (record.get("case_id"), record.get("host"), record.get("run_number"))
        if not isinstance(identity[0], str) or identity[1] not in {"claude-code", "codex"} or not isinstance(identity[2], int):
            raise ValueError(f"record identity is invalid: {path.name}")
        if identity in records:
            raise ValueError(f"record identity is duplicated: {identity}")
        record["_source"] = path.name
        records[identity] = record
    return records


def host_for(kind: str, run_number: int, rule_ordinal: int) -> str:
    if kind == "positive":
        if run_number == 1:
            return "claude-code"
        if run_number == 2:
            return "codex"
        return "claude-code" if rule_ordinal % 2 == 0 else "codex"
    return "claude-code" if rule_ordinal % 2 == 0 else "codex"


def build_plan(cases: list[dict[str, object]], records: dict[tuple[str, str, int], dict[str, object]]) -> dict[str, object]:
    rule_ordinals: dict[str, int] = {}
    for case in cases:
        rule_id = case.get("rule_id")
        if not isinstance(rule_id, str):
            raise ValueError(f"{case['id']}.rule_id is invalid")
        if rule_id not in rule_ordinals:
            rule_ordinals[rule_id] = len(rule_ordinals)

    slots: list[dict[str, object]] = []
    planned_identities: set[tuple[str, str, int]] = set()
    host_totals = {"claude-code": 0, "codex": 0}
    status_totals = {"missing": 0, "pending": 0, "confirmed": 0, "overturned": 0}
    for case in cases:
        case_id = str(case["id"])
        rule_id = str(case["rule_id"])
        kind = case.get("kind")
        if kind not in {"positive", "counterexample", "insufficient"}:
            raise ValueError(f"{case_id}.kind is invalid")
        repetitions = 3 if kind == "positive" else 1
        for run_number in range(1, repetitions + 1):
            host = host_for(kind, run_number, rule_ordinals[rule_id])
            identity = (case_id, host, run_number)
            planned_identities.add(identity)
            host_totals[host] += 1
            record = records.get(identity)
            status = "missing"
            source = None
            if record is not None:
                human_review = record.get("human_review")
                if not isinstance(human_review, dict) or human_review.get("status") not in {"pending", "confirmed", "overturned"}:
                    raise ValueError(f"record human status is invalid: {identity}")
                status = str(human_review["status"])
                source = record["_source"]
            status_totals[status] += 1
            slots.append(
                {
                    "case_id": case_id,
                    "rule_id": rule_id,
                    "kind": kind,
                    "run_number": run_number,
                    "host": host,
                    "fixture": f"pilot/fixtures/{case_id.lower()}",
                    "status": status,
                    "record": source,
                }
            )

    unexpected = [
        {"case_id": identity[0], "host": identity[1], "run_number": identity[2], "record": record["_source"]}
        for identity, record in records.items()
        if identity not in planned_identities
    ]
    return {
        "schema_version": 1,
        "visibility": "operator_only_contains_expected_case_identity",
        "planned_runs": len(slots),
        "host_totals": host_totals,
        "status_totals": status_totals,
        "schedule_complete": status_totals["confirmed"] == len(slots) and not unexpected,
        "unexpected_records": unexpected,
        "slots": slots,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cases", type=pathlib.Path, default=pathlib.Path("evals/cases.json"))
    parser.add_argument("--records", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()

    cases_path = args.cases.resolve(strict=True)
    records_path = args.records.resolve(strict=True) if args.records else None
    if records_path is not None and not records_path.is_dir():
        raise ValueError("--records must identify a directory")
    plan = build_plan(load_cases(cases_path), load_records(records_path))
    raw = json.dumps(plan, indent=2, sort_keys=True) + "\n"
    if args.output:
        output = args.output.resolve()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(raw, encoding="utf-8")
    else:
        sys.stdout.write(raw)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
