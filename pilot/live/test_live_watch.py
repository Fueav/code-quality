from __future__ import annotations

import json
import os
import pathlib
import subprocess
import tempfile
import unittest


LIVE_DIR = pathlib.Path(__file__).resolve().parent
WATCH = LIVE_DIR / "live_watch.sh"


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
            environment = os.environ.copy()
            environment.update(
                {
                    "FAKE_CRONTAB_STORE": str(crontab_store),
                    "PATH": str(fake_bin) + os.pathsep + environment["PATH"],
                }
            )

            subprocess.run(
                ["/bin/zsh", str(WATCH), "--uninstall", "--data-root", str(data)],
                check=True,
                text=True,
                capture_output=True,
                env=environment,
            )
            crontab_store.write_text("5 4 * * * /usr/bin/true\n", encoding="utf-8")
            subprocess.run(
                ["/bin/zsh", str(WATCH), "--install", "--data-root", str(data)],
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
                ["/bin/zsh", str(WATCH), "--uninstall", "--data-root", str(data)],
                check=True,
                text=True,
                capture_output=True,
                env=environment,
            )
            self.assertEqual(crontab_store.read_text(encoding="utf-8"), "5 4 * * * /usr/bin/true\n")

    def test_watermark_filters_large_changes_and_enforces_daily_limit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            repository = root / "repository"
            repository.mkdir()
            self.git(repository, "init", "-b", "main")
            self.write(repository / "app.go", "package app\n")
            base = self.commit(repository, "base")

            self.write(repository / "app.go", "package app\n\nfunc One() {}\n")
            first = self.commit(repository, "source one")
            self.write(repository / "README.md", "documentation only\n")
            self.commit(repository, "docs")
            self.write(repository / "large.go", "package large\n" + "var value = 1\n" * 3001)
            large = self.commit(repository, "large source change")
            self.write(repository / "second.py", "def second():\n    return 2\n")
            second = self.commit(repository, "source two")
            self.write(repository / "third.ts", "export const third = 3;\n")
            third = self.commit(repository, "source three")

            data = root / "data"
            config = root / "config.json"
            config.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "repositories": [{"name": "fixture", "path": str(repository), "ref": "HEAD"}],
                    }
                ),
                encoding="utf-8",
            )
            state = data / "state" / "fixture.json"
            state.parent.mkdir(parents=True)
            state.write_text(json.dumps({"schema_version": 1, "watermark": base, "pending": []}), encoding="utf-8")
            runner, runner_log = self.fake_runner(root)
            quality = root / "quality-review"
            quality.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            quality.chmod(0o700)

            self.run_watch(config, data, runner, quality, maximum=2)

            first_state = json.loads(state.read_text(encoding="utf-8"))
            self.assertEqual(first_state["watermark"], third)
            self.assertEqual(first_state["pending"], [third])
            reviewed = runner_log.read_text(encoding="utf-8").splitlines()
            self.assertEqual(reviewed, [first, second])
            self.assertNotIn(large, reviewed)
            records = [json.loads(line) for line in (data / "index.jsonl").read_text(encoding="utf-8").splitlines()]
            self.assertEqual([record["sha"] for record in records], [first, second])
            for commit in (first, second):
                self.assertTrue((data / "reviews" / "fixture" / commit / "review-result.json").is_file())

            self.run_watch(config, data, runner, quality, maximum=2)
            second_state = json.loads(state.read_text(encoding="utf-8"))
            self.assertEqual(second_state["pending"], [])
            self.assertEqual(runner_log.read_text(encoding="utf-8").splitlines(), [first, second, third])

    def test_failed_review_is_logged_once_and_left_pending_for_next_run(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            repository = root / "repository"
            repository.mkdir()
            self.git(repository, "init", "-b", "main")
            self.write(repository / "app.go", "package app\n")
            base = self.commit(repository, "base")
            self.write(repository / "app.go", "package app\n\nfunc Broken() {}\n")
            target = self.commit(repository, "target")
            data = root / "data"
            (data / "state").mkdir(parents=True)
            (data / "state" / "fixture.json").write_text(
                json.dumps({"schema_version": 1, "watermark": base, "pending": []}), encoding="utf-8"
            )
            config = root / "config.json"
            config.write_text(
                json.dumps(
                    {"schema_version": 1, "repositories": [{"name": "fixture", "path": str(repository), "ref": "HEAD"}]}
                ),
                encoding="utf-8",
            )
            runner = root / "fail-runner.sh"
            runner.write_text("#!/bin/sh\nexit 9\n", encoding="utf-8")
            runner.chmod(0o700)
            quality = root / "quality-review"
            quality.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            quality.chmod(0o700)

            completed = self.run_watch(config, data, runner, quality, maximum=10, check=False)

            self.assertNotEqual(completed.returncode, 0)
            state = json.loads((data / "state" / "fixture.json").read_text(encoding="utf-8"))
            self.assertEqual(state["pending"], [target])
            index = data / "index.jsonl"
            self.assertTrue(index.is_file(), completed.stderr)
            records = [json.loads(line) for line in index.read_text(encoding="utf-8").splitlines()]
            self.assertEqual(len(records), 1)
            self.assertEqual(records[0]["status"], "FAILED")
            self.assertEqual(records[0]["sha"], target)

    def run_watch(
        self,
        config: pathlib.Path,
        data: pathlib.Path,
        runner: pathlib.Path,
        quality: pathlib.Path,
        maximum: int,
        check: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment.update(
            {
                "LIVE_REVIEW_RUNNER": str(runner),
                "LIVE_QUALITY_BINARY": str(quality),
            }
        )
        return subprocess.run(
            [
                "/bin/zsh",
                str(WATCH),
                "--once",
                "--config",
                str(config),
                "--data-root",
                str(data),
                "--max",
                str(maximum),
            ],
            check=check,
            text=True,
            capture_output=True,
            env=environment,
        )

    def fake_runner(self, root: pathlib.Path) -> tuple[pathlib.Path, pathlib.Path]:
        runner = root / "fake-runner.py"
        log = root / "runner.log"
        runner.write_text(
            """#!/usr/bin/env python3
import json
import pathlib
import sys

_, quality, clone, repo, base, target, result_path = sys.argv
with pathlib.Path(%r).open("a", encoding="utf-8") as stream:
    stream.write(target + "\\n")
result = {
    "policy_version": "1.2.0",
    "request": {"base_commit": base, "target_commit": target},
    "findings": [],
    "adjudication": {"semantic_result": "PASS"},
}
path = pathlib.Path(result_path)
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(result), encoding="utf-8")
"""
            % str(log),
            encoding="utf-8",
        )
        runner.chmod(0o700)
        return runner, log

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

    def write(self, path: pathlib.Path, contents: str) -> None:
        path.write_text(contents, encoding="utf-8")


if __name__ == "__main__":
    unittest.main()
