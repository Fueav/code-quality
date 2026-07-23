#!/usr/bin/env python3
"""Generate an optional private human-review queue for report-only smoke runs."""

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


def smoke_matches(mapping: dict[str, object], record: dict[str, object]) -> bool:
    observed = record.get("observed")
    kind = mapping.get("kind")
    if kind not in {"positive", "counterexample", "insufficient"} or not isinstance(observed, dict):
        return False
    if observed.get("duplicate_root_causes") != 0:
        return False
    rule_ids = observed.get("rule_ids")
    if kind == "positive":
        return (
            observed.get("semantic_result") == "MANUAL_REVIEW"
            and isinstance(rule_ids, list)
            and len(rule_ids) > 0
        )
    return observed.get("semantic_result") in {"PASS", "MANUAL_REVIEW"}


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
        "# Code Quality V1 Optional Human Smoke Review Queue",
        "",
        "> Operator and independent human reviewer only. This file contains expected case identities and must not be shown to a review Agent.",
        "",
        f"- Frozen source: `{load_json(workspace / 'baseline.json')['source_commit']}`",
        "- Host: local Codex",
        "- Model: `gpt-5.6-terra`",
        "- Reasoning effort: `high`",
        f"- Pending optional human decisions: {len(entries)}",
        "",
    ]
    review_tool = pathlib.Path(__file__).with_name("qualification_review.py").resolve()
    for index, (run_id, mapping, record, evidence) in enumerate(entries, start=1):
        session = workspace / str(evidence["session"])
        observed = record["observed"]
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
                f"- Mechanical report-only smoke match: `{'yes' if smoke_matches(mapping, record) else 'no'}`",
                f"- Fixture kind: `{mapping['kind']}`; exact rule and S/T/E equality are not smoke gates.",
                f"- Observed: `{json.dumps(observed, sort_keys=True)}`",
                f"- Trusted diff: `{session / 'input' / 'trusted.diff'}`",
                f"- Main review: `{session / 'output' / 'main-review.json'}`",
                f"- Final report: `{session / 'output' / 'review-result.md'}`",
                "",
                "Optional confirmation should check entry reachability, change attribution, causal chain, and root deduplication; exact rule and S/T/E equality are not required:",
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
