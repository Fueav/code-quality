from __future__ import annotations

import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
SOURCE_ROOT = PILOT_DIR.parent
sys.path.insert(0, str(PILOT_DIR))

from native_review_feasibility import (  # noqa: E402
    audit_protocol,
    build_baseline_command,
    build_goal_command,
    create_blind_packet,
    materialize_fixture,
    normalize_native_review,
    score_judgments,
)


class NativeReviewFeasibilityTest(unittest.TestCase):
    def test_protocol_freezes_balanced_admitted_sample_and_session_order(self) -> None:
        report = audit_protocol(
            SOURCE_ROOT,
            SOURCE_ROOT / "evals" / "native-review-feasibility-v2.json",
        )
        self.assertTrue(report["ready"])
        self.assertEqual(report["case_count"], 8)
        self.assertEqual(report["kind_totals"], {"counterexample": 4, "positive": 4})
        self.assertEqual(report["dimension_totals"], {"D1": 2, "D2": 2, "D3": 2, "D4": 2})
        self.assertEqual(report["lane_totals"], {"baseline_native": 8, "goal_native": 8})
        self.assertEqual(report["session_count"], 16)
        self.assertEqual(report["maximum_model_calls"], 24)
        self.assertEqual(report["model"], "gpt-5.6-sol")
        self.assertEqual(report["reasoning_effort"], "high")

    def test_lane_commands_preserve_native_and_goal_mode_boundaries(self) -> None:
        baseline = build_baseline_command(pathlib.Path("/opt/codex"), pathlib.Path("/tmp/native.txt"))
        self.assertEqual(baseline[0], "/opt/codex")
        self.assertIn("review", baseline)
        self.assertIn("--commit", baseline)
        self.assertIn("HEAD", baseline)
        self.assertNotIn("--base", baseline)
        self.assertNotIn("--uncommitted", baseline)
        self.assertIn("gpt-5.6-sol", baseline)
        self.assertIn('model_reasoning_effort="high"', baseline)

        goal = build_goal_command(
            pathlib.Path("/opt/quality-review"),
            pathlib.Path("/tmp/repo"),
            "base-sha",
            "target-sha",
            "Preserve the documented behavior.",
            pathlib.Path("/tmp/sessions"),
        )
        self.assertEqual(goal[0], "/opt/quality-review")
        self.assertIn("run-codex", goal)
        self.assertIn("--goal", goal)
        self.assertIn("Preserve the documented behavior.", goal)
        self.assertIn("gpt-5.6-sol", goal)
        self.assertIn("high", goal)

    def test_fixture_materialization_is_a_generic_two_commit_repository(self) -> None:
        fixture = SOURCE_ROOT / "pilot" / "fixtures" / "des-004-counterexample"
        with tempfile.TemporaryDirectory() as directory:
            repository = pathlib.Path(directory) / "repo"
            materialized = materialize_fixture(fixture, repository)
            self.assertEqual(
                subprocess.check_output(["git", "-C", str(repository), "rev-list", "--count", "HEAD"], text=True).strip(),
                "2",
            )
            self.assertEqual(
                subprocess.check_output(["git", "-C", str(repository), "log", "-2", "--format=%s"], text=True).splitlines(),
                ["candidate change", "base snapshot"],
            )
            self.assertEqual(
                subprocess.check_output(["git", "-C", str(repository), "rev-parse", "HEAD^"], text=True).strip(),
                materialized["base_commit"],
            )
            self.assertEqual(
                subprocess.check_output(["git", "-C", str(repository), "rev-parse", "HEAD"], text=True).strip(),
                materialized["target_commit"],
            )
            self.assertNotIn("DES-004", subprocess.check_output(["git", "-C", str(repository), "log", "--oneline"], text=True))

    def test_native_output_normalization_hides_lane_paths(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory) / "opaque-repo"
            root.mkdir()
            for heading in ("Review comment:", "Review comments:", "Full review comment:", "Full review comments:"):
                raw = f"""Assessment.\n\n{heading}\n\n- [P1] Preserve ownership — {root}/handler.go:10-12\n  The change drops the tenant predicate and can update another tenant.\n"""
                findings = normalize_native_review(raw, root)
                self.assertEqual(
                    findings,
                    [
                        {
                            "title": "Preserve ownership",
                            "body": "The change drops the tenant predicate and can update another tenant.",
                            "priority": 1,
                            "path": "handler.go",
                            "start_line": 10,
                            "end_line": 12,
                        }
                    ],
                )
            self.assertEqual(normalize_native_review("The change is safe.\n", root), [])

    def test_blind_packet_hides_lanes_and_frozen_judgments_drive_gate(self) -> None:
        manifest_path = SOURCE_ROOT / "evals" / "native-review-feasibility-v2.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            evidence = root / "evidence"
            for selected in manifest["cases"]:
                for lane in ("baseline_native", "goal_native"):
                    lane_root = evidence / selected["opaque_id"] / lane
                    lane_root.mkdir(parents=True)
                    (lane_root / "run-metadata.json").write_text(
                        json.dumps({"schema_version": 1, "status": "COMPLETE"}),
                        encoding="utf-8",
                    )
                    output = lane_root / "output"
                    output.mkdir()
                    (output / "normalized-findings.json").write_text(
                        json.dumps({"schema_version": 1, "findings": []}),
                        encoding="utf-8",
                    )
            (evidence / "execution-summary.json").write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "ready_for_adjudication": True,
                        "complete_sessions": 16,
                        "actual_model_calls": 16,
                        "maximum_model_calls": 24,
                        "protocol_manifest_sha256": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
                    }
                ),
                encoding="utf-8",
            )
            packet = root / "packet.md"
            mapping = root / "mapping.json"
            judgments = root / "judgments.json"
            create_blind_packet(manifest_path, evidence, packet, mapping, judgments)
            packet_text = packet.read_text(encoding="utf-8")
            self.assertNotIn("baseline_native", packet_text)
            self.assertNotIn("goal_native", packet_text)

            document = json.loads(judgments.read_text(encoding="utf-8"))
            document["status"] = "adjudicated"
            for case in document["cases"]:
                for output in case["outputs"]:
                    output["verdict"] = "pass"
                    output["reason"] = "Synthetic complete judgment."
            judgments.write_text(json.dumps(document), encoding="utf-8")
            outcome = score_judgments(manifest_path, evidence, mapping, judgments)
            self.assertTrue(outcome["expand_to_40"])
            for lane in ("baseline_native", "goal_native"):
                self.assertEqual(outcome["totals"][lane]["positive"]["pass"], 4)
                self.assertEqual(outcome["totals"][lane]["counterexample"]["pass"], 4)


if __name__ == "__main__":
    unittest.main()
