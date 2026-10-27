"""Human approval decisions are tested against explicit offline GitHub facts."""

import copy
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location("ci_approval", Path(__file__).with_name("ci_approval.py"))
approval = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(approval)
HEAD, BASE = "a" * 40, "b" * 40
REPO = "example/code-quality"


class Facts:
    def __init__(self):
        self.pr = {"state": "open", "draft": False, "head": {"sha": HEAD},
                   "base": {"sha": BASE}, "user": {"login": "author"}}
        self.reviews = [{"id": 1, "state": "APPROVED", "commit_id": HEAD,
                         "user": {"login": "owner", "type": "User"}}]
        self.permission = {"permission": "admin", "role_name": "admin"}
        self.runs = [{"id": 17, "run_attempt": 1, "head_sha": HEAD, "event": "pull_request"}]
        self.jobs = [{"name": "candidate", "conclusion": "success"}]
        self.artifacts = [{"name": f"candidate-{HEAD}", "expired": False}]
        self.main = HEAD
        self.pr_reads = 0
        self.move_on_second_read = False

    def get(self, endpoint):
        if endpoint == f"repos/{REPO}/pulls/1":
            self.pr_reads += 1
            result = copy.deepcopy(self.pr)
            if self.move_on_second_read and self.pr_reads > 1:
                result["head"]["sha"] = "c" * 40
            return result
        if "/collaborators/" in endpoint:
            return self.permission
        if endpoint == f"repos/{REPO}/git/ref/heads/main":
            return {"object": {"sha": self.main}}
        raise AssertionError(f"unexpected API call: {endpoint}")

    def pages(self, endpoint, field=None):
        if endpoint.endswith("/reviews"):
            return self.reviews
        if "/workflows/ci.yml/runs?" in endpoint:
            return self.runs
        if endpoint.endswith("/jobs"):
            return self.jobs
        if endpoint.endswith("/artifacts"):
            return self.artifacts
        raise AssertionError(f"unexpected paged API call: {endpoint}")


class ApprovalTests(unittest.TestCase):
    def setUp(self):
        self.api = Facts()

    def review(self):
        return approval.review_approval(self.api, REPO, 1, HEAD, BASE)

    def test_current_human_owner_approval_reuses_candidate(self):
        result = self.review()
        self.assertEqual(result["decision"], "APPROVED")
        self.assertEqual(result["candidate_run_id"], 17)
        self.assertEqual(result["human_approvals"][0]["commit_id"], HEAD)
        self.assertEqual(self.api.pr_reads, 2)

    def test_maintainer_role_is_recognized_without_granting_all_writers(self):
        self.api.permission = {"permission": "write", "role_name": "maintain"}
        self.review()
        self.api.permission["role_name"] = "write"
        with self.assertRaisesRegex(approval.ApprovalError, "human owner"):
            self.review()

    def test_bot_author_and_stale_commit_do_not_approve(self):
        changes = ({"user": {"login": "robot", "type": "Bot"}},
                   {"user": {"login": "author", "type": "User"}},
                   {"commit_id": BASE})
        for change in changes:
            with self.subTest(change=change):
                self.api = Facts()
                self.api.reviews[0].update(change)
                with self.assertRaisesRegex(approval.ApprovalError, "human owner"):
                    self.review()

    def test_dismissal_and_change_request_override_prior_approval(self):
        for state in ("DISMISSED", "CHANGES_REQUESTED"):
            with self.subTest(state=state):
                self.api = Facts()
                latest = dict(self.api.reviews[0], id=2, state=state)
                self.api.reviews.append(latest)
                with self.assertRaises(approval.ApprovalError):
                    self.review()

    def test_comment_does_not_invalidate_existing_approval(self):
        self.api.reviews.append(dict(self.api.reviews[0], id=2, state="COMMENTED"))
        self.review()

    def test_current_head_and_base_must_still_match(self):
        for side in ("head", "base"):
            with self.subTest(side=side):
                self.api = Facts()
                self.api.pr[side]["sha"] = "c" * 40
                with self.assertRaisesRegex(approval.ApprovalError, "head/base changed"):
                    self.review()

    def test_head_move_during_approval_lookup_fails(self):
        self.api.move_on_second_read = True
        with self.assertRaisesRegex(approval.ApprovalError, "head/base changed"):
            self.review()

    def test_failed_missing_or_expired_candidate_evidence_cannot_approve(self):
        variants = ({"jobs": [{"name": "candidate", "conclusion": "failure"}]},
                    {"jobs": []}, {"artifacts": []},
                    {"artifacts": [{"name": f"candidate-{HEAD}", "expired": True}]},
                    {"runs": [{"id": 17, "event": "pull_request", "head_sha": BASE}]})
        for change in variants:
            with self.subTest(change=change):
                self.api = Facts()
                for name, value in change.items():
                    setattr(self.api, name, value)
                with self.assertRaisesRegex(approval.ApprovalError, "successful candidate"):
                    self.review()

    def release(self, event=None, environment=None):
        event = event or {"inputs": {"source_sha": HEAD, "compare_sha": BASE, "reviewed_source": True},
                          "sender": {"login": "owner", "type": "User"}}
        environment = environment or {"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/main",
                                      "GITHUB_SHA": HEAD, "GITHUB_ACTOR": "owner", "GITHUB_RUN_ID": "123",
                                      "GITHUB_RUN_ATTEMPT": "1"}
        return approval.release_approval(self.api, REPO, HEAD, BASE, event, environment)

    def test_owner_can_explicitly_approve_exact_release_without_a_second_reviewer(self):
        result = self.release()
        self.assertEqual(result["head_sha"], HEAD)
        self.assertEqual(result["approval_evidence"], f"github:{REPO}/actions/runs/123")

    def test_unconfirmed_dispatch_is_not_release_approval(self):
        event = {"inputs": {"source_sha": HEAD, "compare_sha": BASE, "reviewed_source": False},
                 "sender": {"login": "owner", "type": "User"}}
        with self.assertRaisesRegex(approval.ApprovalError, "fresh dispatch"):
            self.release(event=event)

    def test_release_rerun_bot_or_changed_main_needs_fresh_human_confirmation(self):
        environment = {"GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/main",
                       "GITHUB_SHA": HEAD, "GITHUB_ACTOR": "owner", "GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "2"}
        with self.assertRaisesRegex(approval.ApprovalError, "fresh dispatch"):
            self.release(environment=environment)
        event = {"inputs": {"source_sha": HEAD, "compare_sha": BASE, "reviewed_source": True},
                 "sender": {"login": "owner", "type": "Bot"}}
        with self.assertRaisesRegex(approval.ApprovalError, "human owner"):
            self.release(event=event)
        self.api.main = BASE
        with self.assertRaisesRegex(approval.ApprovalError, "main changed"):
            self.release()

    def test_first_rollout_without_trusted_helper_holds_without_running_head_code(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/approval.yml").read_text()
        start = workflow.index("          if [ ! -f harness/ci_approval.py ]; then")
        end = workflow.index("          fi", start) + len("          fi")
        guard = "\n".join(line[10:] for line in workflow[start:end].splitlines())
        with tempfile.TemporaryDirectory() as directory:
            summary = Path(directory) / "summary"
            environment = dict(os.environ, HEAD_SHA=HEAD, COMPARE_SHA=BASE,
                               GITHUB_STEP_SUMMARY=str(summary))
            result = subprocess.run(["bash", "-eu", "-c", guard], cwd=directory,
                                    env=environment, capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn("HOLD", summary.read_text())
            self.assertIn(HEAD, summary.read_text())
            self.assertIn("Do not execute", summary.read_text())

    def test_candidate_approval_and_release_never_publish_skipped_equivalent_jobs(self):
        directory = Path(__file__).resolve().parents[1] / ".github/workflows"
        expected = {"ci.yml": ("pull_request:", "candidate"),
                    "approval.yml": ("pull_request_review:", "human approval"),
                    "release.yml": ("workflow_dispatch:", "approved release package")}
        for filename, (event, job) in expected.items():
            with self.subTest(filename=filename):
                text = (directory / filename).read_text()
                before_steps = text.split("    steps:", 1)[0]
                self.assertIn(event, before_steps)
                self.assertIn("    name: " + job, before_steps)
                self.assertNotIn("    if:", before_steps)
                for other_event, _ in expected.values():
                    if other_event != event:
                        self.assertNotIn("  " + other_event, before_steps)


if __name__ == "__main__":
    unittest.main()
