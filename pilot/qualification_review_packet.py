#!/usr/bin/env python3
"""Generate the private independent-human-review queue for collected runs."""

from __future__ import annotations

import argparse
import json
import pathlib
import shlex
import sys


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def expected_matches(mapping: dict[str, object], record: dict[str, object]) -> bool:
    expected = mapping.get("expected")
    observed = record.get("observed")
    if not isinstance(expected, dict) or not isinstance(observed, dict):
        return False
    if observed.get("semantic_result") != expected.get("semantic_result"):
        return False
    rule_ids = observed.get("rule_ids")
    verifier_count = observed.get("verifier_count")
    if expected.get("verifier_result") == "confirmed" and verifier_count != 1:
        return False
    if expected.get("verifier_result") == "not_run" and verifier_count != 0:
        return False
    finding_count = len(rule_ids) if isinstance(rule_ids, list) else -1
    if finding_count != expected.get("finding_count"):
        return False
    if expected.get("finding_count") == 0:
        return rule_ids == []
    return (
        rule_ids == [mapping.get("rule_id")]
        and observed.get("severity") == expected.get("severity")
        and observed.get("trigger_confidence") == expected.get("trigger_confidence")
        and observed.get("evidence_level") == expected.get("evidence_level")
        and observed.get("duplicate_root_causes") == 0
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    output = args.output.resolve() if args.output else workspace / "human-review-queue.md"
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list):
        raise ValueError("operator manifest is invalid")
    mapping_by_run = {
        str(mapping["run_id"]): mapping
        for mapping in runs
        if isinstance(mapping, dict) and isinstance(mapping.get("run_id"), str)
    }
    entries: list[tuple[str, dict[str, object], dict[str, object], dict[str, object]]] = []
    for record_path in sorted((workspace / "replay-records").glob("*.json")):
        record = load_json(record_path)
        human = record.get("human_review")
        if not isinstance(human, dict) or human.get("status") != "pending":
            continue
        run_id = record_path.stem
        mapping = mapping_by_run.get(run_id)
        if mapping is None:
            raise ValueError(f"unknown replay run: {run_id}")
        evidence = load_json(workspace / "run-evidence" / f"{run_id}.json")
        entries.append((run_id, mapping, record, evidence))

    lines = [
        "# Code Quality V1 Private Human Review Queue",
        "",
        "> Operator and independent human reviewer only. This file contains expected case identities and must not be shown to a review Agent.",
        "",
        f"- Frozen source: `{load_json(workspace / 'baseline.json')['source_commit']}`",
        "- Host: local Codex",
        "- Model: `gpt-5.6-terra`",
        "- Reasoning effort: `high`",
        f"- Pending human decisions: {len(entries)}",
        "",
    ]
    review_tool = pathlib.Path(__file__).with_name("qualification_review.py").resolve()
    for index, (run_id, mapping, record, evidence) in enumerate(entries, start=1):
        session = workspace / str(evidence["session"])
        observed = record["observed"]
        expected = mapping["expected"]
        confirm_command = shlex.join(
            [
                sys.executable,
                str(review_tool),
                "--workspace",
                str(workspace),
                "--run-id",
                run_id,
                "--status",
                "confirmed",
                "--reviewer",
                "<person>",
                "--note",
                "<evidence checked>",
            ]
        )
        lines.extend(
            [
                f"## {index}. `{run_id}`",
                "",
                f"- Case: `{mapping['case_id']}`; rule: `{mapping['rule_id']}`; kind: `{mapping['kind']}`; repetition: `{mapping['run_number']}`",
                f"- Mechanical expected match: `{'yes' if expected_matches(mapping, record) else 'no'}`",
                f"- Expected: `{json.dumps(expected, sort_keys=True)}`",
                f"- Observed: `{json.dumps(observed, sort_keys=True)}`",
                f"- Trusted diff: `{session / 'input' / 'trusted.diff'}`",
                f"- Target snapshot: `{session / 'input' / 'repository'}`",
                f"- Main review: `{session / 'output' / 'main-review.json'}`",
                f"- Verifier review: `{session / 'output' / 'verifier-review.json'}`",
                f"- Final report: `{session / 'output' / 'review-result.md'}`",
                "",
                "Confirm only after checking entry reachability, change attribution, trigger facts, causal chain, rule/S/T/E, root deduplication, and verifier evidence:",
                "",
                "```bash",
                confirm_command,
                "```",
                "",
            ]
        )
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("\n".join(lines), encoding="utf-8")
    json.dump({"output": str(output), "pending_reviews": len(entries), "status": "generated"}, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
