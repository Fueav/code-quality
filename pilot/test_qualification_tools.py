from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
SOURCE_ROOT = PILOT_DIR.parent
sys.path.insert(0, str(PILOT_DIR))

from qualification_initialize import task_markdown  # noqa: E402
from qualification_inventory import validate_fixture  # noqa: E402
from qualification_batch import pending_runs  # noqa: E402
from qualification_matrix import build_plan, load_cases  # noqa: E402
from qualification_run import codex_command, codex_metrics  # noqa: E402
from qualification_review_packet import smoke_matches  # noqa: E402


class QualificationToolsTest(unittest.TestCase):
    def test_report_only_smoke_matrix_is_codex_only(self) -> None:
        cases = load_cases(SOURCE_ROOT / "evals" / "cases.json")
        plan = build_plan(cases, {})
        self.assertEqual(plan["planned_runs"], 60)
        self.assertEqual(plan["host_totals"], {"codex": 60})
        self.assertEqual(plan["status_totals"], {"missing": 60, "pending": 0, "confirmed": 0, "overturned": 0})

    def test_all_eval_cases_have_reviewable_fixtures(self) -> None:
        cases = load_cases(SOURCE_ROOT / "evals" / "cases.json")
        for case in cases:
            fixture = case["fixture"]
            root = SOURCE_ROOT / "pilot" / "fixtures" / str(case["id"]).lower()
            self.assertEqual(validate_fixture(root, fixture["language"]), [], case["id"])

    def test_blind_task_prompt_does_not_need_case_identity(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            task = {
                "run_id": "run-opaque",
                "host": "codex",
                "repository": root / "repo-opaque",
                "base": "a" * 40,
                "target": "b" * 40,
                "diff_reason": "blind report-only smoke committed increment run-opaque",
                "output_root": root / "sessions" / "run-opaque",
            }
            prompt = task_markdown(task, root / "skill" / "SKILL.md", root / "quality-review")
            for forbidden in ("positive", "counterexample", "insufficient", "DES-", "COR-", "REL-", "SEC-", "CHG-"):
                self.assertNotIn(forbidden, prompt)

    def test_codex_metrics_use_completed_turn(self) -> None:
        raw = "\n".join(
            [
                json.dumps({"type": "thread.started"}),
                json.dumps({"type": "turn.completed", "usage": {"input_tokens": 50, "output_tokens": 25}}),
            ]
        )
        self.assertEqual(codex_metrics(raw), (50, 25))

    def test_codex_runner_uses_terra_high_and_keeps_verifier_threads(self) -> None:
        command = codex_command(pathlib.Path("/qualification/sessions/run-opaque"))
        self.assertNotIn("--ephemeral", command)
        for required in (
            "--ignore-user-config",
            "--ignore-rules",
            "--sandbox",
            "workspace-write",
            "gpt-5.6-terra",
            'model_reasoning_effort="high"',
        ):
            self.assertIn(required, command)

    def test_batch_queue_hides_expected_case_identity(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            workspace = pathlib.Path(directory)
            (workspace / "replay-records").mkdir()
            (workspace / "operator-manifest.json").write_text(
                json.dumps(
                    {
                        "runs": [
                            {"run_id": "run-a", "host": "codex", "case_id": "secret-a"},
                            {"run_id": "run-b", "host": "codex", "case_id": "secret-b"},
                        ]
                    }
                ),
                encoding="utf-8",
            )
            self.assertEqual(pending_runs(workspace, None), ["run-a", "run-b"])

    def test_review_match_uses_coarse_report_only_smoke_contract(self) -> None:
        mapping = {
            "kind": "positive",
            "rule_id": "DES-003",
        }
        observed = {
            "semantic_result": "BLOCK",
            "rule_ids": ["DES-003"],
            "severity": "S3",
            "trigger_confidence": "T3",
            "evidence_level": "E2",
            "duplicate_root_causes": 0,
            "verifier_count": 0,
        }
        self.assertFalse(smoke_matches(mapping, {"observed": observed}))
        observed["verifier_count"] = 1
        observed["rule_ids"] = ["DES-004"]
        observed["severity"] = "S2"
        observed["trigger_confidence"] = "T2"
        observed["evidence_level"] = "E1"
        self.assertTrue(smoke_matches(mapping, {"observed": observed}))

        mapping["kind"] = "counterexample"
        observed["semantic_result"] = "MANUAL_REVIEW"
        observed["verifier_count"] = 0
        self.assertTrue(smoke_matches(mapping, {"observed": observed}))


if __name__ == "__main__":
    unittest.main()
