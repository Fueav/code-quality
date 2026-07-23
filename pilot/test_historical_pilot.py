from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(PILOT_DIR))

from historical_pilot import summarize, validate_manifest  # noqa: E402


def manifest() -> dict[str, object]:
    projects = [
        {"id": f"project-{index}", "repository": f"/repos/project-{index}", "maintainer": f"owner-{index}"}
        for index in range(1, 4)
    ]
    changes = []
    for index in range(30):
        changes.append(
            {
                "id": f"change-{index + 1:02d}",
                "project_id": f"project-{index % 3 + 1}",
                "base_commit": f"{index + 1:040x}",
                "target_commit": f"{index + 101:040x}",
                "ground_truth": "severe" if index < 15 else "normal",
                "labeler": "maintainer",
                "label_note": "historical outcome checked",
            }
        )
    return {
        "schema_version": 1,
        "profile": "report_only_historical_pilot",
        "projects": projects,
        "changes": changes,
    }


class HistoricalPilotTest(unittest.TestCase):
    def test_manifest_requires_three_projects_and_thirty_balanced_changes(self) -> None:
        value = manifest()
        self.assertEqual(len(validate_manifest(value)), 30)
        value["changes"] = value["changes"][:29]
        with self.assertRaisesRegex(ValueError, "at least 30"):
            validate_manifest(value)

    def test_summary_reports_discovery_false_high_risk_and_actionability(self) -> None:
        value = manifest()
        with tempfile.TemporaryDirectory() as directory:
            records = pathlib.Path(directory)
            for index, change in enumerate(value["changes"]):
                severe = change["ground_truth"] == "severe"
                false_block = index == 15
                semantic_result = "BLOCK" if severe or false_block else "PASS"
                record = {
                    "schema_version": 1,
                    "change_id": change["id"],
                    "semantic_result": semantic_result,
                    "core_issue_found": True if severe else None,
                    "high_risk_confirmed": severe if semantic_result == "BLOCK" else None,
                    "report_actionable": severe if semantic_result == "BLOCK" else None,
                    "reviewer": "maintainer",
                    "review_note": "evidence checked",
                    "input_tokens": 100,
                    "output_tokens": 10,
                    "duration_ms": 1000 + index,
                    "estimated_cost_usd": None,
                }
                (records / f"{change['id']}.json").write_text(json.dumps(record), encoding="utf-8")

            summary = summarize(value, records)
            self.assertTrue(summary["historical_evidence_complete"])
            self.assertEqual(summary["severe_issue_discovery"]["rate"], 1)
            self.assertEqual(summary["high_risk_results"]["false"], 1)
            self.assertEqual(summary["high_risk_results"]["false_rate_on_normal_changes"], 1 / 15)
            self.assertEqual(summary["report_actionability"]["rate"], 15 / 16)
            self.assertEqual(summary["execution"]["input_tokens"], 3000)


if __name__ == "__main__":
    unittest.main()
