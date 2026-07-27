from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(PILOT_DIR))

from live_report_summary import load_annotations, summarize


class LiveReportSummaryTest(unittest.TestCase):
    def test_summary_counts_judgments_dimensions_and_zero_review_rounds(self) -> None:
        records = [
            annotation("report-1", 1, []),
            annotation("report-2", 2, []),
            annotation(
                "report-3",
                2,
                [
                    finding("F-001", "D1", "adopted"),
                    finding("F-002", "D4", "confirmed_unseen"),
                    finding("F-003", "D4", "noise"),
                ],
            ),
        ]

        summary = summarize(records)

        self.assertEqual(summary["reports"]["total"], 3)
        self.assertEqual(summary["reports"]["zero_findings"]["total"], 2)
        self.assertEqual(summary["reports"]["zero_findings"]["single_round"], 1)
        self.assertEqual(summary["reports"]["zero_findings"]["rereviewed"], 1)
        self.assertEqual(summary["findings"]["total"], 3)
        self.assertEqual(summary["findings"]["judgments"]["confirmed_unseen"]["count"], 1)
        self.assertEqual(summary["findings"]["judgments"]["confirmed_unseen"]["ratio"], 1 / 3)
        self.assertEqual(summary["findings"]["dimensions"], {"D1": 1, "D2": 0, "D3": 0, "D4": 2})

    def test_loader_rejects_invalid_or_duplicate_reports(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "one.json").write_text(json.dumps(annotation("duplicate", 1, [])), encoding="utf-8")
            (root / "two.json").write_text(json.dumps(annotation("duplicate", 2, [])), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "duplicated"):
                load_annotations(root)

    def test_annotation_template_exposes_exact_three_choices(self) -> None:
        template = json.loads((pathlib.Path(__file__).parent / "live_report_annotation_template.json").read_text(encoding="utf-8"))
        self.assertEqual(template["finding_judgment_options"], ["adopted", "noise", "confirmed_unseen"])
        self.assertEqual(template["findings"][0]["judgment"], "<adopted|noise|confirmed_unseen>")


def annotation(report_id: str, review_rounds: int, findings: list[dict[str, object]]) -> dict[str, object]:
    return {
        "schema_version": 1,
        "report_id": report_id,
        "project": "example/service",
        "review_rounds": review_rounds,
        "findings": findings,
    }


def finding(finding_id: str, dimension: str, judgment: str) -> dict[str, object]:
    return {
        "finding_id": finding_id,
        "dimension": dimension,
        "code_locations": [{"path": "app.go", "line": 3}],
        "description": "A concrete production issue.",
        "judgment": judgment,
        "note": "Maintainer checked the behavior.",
    }


if __name__ == "__main__":
    unittest.main()
