from __future__ import annotations

import datetime as dt
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest


LIVE_DIR = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(LIVE_DIR))

from live_adjudicate import (
    FINAL_STATUSES,
    build_summary,
    candidate_commits,
    hunk_touches_line,
    initialize_state,
    transition_status,
)


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
                        "schema_version": 1,
                        "repo": "fixture",
                        "sha": review_sha,
                        "reviewed_at": "2026-07-01T00:00:00Z",
                        "duration_seconds": 1,
                        "status": "COMPLETE",
                        "finding_count": 1,
                        "semantic_result": "MANUAL_REVIEW",
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
        "request": {"base_commit": "base", "target_commit": sha},
        "findings": [
            {
                "candidate": {
                    "id": "F-001",
                    "rule_id": "COR-001",
                    "code_locations": [{"path": "app.py", "line": 1}],
                    "production_impact": "The value is wrong.",
                    "minimal_fix": "Restore the value.",
                },
                "final_verdict": "MANUAL_REVIEW",
            }
        ],
        "adjudication": {"semantic_result": "MANUAL_REVIEW"},
    }


if __name__ == "__main__":
    unittest.main()
