from __future__ import annotations

import importlib.util
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("restricted_adjudication.py")
SPEC = importlib.util.spec_from_file_location("restricted_adjudication", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def finding(finding_id: str) -> dict[str, object]:
    return {
        "id": finding_id,
        "priority": 1,
        "title": "Material issue",
        "reason": "Reason",
        "suggestion": "Fix",
        "code_location": {"path": "main.go", "start_line": 1, "end_line": 1},
    }


def adjudication(finding_id: str, **overrides: object) -> dict[str, object]:
    value: dict[str, object] = {
        "finding_id": finding_id,
        "validity": "SUPPORTED",
        "severity": "S3",
        "trigger_confidence": "T3",
        "evidence_level": "E2",
        "introduced_or_worsened_by_change": True,
        "trigger_condition_is_concrete": True,
        "causal_chain_is_complete": True,
        "finding_is_not_style_preference": True,
        "recommended_disposition": "BLOCK",
        "evidence_refs": [
            {"path": "main.go", "start_line": 1, "end_line": 1, "support": "Reachable path."}
        ],
        "uncertainties": [],
        "reason": "The complete path is proven.",
    }
    value.update(overrides)
    return value


class RestrictedAdjudicationTests(unittest.TestCase):
    def test_native_block_exit_is_a_completed_product_result(self) -> None:
        self.assertTrue(MODULE.native_call_completed(0, False))
        self.assertTrue(MODULE.native_call_completed(3, False))
        self.assertFalse(MODULE.native_call_completed(1, False))
        self.assertFalse(MODULE.native_call_completed(3, True))

    def test_block_requires_every_formula_term_and_valid_evidence(self) -> None:
        value = adjudication("finding-v1:sha256:" + "a" * 64)
        self.assertEqual(MODULE.computed_disposition(value), "BLOCK")
        self.assertEqual(MODULE.computed_disposition(value, valid_evidence_refs=False), "MANUAL_REVIEW")
        for field in (
            "introduced_or_worsened_by_change",
            "trigger_condition_is_concrete",
            "causal_chain_is_complete",
            "finding_is_not_style_preference",
        ):
            changed = dict(value)
            changed[field] = False
            self.assertEqual(MODULE.computed_disposition(changed), "MANUAL_REVIEW")

    def test_conditional_or_insufficient_finding_cannot_block(self) -> None:
        finding_id = "finding-v1:sha256:" + "b" * 64
        self.assertEqual(
            MODULE.computed_disposition(adjudication(finding_id, trigger_confidence="T2")),
            "MANUAL_REVIEW",
        )
        self.assertEqual(
            MODULE.computed_disposition(adjudication(finding_id, validity="INSUFFICIENT")),
            "MANUAL_REVIEW",
        )
        self.assertEqual(
            MODULE.computed_disposition(adjudication(finding_id, validity="CONTRADICTED")),
            "REJECT",
        )

    def test_payload_must_preserve_finding_identity_and_order(self) -> None:
        first = "finding-v1:sha256:" + "c" * 64
        second = "finding-v1:sha256:" + "d" * 64
        with tempfile.TemporaryDirectory() as directory:
            repository = pathlib.Path(directory)
            (repository / "main.go").write_text("package main\n", encoding="utf-8")
            payload = {"adjudications": [adjudication(first), adjudication(second)]}
            normalized = MODULE.validate_adjudication_payload(
                payload,
                [finding(first), finding(second)],
                repository,
            )
            self.assertEqual([item["computed_disposition"] for item in normalized], ["BLOCK", "BLOCK"])
            payload["adjudications"].reverse()
            with self.assertRaisesRegex(ValueError, "ID/order mismatch"):
                MODULE.validate_adjudication_payload(
                    payload,
                    [finding(first), finding(second)],
                    repository,
                )

    def test_exact_paired_pvalue(self) -> None:
        self.assertEqual(MODULE.exact_paired_pvalue(0, 0), 1.0)
        self.assertEqual(MODULE.exact_paired_pvalue(0, 5), 0.0625)
        self.assertEqual(MODULE.exact_paired_pvalue(0, 6), 0.03125)

    def test_preference_requires_significance_and_no_retention_loss(self) -> None:
        rows = []
        for index in range(15):
            rows.append(
                {
                    "label": "severe",
                    "baseline_block": True,
                    "treatment_block": True,
                }
            )
        for index in range(15):
            rows.append(
                {
                    "label": "normal",
                    "baseline_block": index < 6,
                    "treatment_block": False,
                }
            )
        scored = MODULE.score_rows(rows, integrity_ok=True)
        self.assertEqual(scored["decision"], "PREFER_RESTRICTED_ADJUDICATION")
        rows[0]["treatment_block"] = False
        scored = MODULE.score_rows(rows, integrity_ok=True)
        self.assertEqual(scored["decision"], "KEEP_CURRENT_BEHAVIOR")

    def test_prompt_contains_findings_but_not_label_keys(self) -> None:
        finding_id = "finding-v1:sha256:" + "e" * 64
        prompt = MODULE.adjudication_prompt("a" * 40, "b" * 40, [finding(finding_id)])
        self.assertIn(finding_id, prompt)
        self.assertNotIn("ground_truth", prompt)
        self.assertNotIn("label_note", prompt)


if __name__ == "__main__":
    unittest.main()
