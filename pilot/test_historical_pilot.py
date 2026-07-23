from __future__ import annotations

import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
SOURCE_ROOT = PILOT_DIR.parent
sys.path.insert(0, str(PILOT_DIR))

from historical_pilot import summarize, validate_manifest  # noqa: E402
from historical_pilot_initialize import materialize_blind_repository, task_markdown  # noqa: E402


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
    def test_initializer_builds_thirty_opaque_tasks(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            projects = []
            changes = []
            for project_index in range(3):
                repository = root / f"project-{project_index + 1}"
                repository.mkdir()
                self.git(repository, "init", "--quiet")
                self.git(repository, "config", "user.name", "Pilot Test")
                self.git(repository, "config", "user.email", "pilot@example.invalid")
                tracked = repository / "app.txt"
                tracked.write_text("base\n", encoding="utf-8")
                self.git(repository, "add", "app.txt")
                self.git(repository, "commit", "--quiet", "-m", "base")
                project_id = f"project-{project_index + 1}"
                projects.append({"id": project_id, "repository": str(repository), "maintainer": "maintainer"})
                for change_index in range(10):
                    base = self.git(repository, "rev-parse", "HEAD")
                    tracked.write_text(f"change {change_index + 1}\n", encoding="utf-8")
                    self.git(repository, "commit", "--quiet", "-am", f"change {change_index + 1}")
                    target = self.git(repository, "rev-parse", "HEAD")
                    changes.append(
                        {
                            "id": f"{project_id}-change-{change_index + 1:02d}",
                            "project_id": project_id,
                            "base_commit": base,
                            "target_commit": target,
                            "ground_truth": "severe" if change_index < 5 else "normal",
                            "labeler": "maintainer",
                            "label_note": "confirmed historical evidence",
                        }
                    )
            manifest_path = root / "manifest.json"
            manifest_path.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "profile": "report_only_historical_pilot",
                        "projects": projects,
                        "changes": changes,
                    }
                ),
                encoding="utf-8",
            )
            workspace = root / "workspace"
            source_dirty = bool(self.git(SOURCE_ROOT, "status", "--porcelain=v1", "--untracked-files=all"))
            completed = subprocess.run(
                [
                    sys.executable,
                    str(PILOT_DIR / "historical_pilot_initialize.py"),
                    "--source",
                    str(SOURCE_ROOT),
                    "--manifest",
                    str(manifest_path),
                    "--output",
                    str(workspace),
                    "--allow-dirty-development",
                ],
                check=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            self.assertEqual(json.loads(completed.stdout)["planned_runs"], 30)
            baseline = json.loads((workspace / "baseline.json").read_text(encoding="utf-8"))
            self.assertEqual(baseline["development_only"], source_dirty)
            operator = json.loads((workspace / "operator-manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(len(operator["runs"]), 30)
            for mapping in operator["runs"]:
                task_path = workspace / mapping["task"]
                raw = task_path.read_text(encoding="utf-8") + task_path.with_suffix(".md").read_text(encoding="utf-8")
                self.assertNotIn(mapping["change_id"], raw)
                self.assertNotIn(mapping["project_id"], raw)
                self.assertNotIn(mapping["ground_truth"], raw)
            if not source_dirty:
                verified = subprocess.run(
                    [
                        sys.executable,
                        str(PILOT_DIR / "historical_pilot_verify.py"),
                        "--source",
                        str(SOURCE_ROOT),
                        "--workspace",
                        str(workspace),
                    ],
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )
                verification = json.loads(verified.stdout)
                self.assertEqual(verification["planned_runs"], 30)
                self.assertEqual(verification["executed_runs"], 0)

    def test_materialized_repository_excludes_future_fix(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / "source"
            source.mkdir()
            self.git(source, "init", "--quiet")
            self.git(source, "config", "user.name", "Pilot Test")
            self.git(source, "config", "user.email", "pilot@example.invalid")
            tracked = source / "app.txt"
            tracked.write_text("base\n", encoding="utf-8")
            self.git(source, "add", "app.txt")
            self.git(source, "commit", "--quiet", "-m", "base")
            base = self.git(source, "rev-parse", "HEAD")
            tracked.write_text("problem\n", encoding="utf-8")
            self.git(source, "commit", "--quiet", "-am", "introduce problem")
            target = self.git(source, "rev-parse", "HEAD")
            tracked.write_text("fixed\n", encoding="utf-8")
            self.git(source, "commit", "--quiet", "-am", "fix problem")
            future = self.git(source, "rev-parse", "HEAD")

            output = root / "blind"
            metadata = materialize_blind_repository(source, base, target, output)
            self.assertEqual(metadata["base"], base)
            self.assertEqual(metadata["target"], target)
            self.assertEqual(self.git(output, "status", "--porcelain=v1", "--untracked-files=all"), "")
            hidden = subprocess.run(
                ["git", "-C", str(output), "cat-file", "-e", f"{future}^{{commit}}"],
                check=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            self.assertNotEqual(hidden.returncode, 0)

    def test_historical_task_does_not_leak_label(self) -> None:
        task = {
            "run_id": "run-opaque",
            "repository": "/pilot/repositories/repo-opaque",
            "base": "a" * 40,
            "target": "b" * 40,
            "diff_reason": "blind historical committed change run-opaque",
            "output_root": "/pilot/sessions/run-opaque",
        }
        prompt = task_markdown(task, pathlib.Path("/pilot/SKILL.md"), pathlib.Path("/pilot/quality-review"))
        for forbidden in ("ground_truth", "severe", "normal", "label_note", "fix commit"):
            self.assertNotIn(forbidden, prompt)

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

    @staticmethod
    def git(repository: pathlib.Path, *arguments: str) -> str:
        completed = subprocess.run(
            ["git", "-C", str(repository), *arguments],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        return completed.stdout.strip()


if __name__ == "__main__":
    unittest.main()
