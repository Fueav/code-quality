from __future__ import annotations

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from argparse import Namespace
from unittest import mock


LIVE_DIR = pathlib.Path(__file__).resolve().parent
WATCH = LIVE_DIR / "live_watch.sh"
sys.path.insert(0, str(LIVE_DIR))
import native_records


class LiveWatchTest(unittest.TestCase):
    def test_install_and_uninstall_preserve_unrelated_crontab_entries(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            fake_bin = root / "fake-bin"
            fake_bin.mkdir()
            crontab_store = root / "crontab.txt"
            crontab = fake_bin / "crontab"
            crontab.write_text(
                """#!/bin/sh
case "$1" in
  -l)
    [ -f "$FAKE_CRONTAB_STORE" ] || exit 1
    /bin/cat "$FAKE_CRONTAB_STORE"
    ;;
  -r)
    /bin/rm -f "$FAKE_CRONTAB_STORE"
    ;;
  *)
    /bin/cp "$1" "$FAKE_CRONTAB_STORE"
    ;;
esac
""",
                encoding="utf-8",
            )
            crontab.chmod(0o700)
            codex = fake_bin / "codex"
            codex.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            codex.chmod(0o700)
            data = root / "data"
            # A user's startup file must not replace the fake commands with host tools.
            (root / ".zshenv").write_text("export PATH=/nonexistent\n", encoding="utf-8")
            environment = os.environ.copy()
            environment.update(
                {
                    "FAKE_CRONTAB_STORE": str(crontab_store),
                    "PATH": str(fake_bin) + os.pathsep + environment["PATH"],
                    "ZDOTDIR": str(root),
                }
            )

            subprocess.run(
                ["/bin/zsh", "-f", str(WATCH), "--uninstall", "--data-root", str(data)],
                check=True,
                text=True,
                capture_output=True,
                env=environment,
            )
            crontab_store.write_text("5 4 * * * /usr/bin/true\n", encoding="utf-8")
            subprocess.run(
                ["/bin/zsh", "-f", str(WATCH), "--install", "--data-root", str(data)],
                check=True,
                text=True,
                capture_output=True,
                env=environment,
            )
            installed = crontab_store.read_text(encoding="utf-8")
            self.assertIn("5 4 * * * /usr/bin/true", installed)
            self.assertIn("# BEGIN code-quality-live", installed)
            self.assertTrue((data / "bin" / "live_watch.sh").is_file())
            self.assertTrue((data / "bin" / "live_adjudicate.py").is_file())

            subprocess.run(
                ["/bin/zsh", "-f", str(WATCH), "--uninstall", "--data-root", str(data)],
                check=True,
                text=True,
                capture_output=True,
                env=environment,
            )
            self.assertEqual(crontab_store.read_text(encoding="utf-8"), "5 4 * * * /usr/bin/true\n")

    def fixture(self, root):
        repository = root / "repository"
        repository.mkdir()
        self.git(repository, "init", "-b", "main")
        (repository / "app.go").write_text("package app\n")
        base = self.commit(repository, "base")
        (repository / "app.go").write_text("package app\nfunc Changed() {}\n")
        target = self.commit(repository, "source change")
        data = root / "data"
        (data / "state").mkdir(parents=True)
        (data / "state/fixture.json").write_text(json.dumps({"schema_version": 1, "watermark": base, "pending": []}))
        config = root / "config.json"
        config.write_text(json.dumps({"schema_version": 1, "repositories": [{"name": "fixture", "path": str(repository), "ref": "HEAD"}]}))
        quality = root / "quality-review"
        quality.write_text(FAKE_CLI)
        quality.chmod(0o700)
        return repository, base, target, data, config, quality

    def run_watch(self, root, config, data, quality, mode="PASS", maximum=10):
        environment = os.environ.copy()
        environment.update({"LIVE_QUALITY_BINARY": str(quality), "FAKE_MODE": mode,
                            "FAKE_LOG": str(root / "calls.jsonl"), "HOST_CODEX_AUTH": str(root / "must-not-be-read")})
        environment.pop("LIVE_REVIEW_RUNNER", None)
        return subprocess.run(["/bin/zsh", "-f", str(WATCH), "--once", "--config", str(config),
                               "--data-root", str(data), "--max", str(maximum)], text=True,
                              capture_output=True, env=environment, check=False)

    def test_formal_cli_pass_and_block_are_completed_without_outer_agent(self):
        for mode in ("PASS", "BLOCK"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                repo, base, target, data, config, quality = self.fixture(root)
                before = self.git(repo, "status", "--porcelain")
                result = self.run_watch(root, config, data, quality, mode)
                self.assertEqual(result.returncode, 0, result.stderr)
                records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
                self.assertEqual(len(records), 1)
                self.assertEqual(records[0]["semantic_result"], mode)
                self.assertEqual(records[0]["status"], "COMPLETE")
                self.assertEqual(records[0]["origin"], "production_cli")
                self.assertEqual(records[0]["stage_metrics"][0]["duration_ms"], 40)
                self.assertEqual(self.git(repo, "status", "--porcelain"), before)
                calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
                self.assertEqual([call[0] for call in calls], ["run-codex", "validate"])
                self.assertEqual(calls[0][calls[0].index("--execution-profile") + 1], "production-ci")
                self.assertEqual(calls[0][calls[0].index("--base") + 1], base)
                self.assertEqual(calls[0][calls[0].index("--target") + 1], target)
                self.assertTrue((data / "reviews/fixture" / target / "review-result.json").is_file())
                self.assertFalse(any(data.rglob("auth.json")))
                self.assertFalse(any(data.rglob("codex-home")))

    def test_error_unknown_schema_and_validation_failure_stay_pending(self):
        for mode in ("ERROR", "UNKNOWN_SCHEMA", "INVALID"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                _, _, target, data, config, quality = self.fixture(root)
                result = self.run_watch(root, config, data, quality, mode)
                self.assertNotEqual(result.returncode, 0)
                records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
                self.assertEqual(len(records), 1)
                self.assertEqual(records[0]["status"], "FAILED")
                self.assertEqual(json.loads((data / "state/fixture.json").read_text())["pending"], [target])
                self.assertFalse((data / "reviews/fixture" / target / "review-result.json").exists())

    def test_restricted_resume_state_never_starts_native_again(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            _, _, target, data, config, quality = self.fixture(root)
            first = self.run_watch(root, config, data, quality, "RESTRICTED_RETRYABLE")
            self.assertNotEqual(first.returncode, 0)
            self.assertTrue((data / "reviews/fixture" / target / "operator-required.json").is_file())
            second = self.run_watch(root, config, data, quality)
            self.assertNotEqual(second.returncode, 0)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            self.assertEqual([call[0] for call in calls], ["run-codex"])
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertEqual([record["status"] for record in records], ["FAILED", "WAITING_OPERATOR"])
            self.assertEqual(records[0]["model"], "fake")
            self.assertEqual([stage["duration_ms"] for stage in records[0]["stage_metrics"]], [40, 40])
            self.assertEqual([stage["input_tokens"] for stage in records[0]["stage_metrics"]], [100, 100])

    def test_manually_published_session_is_adopted_without_new_native(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            _, _, target, data, config, quality = self.fixture(root)
            self.assertNotEqual(self.run_watch(root, config, data, quality, "RESTRICTED_RETRYABLE").returncode, 0)
            marker = data / "reviews/fixture" / target / "operator-required.json"
            session = pathlib.Path(json.loads(marker.read_text())["session_dir"])
            # Simulate operator-driven resume publication; no fake or real provider call here.
            (session / "output/pending-result.json").rename(session / "output/review-result.json")
            checkpoint = json.loads((session / "checkpoint.json").read_text())
            checkpoint["state"] = "PUBLISHED"
            (session / "checkpoint.json").write_text(json.dumps(checkpoint))
            second = self.run_watch(root, config, data, quality)
            self.assertEqual(second.returncode, 0, second.stderr)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            self.assertEqual([call[0] for call in calls], ["run-codex", "validate"])
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertTrue(records[-1]["recovered"])
            self.assertIsNone(records[-1]["wall_duration_ms"])
            self.assertEqual(records[-1]["status"], "COMPLETE")
            self.assertFalse(marker.exists())
            self.assertTrue((marker.parent / "retained-session.json").exists())
            self.assertEqual(json.loads((data / "state/fixture.json").read_text())["pending"], [])

    def test_uncertain_attempt_never_automatically_repeats_provider(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            _, _, target, data, config, quality = self.fixture(root)
            self.assertNotEqual(self.run_watch(root, config, data, quality, "BROKEN_SUMMARY").returncode, 0)
            self.assertTrue((data / "reviews/fixture" / target / "attempt-intent.json").exists())
            self.assertNotEqual(self.run_watch(root, config, data, quality).returncode, 0)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            self.assertEqual([call[0] for call in calls], ["run-codex"])
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertEqual(records[-1]["status"], "WAITING_OPERATOR")

    def test_lost_archive_after_completed_attempt_reuses_publication(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            _, _, target, data, config, quality = self.fixture(root)
            self.assertEqual(self.run_watch(root, config, data, quality).returncode, 0)
            # Simulate the archive/watermark write being lost after provider completion.
            (data / "reviews/fixture" / target / "review-result.json").unlink()
            state_path = data / "state/fixture.json"
            state = json.loads(state_path.read_text())
            state["pending"] = [target]
            state_path.write_text(json.dumps(state))
            second = self.run_watch(root, config, data, quality)
            self.assertEqual(second.returncode, 0, second.stderr)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            self.assertEqual([call[0] for call in calls], ["run-codex", "validate", "validate"])
            self.assertTrue((data / "reviews/fixture" / target / "review-result.json").is_file())
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertIsNotNone(records[0]["wall_duration_ms"])
            self.assertEqual(records[0]["wall_duration_ms"], records[-1]["wall_duration_ms"])

    def test_known_exit_mismatch_cannot_be_reclassified_by_recovery(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            _, _, _, data, config, quality = self.fixture(root)
            self.assertNotEqual(self.run_watch(root, config, data, quality, "WRONG_EXIT").returncode, 0)
            self.assertNotEqual(self.run_watch(root, config, data, quality).returncode, 0)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            self.assertEqual([call[0] for call in calls], ["run-codex", "validate", "validate"])
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertEqual([row["status"] for row in records], ["FAILED", "FAILED"])

    def test_intent_fsync_failure_holds_before_any_provider_call(self):
        for fail_at in (1, 2):
            with self.subTest(fail_at=fail_at), tempfile.TemporaryDirectory() as temporary:
                root = pathlib.Path(temporary)
                config = root / "config.json"
                config.write_text("{}")
                args = Namespace(archive=root / "archive", repo_name="fixture", target="target", base="base",
                                 config=config, quality_bin=root / "fake", repository=root,
                                 result_destination=root / "result.json", record=root / "record.json")
                effects = [None] * (fail_at - 1) + [OSError("simulated fsync failure")]
                with mock.patch("native_records.os.fsync", side_effect=effects), mock.patch("native_records.subprocess.run") as provider:
                    self.assertEqual(native_records.run(args), 1)
                    provider.assert_not_called()
                original = (args.archive / "attempt-intent.json").read_bytes()
                with mock.patch("native_records.subprocess.run") as provider:
                    self.assertEqual(native_records.run(args), 1)
                    provider.assert_not_called()
                self.assertEqual((args.archive / "attempt-intent.json").read_bytes(), original)
                self.assertEqual(json.loads(args.record.read_text())["status"], "WAITING_OPERATOR")

    def test_existing_intent_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "intent.json"
            native_records.persist_exclusive(path, {"target": "first"})
            with self.assertRaises(FileExistsError):
                native_records.persist_exclusive(path, {"target": "second"})
            self.assertEqual(json.loads(path.read_text()), {"target": "first"})

    def test_watermark_filters_large_changes_and_enforces_daily_limit(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            repo, _, first, data, config, quality = self.fixture(root)
            (repo / "README.md").write_text("docs only\n")
            self.commit(repo, "docs")
            (repo / "large.go").write_text("package large\n" + "var value = 1\n" * 3001)
            large = self.commit(repo, "large")
            (repo / "second.py").write_text("value = 2\n")
            second = self.commit(repo, "second")
            (repo / "third.py").write_text("value = 3\n")
            third = self.commit(repo, "third")
            result = self.run_watch(root, config, data, quality, maximum=2)
            self.assertEqual(result.returncode, 0, result.stderr)
            state = json.loads((data / "state/fixture.json").read_text())
            self.assertEqual(state["watermark"], third)
            self.assertEqual(state["pending"], [third])
            records = [json.loads(line) for line in (data / "index.jsonl").read_text().splitlines()]
            self.assertEqual([row["sha"] for row in records], [first, second])
            self.assertNotIn(large, [row["sha"] for row in records])
            result = self.run_watch(root, config, data, quality, maximum=2)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads((data / "state/fixture.json").read_text())["pending"], [])

    def commit(self, repository, message):
        self.git(repository, "add", ".")
        self.git(repository, "-c", "core.hooksPath=/dev/null", "commit", "-m", message)
        return self.git(repository, "rev-parse", "HEAD")

    def git(self, repository, *arguments):
        environment = os.environ.copy()
        environment.update({"GIT_AUTHOR_NAME": "Live Test", "GIT_AUTHOR_EMAIL": "live@example.test",
                            "GIT_COMMITTER_NAME": "Live Test", "GIT_COMMITTER_EMAIL": "live@example.test"})
        return subprocess.run(["git", "-C", str(repository), *arguments], check=True, text=True,
                              capture_output=True, env=environment).stdout.strip()


FAKE_CLI = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
with pathlib.Path(os.environ["FAKE_LOG"]).open("a") as log:
    log.write(json.dumps(args) + "\n")
mode = os.environ["FAKE_MODE"]
if args[0] == "validate":
    raise SystemExit(1 if mode == "INVALID" else 0)
assert args[0] == "run-codex"
def flag(name): return args[args.index(name) + 1]
assert flag("--execution-profile") == "production-ci"
assert flag("--review-scope") == "full"
root = pathlib.Path(flag("--output-root"))
assert (root.parent.parent / "attempt-intent.json").is_file()
session = root / "review-fake"
output = session / "output"
output.mkdir(parents=True)
outcome = mode if mode in ("PASS", "BLOCK", "ERROR") else "PASS"
contract = {"tool_version": "v-test", "result_schema_version": 10, "provider_host": "codex",
            "model": "fake", "reasoning_effort": "high", "execution_profile": "production-ci"}
findings = [{"id": "finding-v1:sha256:" + "c" * 64, "title": "defect", "priority": 1,
             "code_location": {"path": "app.go", "start_line": 1, "end_line": 1},
             "reason": "wrong amount", "suggestion": "fix amount"}] if outcome == "BLOCK" else []
result = {"schema_version": 11 if mode == "UNKNOWN_SCHEMA" else 10,
          "review_key": "review-v1:sha256:" + "a" * 64, "contract_digest": "contract-v1:sha256:" + "b" * 64,
          "review_scope": "FULL", "current_head": flag("--target"),
          "request": {"base_commit": flag("--base"), "target_commit": flag("--target")},
          "contract": contract, "findings": findings,
          "execution": {"host": "codex", "model": "fake", "reasoning_effort": "high",
                        "execution_profile": "production-ci", "restricted_attempts": 1 if findings or mode == "RESTRICTED_RETRYABLE" else 0},
          "adjudication": {"semantic_result": outcome}}
(output / ("pending-result.json" if mode == "RESTRICTED_RETRYABLE" else "review-result.json")).write_text(json.dumps(result))
metrics = {"schema_version": 2, "stage": "NATIVE", "attempt": 1, "duration_ms": 40,
           "input_tokens": 100, "output_tokens": 10, "cached_input_tokens": None}
(output / "native-run-metrics.json").write_text(json.dumps(metrics))
if findings or mode == "RESTRICTED_RETRYABLE":
    restricted = output / "restricted-attempts/0001"
    restricted.mkdir(parents=True)
    (restricted / "restricted-adjudication-metrics.json").write_text(json.dumps({**metrics, "stage": "RESTRICTED"}))
if mode == "RESTRICTED_RETRYABLE":
    (session / "checkpoint.json").write_text(json.dumps({"schema_version": 1, "state": mode,
        "plan": result, "restricted_attempts": [{"attempt": 1}]}))
    print(json.dumps({"schema_version": 1, "state": mode, "session_dir": str(session),
        "review_key": result["review_key"], "contract_digest": result["contract_digest"], "restricted_attempts": 1}))
    raise SystemExit(1)
print("interrupted summary" if mode == "BROKEN_SUMMARY" else json.dumps({"result": outcome, "evidence_dir": str(session)}))
raise SystemExit(1 if mode == "WRONG_EXIT" else {"PASS": 0, "BLOCK": 3, "ERROR": 1}[outcome])
'''


if __name__ == "__main__":
    unittest.main()
