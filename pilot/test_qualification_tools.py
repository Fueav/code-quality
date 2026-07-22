from __future__ import annotations

import pathlib
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
SOURCE_ROOT = PILOT_DIR.parent
sys.path.insert(0, str(PILOT_DIR))

from qualification_initialize import task_markdown  # noqa: E402
from qualification_inventory import validate_fixture  # noqa: E402
from qualification_matrix import build_plan, load_cases  # noqa: E402


class QualificationToolsTest(unittest.TestCase):
    def test_frozen_matrix_is_balanced(self) -> None:
        cases = load_cases(SOURCE_ROOT / "evals" / "cases.json")
        plan = build_plan(cases, {})
        self.assertEqual(plan["planned_runs"], 100)
        self.assertEqual(plan["host_totals"], {"claude-code": 50, "codex": 50})
        self.assertEqual(plan["status_totals"], {"missing": 100, "pending": 0, "confirmed": 0, "overturned": 0})

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
                "diff_reason": "blind qualification committed increment run-opaque",
                "output_root": root / "sessions" / "run-opaque",
            }
            prompt = task_markdown(task, root / "skill" / "SKILL.md", root / "quality-review")
            for forbidden in ("positive", "counterexample", "insufficient", "DES-", "COR-", "REL-", "SEC-", "CHG-"):
                self.assertNotIn(forbidden, prompt)


if __name__ == "__main__":
    unittest.main()
