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
from historical_pilot_initialize import builtin_task_markdown, materialize_blind_repository, task_markdown  # noqa: E402
from qualification_run import builtin_codex_command, codex_command  # noqa: E402


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
            workspace = workspace.resolve()
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
                first = operator["runs"][0]
                task = json.loads((workspace / first["task"]).read_text(encoding="utf-8"))
                prepared_raw = subprocess.run(
                    [
                        str(workspace / "quality-review"),
                        "prepare",
                        "--host",
                        "codex",
                        "--repo",
                        task["repository"],
                        "--base",
                        task["base"],
                        "--target",
                        task["target"],
                        "--diff-reason",
                        task["diff_reason"],
                        "--output-root",
                        task["output_root"],
                    ],
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )
                prepared = json.loads(prepared_raw.stdout)
                pathlib.Path(prepared["main_review_path"]).write_text(
                    json.dumps(
                        {
                            "activated_rule_families": [],
                            "inactive_rule_families": [
                                {"id": dimension, "reason": "No bottom-line issue in this test observation."}
                                for dimension in ("D1", "D2", "D3", "D4")
                            ],
                            "findings": [],
                            "uninspected_scope": [],
                            "missing_context": [],
                            "inspected_context": [
                                {"path": "input/trusted.diff", "purpose": "Reviewed the committed test change."}
                            ],
                        }
                    ),
                    encoding="utf-8",
                )
                finalized_raw = subprocess.run(
                    [str(workspace / "quality-review"), "finalize", "--session", prepared["session_dir"]],
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )
                finalized = json.loads(finalized_raw.stdout)
                run_root = workspace / "sessions" / first["run_id"]
                operator_root = run_root / "operator"
                operator_root.mkdir()
                transcript = operator_root / "attempt-1.stdout.jsonl"
                transcript.write_text(
                    json.dumps(
                        {"type": "turn.completed", "usage": {"input_tokens": 100, "output_tokens": 20}}
                    )
                    + "\n",
                    encoding="utf-8",
                )
                stderr_path = operator_root / "attempt-1.stderr.log"
                stderr_path.write_text("", encoding="utf-8")
                metadata = operator_root / "attempt-1.metadata.json"
                metadata.write_text(
                    json.dumps(
                        {
                            "schema_version": 1,
                            "run_id": first["run_id"],
                            "host": "codex",
                            "host_version": baseline["codex_version"],
                            "model": "gpt-5.6-terra",
                            "reasoning_effort": "high",
                            "attempt": 1,
                            "command": codex_command(run_root),
                            "returncode": 0,
                            "duration_ms": 123,
                            "stdout": transcript.name,
                            "stderr": stderr_path.name,
                        }
                    ),
                    encoding="utf-8",
                )
                builtin_transcript = operator_root / "builtin-attempt-1.stdout.jsonl"
                builtin_transcript.write_text(
                    "\n".join(
                        [
                            json.dumps(
                                {
                                    "type": "item.completed",
                                    "item": {"type": "agent_message", "text": '{"schema_version":1,"findings":[]}'},
                                }
                            ),
                            json.dumps(
                                {"type": "turn.completed", "usage": {"input_tokens": 80, "output_tokens": 10}}
                            ),
                        ]
                    )
                    + "\n",
                    encoding="utf-8",
                )
                builtin_result = operator_root / "builtin-attempt-1.result.json"
                builtin_result.write_text('{"schema_version":1,"findings":[]}\n', encoding="utf-8")
                builtin_stderr = operator_root / "builtin-attempt-1.stderr.log"
                builtin_stderr.write_text("", encoding="utf-8")
                builtin_worktree = operator_root / "builtin-attempt-1.worktree"
                builtin_metadata = operator_root / "builtin-attempt-1.metadata.json"
                builtin_metadata.write_text(
                    json.dumps(
                        {
                            "schema_version": 1,
                            "run_id": first["run_id"],
                            "lane": "builtin",
                            "host": "codex",
                            "host_version": baseline["codex_version"],
                            "model": "gpt-5.6-terra",
                            "reasoning_effort": "high",
                            "attempt": 1,
                            "command": builtin_codex_command(
                                builtin_worktree,
                                task["target"],
                                workspace / "builtin-review.schema.json",
                            ),
                            "returncode": 0,
                            "duration_ms": 111,
                            "stdout": builtin_transcript.name,
                            "stderr": builtin_stderr.name,
                        }
                    ),
                    encoding="utf-8",
                )
                subprocess.run(
                    [
                        sys.executable,
                        str(PILOT_DIR / "historical_pilot_collect.py"),
                        "--workspace",
                        str(workspace),
                        "--run-id",
                        first["run_id"],
                        "--result",
                        finalized["result_path"],
                        "--transcript",
                        str(transcript),
                        "--runner-metadata",
                        str(metadata),
                        "--input-tokens",
                        "100",
                        "--output-tokens",
                        "20",
                        "--duration-ms",
                        "123",
                        "--builtin-result",
                        str(builtin_result),
                        "--builtin-transcript",
                        str(builtin_transcript),
                        "--builtin-runner-metadata",
                        str(builtin_metadata),
                        "--builtin-input-tokens",
                        "80",
                        "--builtin-output-tokens",
                        "10",
                        "--builtin-duration-ms",
                        "111",
                    ],
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )
                subprocess.run(
                    [
                        sys.executable,
                        str(PILOT_DIR / "historical_pilot_review.py"),
                        "--workspace",
                        str(workspace),
                        "--run-id",
                        first["run_id"],
                        "--reviewer",
                        "maintainer",
                        "--note",
                        "Known issue was not found in the synthetic PASS observation.",
                        "--skill-core-issue-found",
                        "no",
                        "--builtin-core-issue-found",
                        "no",
                    ],
                    check=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                )
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
                self.assertEqual(verification["executed_runs"], 1)
                self.assertEqual(verification["reviewed_runs"], 1)

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
        prompt += builtin_task_markdown(task)
        for forbidden in ("ground_truth", "severe", "normal", "label_note", "fix commit"):
            self.assertNotIn(forbidden, prompt)

    def test_manifest_requires_three_projects_and_thirty_balanced_changes(self) -> None:
        value = manifest()
        self.assertEqual(len(validate_manifest(value)), 30)
        value["changes"] = value["changes"][:29]
        with self.assertRaisesRegex(ValueError, "at least 30"):
            validate_manifest(value)

    def test_summary_compares_skill_and_builtin_review(self) -> None:
        value = manifest()
        with tempfile.TemporaryDirectory() as directory:
            records = pathlib.Path(directory)
            for index, change in enumerate(value["changes"]):
                severe = change["ground_truth"] == "severe"
                skill_result = "MANUAL_REVIEW" if severe else "PASS"
                builtin_result = "MANUAL_REVIEW" if severe or index == 15 else "PASS"
                record = {
                    "schema_version": 1,
                    "change_id": change["id"],
                    "reviewer": "maintainer",
                    "review_note": "evidence checked",
                    "skill": {
                        "semantic_result": skill_result,
                        "core_issue_found": True if severe else None,
                        "report_actionable": True if skill_result == "MANUAL_REVIEW" else None,
                        "input_tokens": 100,
                        "output_tokens": 10,
                        "duration_ms": 1000 + index,
                        "estimated_cost_usd": None,
                    },
                    "builtin": {
                        "semantic_result": builtin_result,
                        "core_issue_found": index != 14 if severe else None,
                        "report_actionable": severe if builtin_result == "MANUAL_REVIEW" else None,
                        "input_tokens": 80,
                        "output_tokens": 8,
                        "duration_ms": 900 + index,
                        "estimated_cost_usd": None,
                    },
                }
                (records / f"{change['id']}.json").write_text(json.dumps(record), encoding="utf-8")

            summary = summarize(value, records)
            self.assertTrue(summary["historical_evidence_complete"])
            self.assertEqual(summary["skill"]["severe_issue_discovery"]["rate"], 1)
            self.assertEqual(summary["builtin"]["severe_issue_discovery"]["found"], 14)
            self.assertEqual(summary["builtin"]["manual_review_findings"]["normal_rate"], 1 / 15)
            self.assertEqual(summary["comparison"]["severe_core_issue_pairs"]["skill_only"], 1)
            self.assertTrue(summary["comparison"]["caught_up_to_builtin_review"])
            self.assertEqual(summary["skill"]["execution"]["input_tokens"], 3000)

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
