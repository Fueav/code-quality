from __future__ import annotations

import datetime as dt
import argparse
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


LIVE_DIR = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(LIVE_DIR))

from live_adjudicate import (
    FINAL_STATUSES,
    adjudicate_pair,
    adjudication_lock,
    archived_result,
    build_summary,
    candidate_commits,
    hunk_touches_line,
    initialize_state,
    run_adjudications,
    transition_status,
)
from native_records import digest


class LiveAdjudicateTest(unittest.TestCase):
    def test_hunk_line_radius_includes_exact_boundary(self) -> None:
        at_boundary = "@@ -30,1 +30,1 @@\n-old\n+new\n"
        outside_boundary = "@@ -31,1 +31,1 @@\n-old\n+new\n"

        self.assertTrue(hunk_touches_line(at_boundary, line=20, radius=10))
        self.assertFalse(hunk_touches_line(outside_boundary, line=20, radius=10))

    def test_state_machine_and_terminal_statuses(self) -> None:
        self.assertEqual(transition_status("open", ["fixes"], age_days=2, has_candidates=True), "confirmed_by_later_fix")
        self.assertEqual(transition_status("open", ["touches_only"], age_days=2, has_candidates=True), "superseded")
        self.assertEqual(transition_status("open", ["unclear"], age_days=40, has_candidates=True), "open")
        self.assertEqual(transition_status("open", [], age_days=29, has_candidates=False), "open")
        self.assertEqual(transition_status("open", [], age_days=30, has_candidates=False), "stale_probable_noise")
        for status in FINAL_STATUSES:
            self.assertEqual(transition_status(status, ["fixes"], age_days=100, has_candidates=True), status)

    def test_candidate_prefilter_uses_synthetic_git_repository(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            repository = pathlib.Path(temporary)
            self.git(repository, "init", "-b", "main")
            source = repository / "app.py"
            source.write_text("".join(f"line_{line} = {line}\n" for line in range(1, 61)), encoding="utf-8")
            base = self.commit(repository, "base")
            lines = source.read_text(encoding="utf-8").splitlines()
            lines[29] = "line_30 = 'boundary'"
            source.write_text("\n".join(lines) + "\n", encoding="utf-8")
            boundary = self.commit(repository, "boundary")
            lines = source.read_text(encoding="utf-8").splitlines()
            lines[40] = "line_41 = 'outside'"
            source.write_text("\n".join(lines) + "\n", encoding="utf-8")
            head = self.commit(repository, "outside")

            candidates = candidate_commits(repository, base, [{"path": "app.py", "line": 20}], head)

            self.assertEqual(candidates, [boundary])

    def test_terminal_finding_is_not_sent_to_pair_adjudication_again(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            repository = root / "repository"
            repository.mkdir()
            self.git(repository, "init", "-b", "main")
            (repository / "app.py").write_text("value = 1\n", encoding="utf-8")
            review_sha = self.commit(repository, "reviewed")
            (repository / "app.py").write_text("value = 2\n", encoding="utf-8")
            self.commit(repository, "later candidate")
            data = root / "data"
            result = review_result(review_sha)
            result_path = data / "reviews" / "fixture" / review_sha / "review-result.json"
            result_path.parent.mkdir(parents=True)
            result_path.write_text(json.dumps(result), encoding="utf-8")
            (data / "index.jsonl").write_text(
                json.dumps(
                    {
                        "schema_version": 2,
                        "origin": "production_cli", "review_key": result["review_key"],
                        "result_sha256": digest(result_path),
                        "repo": "fixture",
                        "sha": review_sha,
                        "reviewed_at": "2026-07-01T00:00:00Z",
                        "duration_seconds": 1,
                        "status": "COMPLETE",
                        "finding_count": 1,
                        "semantic_result": "BLOCK",
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            state = initialize_state("fixture", review_sha, result)
            state["findings"][0]["status"] = "confirmed_by_later_fix"
            state_path = data / "adjudications" / "fixture" / f"{review_sha}.json"
            state_path.parent.mkdir(parents=True)
            state_path.write_text(json.dumps(state), encoding="utf-8")
            config = root / "config.json"
            config.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "codex_bin": "/bin/false",
                        "repositories": [{"name": "fixture", "path": str(repository), "ref": "HEAD"}],
                    }
                ),
                encoding="utf-8",
            )
            marker = root / "runner-called"
            runner = root / "runner.sh"
            runner.write_text(f"#!/bin/sh\ntouch {marker}\nexit 99\n", encoding="utf-8")
            runner.chmod(0o700)
            environment = os.environ.copy()
            environment["LIVE_ADJUDICATION_RUNNER"] = str(runner)

            completed = subprocess.run(
                [
                    sys.executable,
                    str(LIVE_DIR / "live_adjudicate.py"),
                    "--config",
                    str(config),
                    "--data-root",
                    str(data),
                    "--now",
                    "2026-08-10T00:00:00Z",
                ],
                text=True,
                capture_output=True,
                env=environment,
                check=False,
            )

            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertFalse(marker.exists())
            persisted = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(persisted["findings"][0]["decisions"], [])

    def test_summary_maps_confirmed_and_stale_to_existing_live_categories(self) -> None:
        reviews = [
            {"repo": "alpha", "sha": "a", "finding_count": 0},
            {"repo": "alpha", "sha": "b", "finding_count": 2},
            {"repo": "beta", "sha": "c", "finding_count": 1},
        ]
        findings = [
            {"repo": "alpha", "dimension": "D1", "status": "confirmed_by_later_fix"},
            {"repo": "alpha", "dimension": "D4", "status": "open"},
            {"repo": "beta", "dimension": "D4", "status": "stale_probable_noise"},
        ]

        summary = build_summary(reviews, findings, generated_at=dt.datetime(2026, 7, 28, tzinfo=dt.timezone.utc))

        self.assertEqual(summary["reports"]["zero_findings"]["count"], 1)
        self.assertEqual(summary["reports"]["zero_findings"]["ratio"], 1 / 3)
        self.assertEqual(summary["findings"]["labels"]["confirmed_by_later_fix"]["count"], 1)
        self.assertEqual(summary["findings"]["mapped_judgments"]["adopted"]["count"], 1)
        self.assertEqual(summary["findings"]["mapped_judgments"]["noise"]["count"], 1)
        self.assertEqual(summary["findings"]["dimensions"]["D4"]["total"], 2)
        self.assertEqual(summary["repositories"]["alpha"]["findings"], 2)

    def test_pair_uses_read_only_existing_login_without_auth_copy(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            codex = root / "codex"
            codex.write_text("#!/bin/sh\nexit 99\n")
            codex.chmod(0o700)
            def fake_run(command, **kwargs):
                self.assertEqual(command[command.index("-s") + 1], "read-only")
                self.assertIn("--ignore-user-config", command)
                self.assertIn("--ignore-rules", command)
                self.assertEqual(kwargs["env"]["CODEX_HOME"], "/existing-login")
                output = pathlib.Path(command[command.index("--output-last-message") + 1])
                output.write_text(json.dumps({"judgment": "fixes", "reason": "same defect"}))
                self.assertFalse(any(output.parent.rglob("auth.json")))
                return subprocess.CompletedProcess(command, 0, "", "")
            with mock.patch.dict(os.environ, {"CODEX_HOME": "/existing-login"}), \
                 mock.patch("live_adjudicate.hardened_clone"), \
                 mock.patch("live_adjudicate.candidate_diff", return_value="diff"), \
                 mock.patch("live_adjudicate.subprocess.run", side_effect=fake_run):
                result = adjudicate_pair(root, "a" * 40, {"code_locations": []}, codex)
            self.assertEqual(result["judgment"], "fixes")

    def test_native_finding_adaptation_preserves_identity_and_range(self):
        result = review_result("a" * 40)
        state = initialize_state("fixture", "a" * 40, result)
        finding = state["findings"][0]
        self.assertEqual(finding["finding_id"], result["findings"][0]["id"])
        self.assertEqual(finding["code_locations"], [{"path": "app.py", "line": 1, "end_line": 2}])
        self.assertEqual(finding["production_impact"], "The value is wrong.")
        self.assertEqual(finding["dimension"], "unknown")

    def test_old_unknown_and_error_results_are_not_adjudication_samples(self):
        for schema in (None, 1, 9, 11):
            with self.subTest(schema=schema):
                result = review_result("a" * 40)
                result["schema_version"] = schema
                with self.assertRaises(ValueError):
                    initialize_state("fixture", "a" * 40, result)
        result = review_result("a" * 40)
        result["findings"] = []
        result["adjudication"]["semantic_result"] = "ERROR"
        with self.assertRaises(ValueError):
            initialize_state("fixture", "a" * 40, result)

    def test_archive_requires_current_validated_index_and_unchanged_result(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "result.json"
            result = review_result("a" * 40)
            path.write_text(json.dumps(result))
            index = {"schema_version": 2, "origin": "production_cli", "sha": "a" * 40,
                     "review_key": result["review_key"], "result_sha256": digest(path)}
            self.assertEqual(archived_result(index, path), result)
            for changed in ({"schema_version": 1}, {"sha": "b" * 40}, {"result_sha256": "sha256:changed"}):
                with self.subTest(changed=changed), self.assertRaises(ValueError):
                    archived_result({**index, **changed}, path)

    def adjudication_fixture(self, root):
        data = root / "data"
        sha = "a" * 40
        result = review_result(sha)
        result_path = data / "reviews" / "fixture" / sha / "review-result.json"
        result_path.parent.mkdir(parents=True)
        result_path.write_text(json.dumps(result))
        record = {"schema_version": 2, "origin": "production_cli", "status": "COMPLETE",
                  "repo": "fixture", "sha": sha, "review_key": result["review_key"],
                  "result_sha256": digest(result_path), "finding_count": 1,
                  "reviewed_at": "2026-08-01T00:00:00Z"}
        (data / "index.jsonl").write_text(json.dumps(record) + "\n")
        config = root / "config.json"
        config.write_text(json.dumps({"schema_version": 1, "codex_bin": "/bin/false",
                                      "repositories": [{"name": "fixture", "path": str(root), "ref": "HEAD"}]}))
        args = argparse.Namespace(config=config, data_root=data, now="2026-08-10T00:00:00Z")
        state_path = data / "adjudications" / "fixture" / f"{sha}.json"
        return args, state_path

    def test_pair_intent_and_each_completed_decision_survive_interruption(self):
        with tempfile.TemporaryDirectory() as temporary:
            args, state_path = self.adjudication_fixture(pathlib.Path(temporary))
            candidates = ["b" * 40, "c" * 40]
            calls = []

            def pair(repository, candidate, finding, codex):
                saved = json.loads(state_path.read_text())["findings"][0]
                self.assertEqual(saved["pending_adjudication"]["candidate_sha"], candidate)
                calls.append(candidate)
                if candidate == candidates[0]:
                    self.assertEqual(saved["decisions"], [])
                    return {"judgment": "unclear", "reason": "synthetic uncertainty"}
                self.assertEqual(saved["decisions"][0]["candidate_sha"], candidates[0])
                raise KeyboardInterrupt("synthetic process interruption after call intent")

            with mock.patch("live_adjudicate.run_git", return_value="d" * 40), \
                 mock.patch("live_adjudicate.candidate_commits", return_value=candidates), \
                 mock.patch("live_adjudicate.adjudicate_pair", side_effect=pair):
                with self.assertRaises(KeyboardInterrupt):
                    run_adjudications(args)
            self.assertEqual(calls, candidates)
            saved = json.loads(state_path.read_text())["findings"][0]
            self.assertEqual(len(saved["decisions"]), 1)
            self.assertEqual(saved["pending_adjudication"]["candidate_sha"], candidates[1])

            with mock.patch("live_adjudicate.run_git", return_value="d" * 40), \
                 mock.patch("live_adjudicate.adjudicate_pair") as paid_call:
                self.assertEqual(run_adjudications(args), 1)
                paid_call.assert_not_called()
            summary = json.loads((args.data_root / "summary.json").read_text())
            self.assertEqual(summary["status"], "HOLD")
            self.assertIn("Status: `HOLD`", (args.data_root / "summary.md").read_text())

    def test_failed_pair_is_held_without_automatic_paid_retry(self):
        with tempfile.TemporaryDirectory() as temporary:
            args, state_path = self.adjudication_fixture(pathlib.Path(temporary))
            with mock.patch("live_adjudicate.run_git", return_value="d" * 40), \
                 mock.patch("live_adjudicate.candidate_commits", return_value=["b" * 40]), \
                 mock.patch("live_adjudicate.adjudicate_pair", side_effect=TimeoutError("synthetic timeout")) as paid_call:
                self.assertEqual(run_adjudications(args), 1)
                self.assertEqual(run_adjudications(args), 1)
                self.assertEqual(paid_call.call_count, 1)
            self.assertIn("pending_adjudication", json.loads(state_path.read_text())["findings"][0])

    def test_concurrent_cli_fails_before_loading_config_or_starting_provider(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            with adjudication_lock(root):
                completed = subprocess.run([sys.executable, "-B", str(LIVE_DIR / "live_adjudicate.py"),
                                            "--data-root", str(root), "--config", str(root / "missing.json")],
                                           text=True, capture_output=True, check=False)
            self.assertEqual(completed.returncode, 1)
            self.assertIn("already running", completed.stderr)
            self.assertNotIn("missing.json", completed.stderr)
            # An ordinary completion releases the kernel lock for the next run.
            with adjudication_lock(root):
                pass

    def test_kernel_releases_lock_after_process_crash(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            source = ("import os,pathlib,sys; sys.path.insert(0,sys.argv[1]); "
                      "from live_adjudicate import adjudication_lock; "
                      "lock=adjudication_lock(pathlib.Path(sys.argv[2])); lock.__enter__(); os._exit(9)")
            completed = subprocess.run([sys.executable, "-B", "-c", source, str(LIVE_DIR), str(root)], check=False)
            self.assertEqual(completed.returncode, 9)
            with adjudication_lock(root):
                pass

    def commit(self, repository: pathlib.Path, message: str) -> str:
        self.git(repository, "add", ".")
        self.git(repository, "commit", "-m", message)
        return self.git(repository, "rev-parse", "HEAD")

    def git(self, repository: pathlib.Path, *arguments: str) -> str:
        environment = os.environ.copy()
        environment.update(
            {
                "GIT_AUTHOR_NAME": "Live Test",
                "GIT_AUTHOR_EMAIL": "live@example.test",
                "GIT_COMMITTER_NAME": "Live Test",
                "GIT_COMMITTER_EMAIL": "live@example.test",
            }
        )
        completed = subprocess.run(
            ["git", "-C", str(repository), *arguments],
            check=True,
            text=True,
            capture_output=True,
            env=environment,
        )
        return completed.stdout.strip()


def review_result(sha: str) -> dict[str, object]:
    return {
        "schema_version": 10,
        "review_key": "review-v1:sha256:" + "a" * 64,
        "contract_digest": "contract-v1:sha256:" + "b" * 64,
        "review_scope": "FULL", "current_head": sha,
        "contract": {"tool_version": "v-test", "result_schema_version": 10, "provider_host": "codex",
                     "model": "fake", "reasoning_effort": "high", "execution_profile": "production-ci"},
        "execution": {"host": "codex", "model": "fake", "reasoning_effort": "high", "execution_profile": "production-ci"},
        "request": {"base_commit": "base", "target_commit": sha},
        "findings": [{"id": "finding-v1:sha256:" + "c" * 64, "title": "The value is wrong", "priority": 1,
                      "code_location": {"path": "app.py", "start_line": 1, "end_line": 2},
                      "reason": "The value is wrong.", "suggestion": "Restore the value."}],
        "adjudication": {"semantic_result": "BLOCK"},
    }


if __name__ == "__main__":
    unittest.main()
