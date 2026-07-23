#!/usr/bin/env python3
"""Generate the private maintainer-review queue for historical observations."""

from __future__ import annotations

import argparse
import json
import pathlib
import shlex
import sys

from historical_pilot import load_json, validate_manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()

    workspace = args.workspace.resolve(strict=True)
    output = args.output.resolve() if args.output else workspace / "maintainer-review-queue.md"
    manifest = load_json(workspace / "private" / "manifest.json")
    changes = validate_manifest(manifest)
    operator = load_json(workspace / "operator-manifest.json")
    runs = operator.get("runs")
    if not isinstance(runs, list):
        raise ValueError("operator manifest is invalid")
    review_tool = pathlib.Path(__file__).with_name("historical_pilot_review.py").resolve()

    entries: list[tuple[dict[str, object], dict[str, object], dict[str, object], dict[str, object]]] = []
    for mapping in runs:
        if not isinstance(mapping, dict) or not isinstance(mapping.get("run_id"), str):
            raise ValueError("operator run mapping is invalid")
        run_id = str(mapping["run_id"])
        observation_path = workspace / "observations" / f"{run_id}.json"
        if not observation_path.is_file():
            continue
        change = changes[str(mapping["change_id"])]
        if (workspace / "records" / f"{change['id']}.json").exists():
            continue
        entries.append((mapping, change, load_json(observation_path), load_json(workspace / "run-evidence" / f"{run_id}.json")))

    lines = [
        "# Code Quality V1 Historical Pilot Maintainer Review Queue",
        "",
        "> Private operator/maintainer artifact. It contains frozen ground truth and must never be shown to a review Agent.",
        "",
        f"- Frozen code-quality source: `{load_json(workspace / 'baseline.json')['source_commit']}`",
        "- Host/model: local Codex / `gpt-5.6-terra` / `high`",
        f"- Pending maintainer judgments: {len(entries)}",
        "",
        "For each entry, compare the frozen label with both independent reviews. Judge whether each review found the known core issue and whether each MANUAL_REVIEW report gives an actionable fix.",
        "",
    ]
    for index, (mapping, change, observation, evidence) in enumerate(entries, start=1):
        run_id = str(mapping["run_id"])
        command = [
            sys.executable,
            str(review_tool),
            "--workspace",
            str(workspace),
            "--run-id",
            run_id,
            "--reviewer",
            "<maintainer>",
            "--note",
            "<evidence checked>",
        ]
        if change["ground_truth"] == "severe":
            command.extend(["--skill-core-issue-found", "<yes|no>", "--builtin-core-issue-found", "<yes|no>"])
        if observation["skill"]["semantic_result"] == "MANUAL_REVIEW":
            command.extend(["--skill-report-actionable", "<yes|no>"])
        if observation["builtin"]["semantic_result"] == "MANUAL_REVIEW":
            command.extend(["--builtin-report-actionable", "<yes|no>"])
        skill_evidence = evidence["skill"]
        builtin_evidence = evidence["builtin"]
        session = workspace / str(skill_evidence["session"])
        lines.extend(
            [
                f"## {index}. `{change['id']}`",
                "",
                f"- Project: `{change['project_id']}`; run: `{run_id}`",
                f"- Frozen ground truth: `{change['ground_truth']}`",
                f"- Labeler: `{change['labeler']}`",
                f"- Label evidence: {change['label_note']}",
                f"- Change: `{change['base_commit']}` → `{change['target_commit']}`",
                f"- Skill result: `{observation['skill']['semantic_result']}`; findings: `{observation['skill']['finding_count']}`; rules: `{json.dumps(observation['skill']['rule_ids'])}`",
                f"- Built-in result: `{observation['builtin']['semantic_result']}`; findings: `{observation['builtin']['finding_count']}`",
                f"- Trusted diff: `{session / 'input' / 'trusted.diff'}`",
                f"- Skill main review: `{session / 'output' / 'main-review.json'}`",
                f"- Skill final report: `{session / 'output' / 'review-result.md'}`",
                f"- Built-in review: `{workspace / str(builtin_evidence['result'])}`",
                "",
                "```bash",
                shlex.join(command),
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
