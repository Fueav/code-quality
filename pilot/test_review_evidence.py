"""Offline interface tests. All model results and CLI processes below are synthetic."""

from __future__ import annotations

import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


PILOT = Path(__file__).resolve().parent
sys.path.insert(0, str(PILOT))

import review_evidence as evidence
from live.native_records import digest, record_from_result


CONTRACT = "contract-v1:sha256:" + "a" * 64


def write_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def sample_record(**overrides: object) -> dict:
    record = {
        "schema_version": 2,
        "tool_version": "test-version",
        "contract_digest": CONTRACT,
        "provider": "codex",
        "model": "synthetic-model",
        "reasoning_effort": "high",
        "review_scope": "FULL",
        "status": "COMPLETE",
        "semantic_result": "PASS",
        "wall_duration_ms": 100,
        "stage_metrics": [{
            "stage": "NATIVE", "attempt": 1, "duration_ms": 80,
            "input_tokens": 300, "output_tokens": 20, "cached_input_tokens": None,
        }],
    }
    record.update(overrides)
    return record


class FrozenBundle:
    """Produce temporary fake evidence; never represents a real-model qualification."""

    def __init__(self, root: Path) -> None:
        self.root = root.resolve() / "bundle"
        self.root.mkdir()
        self.suite_path = self.root / "suite.json"
        self.evidence_path = self.root / "evidence.json"
        self.validator = self.root / "quality-review-fake"
        self.validator.write_text(
            f"#!{sys.executable}\n"
            "import json, pathlib, sys\n"
            "root = pathlib.Path(__file__).parent\n"
            "with (root / 'validator-calls.jsonl').open('a') as stream:\n"
            "    stream.write(json.dumps(sys.argv[1:]) + '\\n')\n"
            "if len(sys.argv) != 3 or sys.argv[1] != 'validate':\n"
            "    raise SystemExit(99)\n"
            "if (root / 'reject-validation').exists():\n"
            "    raise SystemExit(1)\n"
            "value = json.loads(pathlib.Path(sys.argv[2]).read_text())\n"
            "raise SystemExit(0 if value.get('schema_version') == 10 else 1)\n",
            encoding="utf-8",
        )
        self.validator.chmod(0o700)
        self.cases = [
            {"id": "severe", "base": "a" * 40, "target": "b" * 40, "expected_result": "BLOCK"},
            {"id": "clean", "base": "a" * 40, "target": "c" * 40, "expected_result": "PASS"},
        ]
        self.results: dict[str, dict] = {}
        self.record_paths: dict[str, Path] = {}
        self.result_paths: dict[str, Path] = {}
        self.document = {"schema_version": 1, "kind": "model", "contract_digest": CONTRACT, "runs": []}
        write_json(self.suite_path, {"schema_version": 1, "cases": self.cases})
        self.document["suite_sha256"] = digest(self.suite_path)
        for index, case in enumerate(self.cases):
            case_id = case["id"]
            session = self.root / case_id / "session"
            result_path = session / "output/review-result.json"
            is_block = case["expected_result"] == "BLOCK"
            result = {
                "schema_version": 10,
                "review_key": "review-v1:sha256:" + str(index + 1) * 64,
                "contract_digest": CONTRACT,
                "review_scope": "FULL",
                "current_head": case["target"],
                "request": {"base_commit": case["base"], "target_commit": case["target"]},
                "contract": {
                    "tool_version": "test-version", "result_schema_version": 10,
                    "provider_host": "codex", "model": "synthetic-model",
                    "reasoning_effort": "high", "execution_profile": "production-ci",
                },
                "execution": {
                    "host": "codex", "model": "synthetic-model", "reasoning_effort": "high",
                    "execution_profile": "production-ci", "native_attempts": 1,
                    "restricted_attempts": int(is_block), "provider_attempts_total": 1 + int(is_block),
                },
                "adjudication": {"semantic_result": case["expected_result"]},
                "findings": [{
                    "id": "finding-v1:sha256:" + "d" * 64, "priority": 1,
                    "title": "Synthetic severe defect", "reason": "Synthetic consequence.",
                    "suggestion": "Restore the synthetic invariant.",
                    "code_location": {"path": "app.go", "start_line": 3, "end_line": 4},
                }] if is_block else [],
            }
            self.results[case_id] = result
            self.result_paths[case_id] = result_path
            self.record_paths[case_id] = self.root / case_id / "record.json"
            write_json(session / "output/native-run-metrics.json", {
                "schema_version": 2, "stage": "NATIVE", "attempt": 1,
                "duration_ms": 80, "input_tokens": 300, "output_tokens": 20, "cached_input_tokens": None,
            })
            if is_block:
                write_json(session / "output/restricted-attempts/0001/restricted-adjudication-metrics.json", {
                    "schema_version": 2, "stage": "RESTRICTED", "attempt": 1,
                    "duration_ms": 30, "input_tokens": 100, "output_tokens": 10, "cached_input_tokens": 0,
                })
            self.document["runs"].append({"case_id": case_id, "record": {}})
            self.refresh_result(case_id)
        self.save_manifest()

    def refresh_result(self, case_id: str) -> None:
        result_path = self.result_paths[case_id]
        write_json(result_path, self.results[case_id])
        record = record_from_result(self.results[case_id], result_path.parent.parent, wall_ms=120)
        record.update(origin="production_cli", result_path=str(result_path), result_sha256=digest(result_path))
        write_json(self.record_paths[case_id], record)
        self.refresh_record_hash(case_id)

    def refresh_record_hash(self, case_id: str) -> None:
        path = self.record_paths[case_id]
        run = next(run for run in self.document["runs"] if run["case_id"] == case_id)
        run["record"] = {"path": str(path.relative_to(self.root)), "sha256": digest(path)}

    def save_manifest(self) -> None:
        write_json(self.evidence_path, self.document)

    def verify(self, expected_contract: str = CONTRACT) -> dict:
        self.save_manifest()
        return evidence.verify(self.suite_path, self.evidence_path, expected_contract, self.validator)


class LimitedEvidenceInterfaceTests(unittest.TestCase):
    """Synthetic failed executions exercise LIMITED without claiming model effectiveness."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="offline-limited-evidence-test-")
        self.addCleanup(self.temporary.cleanup)
        self.fixture_count = 0

    def bundle(self) -> FrozenBundle:
        self.fixture_count += 1
        directory = Path(self.temporary.name) / str(self.fixture_count)
        directory.mkdir()
        bundle = FrozenBundle(directory)
        failed_id = "interrupted"
        synthetic = copy.deepcopy(bundle.results["severe"])
        synthetic["review_key"] = "review-v1:sha256:" + "3" * 64
        synthetic["current_head"] = synthetic["request"]["target_commit"] = "d" * 40
        session = bundle.root / failed_id / "session"
        for relative, stage, duration in (("output/native-run-metrics.json", "NATIVE", 95),
                                          ("output/restricted-attempts/0001/restricted-adjudication-metrics.json", "RESTRICTED", 150)):
            write_json(session / relative, {"schema_version": 2, "stage": stage, "attempt": 1,
                                          "duration_ms": duration, "input_tokens": 400,
                                          "output_tokens": 30, "cached_input_tokens": 0})
        record = record_from_result(synthetic, session, wall_ms=260)
        record.update(status="FAILED", semantic_result=None)
        bundle.results[failed_id] = synthetic
        bundle.result_paths[failed_id] = session / "output/review-result.json"
        bundle.record_paths[failed_id] = bundle.root / failed_id / "record.json"
        write_json(bundle.record_paths[failed_id], record)
        bundle.cases.append({"id": failed_id, "base": "a" * 40, "target": "d" * 40, "expected_result": "BLOCK"})
        bundle.document["runs"].append({"case_id": failed_id, "record": {}})
        bundle.refresh_record_hash(failed_id)
        for case in bundle.cases:
            case["review_key"] = bundle.results[case["id"]]["review_key"]
            case["contract_digest"] = CONTRACT
        self.freeze_suite(bundle, {"schema_version": 1, "cases": bundle.cases,
                                 "review_config": {"host": "codex", "model": "synthetic-model", "reasoning_effort": "high"}})
        return bundle

    def freeze_suite(self, bundle: FrozenBundle, suite: dict) -> None:
        write_json(bundle.suite_path, suite)
        bundle.document["suite_sha256"] = digest(bundle.suite_path)
        bundle.save_manifest()

    def run_verify(self, bundle: FrozenBundle, allow_incomplete: bool = True) -> subprocess.CompletedProcess:
        bundle.save_manifest()
        command = [sys.executable, "-B", str(PILOT / "review_evidence.py"), "verify",
                   "--suite", str(bundle.suite_path), "--evidence", str(bundle.evidence_path),
                   "--expected-contract", CONTRACT, "--quality-bin", str(bundle.validator)]
        if allow_incomplete:
            command.append("--allow-incomplete")
        return subprocess.run(command, text=True, capture_output=True, check=False,
                              env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})

    def assert_hold(self, bundle: FrozenBundle, allow_incomplete: bool = True) -> None:
        completed = self.run_verify(bundle, allow_incomplete)
        self.assertEqual(completed.returncode, 1, completed.stderr)
        self.assertEqual(json.loads(completed.stdout)["status"], "HOLD")

    def test_strict_rejects_failure_but_explicit_limited_retains_three_cases(self) -> None:
        bundle = self.bundle()
        self.assert_hold(bundle, allow_incomplete=False)
        report = evidence.verify(bundle.suite_path, bundle.evidence_path, CONTRACT, bundle.validator, allow_incomplete=True)
        self.assertEqual(report["status"], "LIMITED")
        self.assertEqual(report["case_count"], 3)
        self.assertEqual(report["completed_cases"], ["severe", "clean"])
        self.assertEqual(report["failed_cases"], ["interrupted"])
        self.assertEqual(report["incomplete_cases"], ["interrupted"])
        self.assertEqual(report["mismatches"], [])
        group = report["summary"]["groups"][0]
        self.assertEqual((group["samples"], group["completed"], group["failed"]), (3, 2, 1))
        self.assertEqual(report["summary"]["failed_observations"], 1)
        self.assertEqual(group["quality"]["effectiveness"], "INCOMPLETE_LABELED_SUITE")
        completed = self.run_verify(bundle)
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(json.loads(completed.stdout)["status"], "LIMITED")

    def test_limited_never_exempts_a_completed_wrong_answer(self) -> None:
        bundle = self.bundle()
        result = bundle.results["severe"]
        result["adjudication"]["semantic_result"] = "PASS"
        result["findings"] = []
        result["execution"]["restricted_attempts"] = 0
        bundle.refresh_result("severe")
        completed = self.run_verify(bundle)
        self.assertEqual(completed.returncode, 1, completed.stderr)
        report = json.loads(completed.stdout)
        self.assertEqual(report["status"], "BLOCK")
        self.assertEqual(report["mismatches"], ["severe"])
        self.assertEqual(report["completed_cases"], ["severe", "clean"])
        self.assertEqual(report["failed_cases"], ["interrupted"])

    def test_failed_evidence_tampering_and_hidden_publication_hold(self) -> None:
        record_mutations = {
            "review_key": "review-v1:sha256:" + "f" * 64,
            "contract_digest": "contract-v1:sha256:" + "f" * 64,
            "current_head": "e" * 40, "review_scope": "INCREMENTAL",
            "execution_profile": "interactive", "provider": "claude-code",
            "model": "different-model", "reasoning_effort": "max", "semantic_result": "PASS",
            "result_sha256": "sha256:" + "f" * 64,
        }
        modes = [*record_mutations, "metric_hash", "metric_value", "metric_identity", "metric_path",
                 "negative_metric", "hidden_result", "missing_frozen_review_key"]
        for mode in modes:
            with self.subTest(mode=mode):
                bundle = self.bundle()
                record_path = bundle.record_paths["interrupted"]
                record = json.loads(record_path.read_text())
                row = record["stage_metrics"][0]
                metric_path = Path(row["artifact_path"])
                raw = json.loads(metric_path.read_text())
                if mode in record_mutations:
                    record[mode] = record_mutations[mode]
                elif mode == "metric_hash":
                    metric_path.write_text(metric_path.read_text() + " ")
                elif mode == "metric_value":
                    row["input_tokens"] = 1
                elif mode == "metric_identity":
                    raw["stage"] = "RESTRICTED"
                    write_json(metric_path, raw)
                    row["artifact_sha256"] = digest(metric_path)
                elif mode == "metric_path":
                    other = json.loads(bundle.record_paths["severe"].read_text())
                    record["stage_metrics"][0] = other["stage_metrics"][0]
                elif mode == "negative_metric":
                    raw["input_tokens"] = row["input_tokens"] = -1
                    write_json(metric_path, raw)
                    row["artifact_sha256"] = digest(metric_path)
                elif mode == "hidden_result":
                    write_json(bundle.result_paths["interrupted"], bundle.results["interrupted"])
                elif mode == "missing_frozen_review_key":
                    suite = json.loads(bundle.suite_path.read_text())
                    suite["cases"][-1].pop("review_key")
                    self.freeze_suite(bundle, suite)
                write_json(record_path, record)
                bundle.refresh_record_hash("interrupted")
                self.assert_hold(bundle)

    def test_limited_still_requires_every_case_exactly_once(self) -> None:
        for mode in ("missing", "duplicate"):
            with self.subTest(mode=mode):
                bundle = self.bundle()
                if mode == "missing":
                    bundle.document["runs"].pop()
                else:
                    bundle.document["runs"].append(copy.deepcopy(bundle.document["runs"][-1]))
                self.assert_hold(bundle)

    def test_limited_requires_both_completed_correct_block_and_pass(self) -> None:
        for case_id in ("severe", "clean"):
            with self.subTest(additional_failed_case=case_id):
                bundle = self.bundle()
                path = bundle.record_paths[case_id]
                record = json.loads(path.read_text())
                record.update(status="FAILED", semantic_result=None)
                record.pop("result_path")
                record.pop("result_sha256")
                bundle.result_paths[case_id].unlink()
                write_json(path, record)
                bundle.refresh_record_hash(case_id)
                self.assert_hold(bundle)


class ReviewSummaryTests(unittest.TestCase):
    def test_recovered_review_counts_latest_metrics_once_and_retains_observations(self) -> None:
        review_key = "review-v1:sha256:" + "1" * 64
        session_dir = "/synthetic/recovered-session"
        failed = sample_record(review_key=review_key, session_dir=session_dir, status="FAILED", semantic_result=None, wall_duration_ms=120)
        completed = sample_record(review_key=review_key, session_dir=session_dir, wall_duration_ms=250)
        completed["stage_metrics"].append({
            "stage": "RESTRICTED", "attempt": 1, "duration_ms": 70,
            "input_tokens": 200, "output_tokens": 10, "cached_input_tokens": None,
        })

        summary = evidence.summarize([failed, completed])
        self.assertEqual(summary["samples"], 1)
        self.assertEqual(summary["logical_reviews"], 1)
        self.assertEqual(summary["observations"], 2)
        self.assertEqual(summary["failed_observations"], 1)
        self.assertEqual(len(summary["groups"]), 1)
        group = summary["groups"][0]
        self.assertEqual((group["samples"], group["completed"], group["failed"]), (1, 1, 0))
        self.assertEqual(group["logical_reviews"], 1)
        for field, value in (("wall_duration_ms", 250), ("native_duration_ms", 80), ("restricted_duration_ms", 70)):
            self.assertEqual(group[field], {"known": 1, "unknown": 0, "p50": value, "p95": value, "sum": value})
        self.assertEqual(group["tokens"]["input_tokens"], {"known": 1, "unknown": 0, "p50": 500, "p95": 500, "sum": 500})
        self.assertEqual(group["tokens"]["output_tokens"]["sum"], 30)

        for key in (None, ""):
            with self.subTest(unidentified_review_key=key):
                unidentified = copy.deepcopy([failed, completed])
                for row in unidentified:
                    row["review_key"] = key
                    row.pop("session_dir")
                independent = evidence.summarize(unidentified)
                self.assertEqual(independent["samples"], 2)
                self.assertEqual(independent["logical_reviews"], 2)
                self.assertEqual(independent["groups"][0]["logical_reviews"], 2)
                self.assertEqual(independent["observations"], 2)
                self.assertEqual(independent["groups"][0]["wall_duration_ms"]["sum"], 370)
        unidentified = [{key: value for key, value in row.items() if key not in ("review_key", "session_dir")} for row in (failed, completed)]
        summary = evidence.summarize(unidentified)
        self.assertEqual((summary["samples"], summary["logical_reviews"]), (2, 2))

    def test_independent_sessions_with_same_review_key_keep_all_execution_costs(self) -> None:
        review_key = "review-v1:sha256:" + "3" * 64
        records = [sample_record(review_key=review_key, session_dir="/synthetic/first-session", wall_duration_ms=100),
                   sample_record(review_key=review_key, session_dir="/synthetic/second-session", wall_duration_ms=250)]
        for session_identity in ("distinct", None, "", "missing"):
            with self.subTest(session_identity=session_identity):
                rows = copy.deepcopy(records)
                for row in rows:
                    if session_identity == "missing":
                        row.pop("session_dir")
                    elif session_identity != "distinct":
                        row["session_dir"] = session_identity
                summary = evidence.summarize(rows)
                self.assertEqual((summary["samples"], summary["observations"], summary["logical_reviews"]), (2, 2, 1))
                group = summary["groups"][0]
                self.assertEqual((group["samples"], group["completed"], group["logical_reviews"]), (2, 2, 1))
                self.assertEqual(group["wall_duration_ms"], {"known": 2, "unknown": 0, "p50": 175, "p95": 250, "sum": 350})
                self.assertEqual(group["native_duration_ms"]["sum"], 160)
                self.assertEqual(group["tokens"]["input_tokens"]["sum"], 600)
                self.assertEqual(group["tokens"]["output_tokens"]["sum"], 40)

    def test_waiting_operator_checks_never_count_or_replace_execution_samples(self) -> None:
        review_key = "review-v1:sha256:" + "2" * 64
        session_dir = "/synthetic/waiting-session"
        waiting = sample_record(review_key=review_key, session_dir=session_dir, status="WAITING_OPERATOR", semantic_result=None,
                                wall_duration_ms=99999, stage_metrics=[])
        for status, outcome in (("FAILED", None), ("COMPLETE", "PASS")):
            actual = sample_record(review_key=review_key, session_dir=session_dir, status=status, semantic_result=outcome)
            for rows in ([waiting, actual], [actual, waiting]):
                with self.subTest(status=status, waiting_last=rows[-1] is waiting):
                    summary = evidence.summarize(rows)
                    self.assertEqual(summary["samples"], 1)
                    self.assertEqual(summary["logical_reviews"], 1)
                    self.assertEqual(summary["observations"], 1)
                    self.assertEqual(summary["failed_observations"], int(status == "FAILED"))
                    self.assertEqual(summary["waiting_checks"], 1)
                    self.assertEqual(len(summary["groups"]), 1)
                    group = summary["groups"][0]
                    self.assertEqual(group["logical_reviews"], 1)
                    self.assertEqual((group["samples"], group["completed"], group["failed"]),
                                     (1, int(status == "COMPLETE"), int(status == "FAILED")))
                    self.assertEqual(group["wall_duration_ms"], {"known": 1, "unknown": 0, "p50": 100, "p95": 100, "sum": 100})
                    self.assertEqual(group["native_duration_ms"]["sum"], 80)
                    self.assertEqual(group["tokens"]["input_tokens"]["sum"], 300)

        only_waiting = evidence.summarize([waiting])
        self.assertEqual((only_waiting["samples"], only_waiting["observations"], only_waiting["failed_observations"]), (0, 0, 0))
        self.assertEqual(only_waiting["logical_reviews"], 0)
        self.assertEqual(only_waiting["waiting_checks"], 1)
        self.assertTrue(all(group["samples"] == 0 and group["observations"] == 0 for group in only_waiting["groups"]))

    def test_percentiles_keep_failed_and_unknown_samples_visible(self) -> None:
        rows = [sample_record(wall_duration_ms=value) for value in range(10, 201, 10)]
        rows.append(sample_record(status="FAILED", semantic_result="ERROR", wall_duration_ms=None, stage_metrics=[]))

        summary = evidence.summarize(rows)
        group = summary["groups"][0]

        self.assertEqual(summary["samples"], 21)
        self.assertEqual((group["completed"], group["failed"]), (20, 1))
        self.assertEqual(group["wall_duration_ms"], {"known": 20, "unknown": 1, "p50": 105, "p95": 190, "sum": 2100})
        self.assertEqual(group["native_duration_ms"]["unknown"], 1)
        self.assertEqual(group["tokens"]["cached_input_tokens"], {"known": 0, "unknown": 21, "p50": None, "p95": None, "sum": None})
        self.assertEqual(group["quality"]["effectiveness"], "UNKNOWN")

    def test_groups_do_not_mix_versions_models_effort_scope_or_contracts(self) -> None:
        variants = {
            "tool_version": "another-version", "contract_digest": "contract-v1:sha256:" + "b" * 64,
            "provider": "claude-code", "model": "another-model", "reasoning_effort": "max", "review_scope": "INCREMENTAL",
        }
        rows = [sample_record(), *(sample_record(**{key: value}) for key, value in variants.items())]
        summary = evidence.summarize(rows)
        self.assertEqual(len(summary["groups"]), 7)
        self.assertTrue(all(group["samples"] == 1 for group in summary["groups"]))

    def test_missing_restricted_stage_is_zero_only_for_completed_native_run(self) -> None:
        self.assertEqual(evidence.metric_total(sample_record(), "duration_ms", "RESTRICTED"), 0)
        self.assertIsNone(evidence.metric_total(sample_record(status="FAILED"), "duration_ms", "RESTRICTED"))
        self.assertIsNone(evidence.metric_total(sample_record(stage_metrics=[]), "duration_ms", "RESTRICTED"))
        incomplete = sample_record(stage_metrics=[{"stage": "NATIVE", "duration_ms": 20}, {"stage": "RESTRICTED", "duration_ms": None}])
        self.assertIsNone(evidence.metric_total(incomplete, "duration_ms"))

    def test_error_mislabeled_complete_still_counts_as_failure(self) -> None:
        group = evidence.summarize([sample_record(semantic_result="ERROR")])["groups"][0]
        self.assertEqual((group["completed"], group["failed"]), (0, 1))

    def test_legacy_records_require_explicit_migration(self) -> None:
        with self.assertRaisesRegex(ValueError, "legacy"):
            evidence.summarize([sample_record(schema_version=1)])


class FrozenEvidenceInterfaceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="offline-evidence-test-")
        self.addCleanup(self.temporary.cleanup)
        self.bundle = FrozenBundle(Path(self.temporary.name))

    def test_complete_synthetic_bundle_exercises_only_local_validate(self) -> None:
        result = self.bundle.verify()
        self.assertEqual(result["status"], "PASS")
        self.assertEqual(result["claim"], "FROZEN_SUITE_ONLY")
        self.assertEqual(result["summary"]["samples"], 2)
        self.assertEqual(result["summary"]["groups"][0]["quality"], {
            "label_scope": "EXPECTED_GATE_ONLY",
            "severe_samples": 1, "clean_samples": 1, "severe_retained": 1,
            "false_blocks": 0, "effectiveness": "LABELED_SUITE_ONLY",
        })
        calls = [json.loads(line) for line in (self.bundle.root / "validator-calls.jsonl").read_text().splitlines()]
        self.assertEqual(len(calls), 2)
        self.assertTrue(all(call[0] == "validate" for call in calls))
        for case_id, record_path in self.bundle.record_paths.items():
            record = json.loads(record_path.read_text())
            self.assertEqual(record["session_dir"], str(self.bundle.result_paths[case_id].parent.parent.resolve()))

    def test_stage_process_and_freeze_timings_preserve_unknowns_without_becoming_tokens(self) -> None:
        legacy = json.loads(self.bundle.record_paths["severe"].read_text())
        legacy.pop("review_key")
        legacy.pop("session_dir", None)
        for stage in legacy["stage_metrics"]:
            self.assertIsNone(stage["process_duration_ms"])
            self.assertIsNone(stage["freeze_duration_ms"])

        session = self.bundle.result_paths["severe"].parent.parent
        timings = {
            "output/native-run-metrics.json": (65, 15),
            "output/restricted-attempts/0001/restricted-adjudication-metrics.json": (20, 10),
        }
        for relative, (process, freeze) in timings.items():
            path = session / relative
            metrics = json.loads(path.read_text())
            metrics.update(process_duration_ms=process, freeze_duration_ms=freeze)
            write_json(path, metrics)
        self.bundle.refresh_result("severe")
        measured = json.loads(self.bundle.record_paths["severe"].read_text())
        self.assertEqual([(stage["process_duration_ms"], stage["freeze_duration_ms"])
                          for stage in measured["stage_metrics"]], [(65, 15), (20, 10)])

        group = evidence.summarize([legacy, measured])["groups"][0]
        for field, value in (("native_process_duration_ms", 65), ("native_freeze_duration_ms", 15),
                             ("restricted_process_duration_ms", 20), ("restricted_freeze_duration_ms", 10)):
            self.assertEqual(group[field], {"known": 1, "unknown": 1, "p50": value, "p95": value, "sum": value})
        self.assertEqual(set(group["tokens"]), {"input_tokens", "output_tokens", "cached_input_tokens"})
        self.assertEqual(group["tokens"]["input_tokens"]["sum"], 800)

    def test_structural_fixtures_cannot_qualify_model_effectiveness(self) -> None:
        self.bundle.document["kind"] = "structural"
        with self.assertRaisesRegex(ValueError, "structural"):
            self.bundle.verify()
        self.assertFalse((self.bundle.root / "validator-calls.jsonl").exists())

    def test_missing_manifest_returns_hold_from_cli(self) -> None:
        missing = self.bundle.root / "missing.json"
        environment = os.environ.copy()
        environment["PYTHONDONTWRITEBYTECODE"] = "1"
        completed = subprocess.run([
            sys.executable, "-B", str(PILOT / "review_evidence.py"), "verify",
            "--suite", str(self.bundle.suite_path), "--evidence", str(missing),
            "--expected-contract", CONTRACT, "--quality-bin", str(self.bundle.validator),
        ], text=True, capture_output=True, env=environment, check=False)
        self.assertEqual(completed.returncode, 1, completed.stderr)
        self.assertEqual(json.loads(completed.stdout)["status"], "HOLD")
        self.assertFalse((self.bundle.root / "validator-calls.jsonl").exists())

    def test_contract_mismatch_is_rejected_before_validation(self) -> None:
        with self.assertRaisesRegex(ValueError, "requested release contract"):
            self.bundle.verify("contract-v1:sha256:" + "f" * 64)
        self.assertFalse((self.bundle.root / "validator-calls.jsonl").exists())

    def test_result_contract_cannot_be_hidden_by_manifest(self) -> None:
        self.bundle.results["severe"]["contract_digest"] = "contract-v1:sha256:" + "f" * 64
        self.bundle.refresh_result("severe")
        with self.assertRaisesRegex(ValueError, "unexpected review contract"):
            self.bundle.verify()

    def test_incremental_result_cannot_qualify_full_review(self) -> None:
        self.bundle.results["severe"]["review_scope"] = "INCREMENTAL"
        self.bundle.refresh_result("severe")
        with self.assertRaisesRegex(ValueError, "requires FULL scope"):
            self.bundle.verify()

    def test_changed_suite_invalidates_frozen_evidence(self) -> None:
        suite = json.loads(self.bundle.suite_path.read_text())
        suite["cases"][0]["base"] = "d" * 40
        write_json(self.bundle.suite_path, suite)
        with self.assertRaisesRegex(ValueError, "frozen benchmark"):
            self.bundle.verify()

    def test_result_hash_mismatch_is_rejected(self) -> None:
        path = self.bundle.result_paths["severe"]
        path.write_text(path.read_text() + " ")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            self.bundle.verify()

    def test_record_hash_mismatch_is_rejected(self) -> None:
        path = self.bundle.record_paths["severe"]
        path.write_text(path.read_text() + " ")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            self.bundle.verify()

    def test_actual_stage_metrics_cannot_drift_from_record(self) -> None:
        path = self.bundle.result_paths["severe"].parent / "native-run-metrics.json"
        metrics = json.loads(path.read_text())
        metrics["duration_ms"] = 999
        write_json(path, metrics)
        with self.assertRaisesRegex(ValueError, "record does not match.*stage_metrics"):
            self.bundle.verify()

    def test_forged_record_metrics_are_rejected_even_with_new_record_hash(self) -> None:
        path = self.bundle.record_paths["severe"]
        record = json.loads(path.read_text())
        record["stage_metrics"][0]["input_tokens"] = 1
        write_json(path, record)
        self.bundle.refresh_record_hash("severe")
        with self.assertRaisesRegex(ValueError, "record does not match.*stage_metrics"):
            self.bundle.verify()

    def test_missing_metrics_cannot_be_reclassified_as_real_execution(self) -> None:
        (self.bundle.result_paths["severe"].parent / "native-run-metrics.json").unlink()
        self.bundle.refresh_result("severe")
        with self.assertRaisesRegex(ValueError, "model stage metrics are missing"):
            self.bundle.verify()

    def test_error_result_never_qualifies(self) -> None:
        result = self.bundle.results["severe"]
        result["adjudication"]["semantic_result"] = "ERROR"
        result["findings"] = []
        self.bundle.refresh_result("severe")
        with self.assertRaisesRegex(ValueError, "incomplete execution"):
            self.bundle.verify()

    def test_missing_case_is_not_a_partial_pass(self) -> None:
        self.bundle.document["runs"].pop()
        with self.assertRaisesRegex(ValueError, "execution is incomplete"):
            self.bundle.verify()

    def test_duplicate_case_is_not_counted_twice(self) -> None:
        self.bundle.document["runs"].append(copy.deepcopy(self.bundle.document["runs"][0]))
        with self.assertRaisesRegex(ValueError, "duplicate benchmark case"):
            self.bundle.verify()

    def test_cli_validation_failure_is_not_overridden(self) -> None:
        (self.bundle.root / "reject-validation").write_text("reject")
        with self.assertRaisesRegex(ValueError, "native result validation failed"):
            self.bundle.verify()

    def test_wrong_case_commit_range_is_rejected(self) -> None:
        result = self.bundle.results["severe"]
        result["request"]["base_commit"] = "d" * 40
        self.bundle.refresh_result("severe")
        with self.assertRaisesRegex(ValueError, "commit range"):
            self.bundle.verify()

    def test_valid_model_output_missing_expected_defect_blocks_suite(self) -> None:
        result = self.bundle.results["severe"]
        result["adjudication"]["semantic_result"] = "PASS"
        result["findings"] = []
        result["execution"]["restricted_attempts"] = 0
        self.bundle.refresh_result("severe")
        report = self.bundle.verify()
        self.assertEqual(report["status"], "BLOCK")
        self.assertEqual(report["mismatches"], ["severe"])
        self.assertEqual(report["summary"]["groups"][0]["quality"]["severe_retained"], 0)

    def test_artifact_outside_bundle_is_rejected(self) -> None:
        outside = self.bundle.root.parent / "outside-record.json"
        outside.write_bytes(self.bundle.record_paths["severe"].read_bytes())
        self.bundle.document["runs"][0]["record"] = {"path": str(outside), "sha256": digest(outside)}
        with self.assertRaisesRegex(ValueError, "outside the frozen evidence bundle"):
            self.bundle.verify()

    def test_bundle_symlink_is_rejected(self) -> None:
        link = self.bundle.root / "linked-record.json"
        link.symlink_to(self.bundle.record_paths["severe"])
        self.bundle.document["runs"][0]["record"] = {"path": link.name, "sha256": digest(link)}
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.bundle.verify()


if __name__ == "__main__":
    unittest.main()
