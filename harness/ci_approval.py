#!/usr/bin/env python3
"""Read GitHub facts before granting SHA-bound human approval to Harness."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess


class ApprovalError(RuntimeError):
    pass


SHA = re.compile(r"[0-9a-f]{40}")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")


class GitHub:
    def get(self, endpoint):
        result = subprocess.run(["gh", "api", endpoint], capture_output=True, text=True, check=False)
        if result.returncode:
            raise ApprovalError("GitHub approval facts are unavailable")
        try:
            return json.loads(result.stdout)
        except ValueError as error:
            raise ApprovalError("GitHub returned invalid approval facts") from error

    def pages(self, endpoint, field=None):
        items = []
        for page in range(1, 101):
            separator = "&" if "?" in endpoint else "?"
            payload = self.get(f"{endpoint}{separator}per_page=100&page={page}")
            batch = payload.get(field) if field and isinstance(payload, dict) else payload
            if not isinstance(batch, list):
                raise ApprovalError("GitHub returned an invalid result list")
            items.extend(batch)
            if len(batch) < 100:
                return items
        raise ApprovalError("GitHub result pagination exceeded the bounded approval lookup")


def validate_identity(repository, head, compare):
    if not REPOSITORY.fullmatch(repository) or not SHA.fullmatch(head) or not SHA.fullmatch(compare):
        raise ApprovalError("repository and full head/compare SHAs are required")


def human_owner(api, repository, user):
    if not isinstance(user, dict):
        return False
    login = user.get("login", "")
    if user.get("type") != "User" or not re.fullmatch(r"[A-Za-z0-9-]+", login) or login.endswith("[bot]"):
        return False
    permission = api.get(f"repos/{repository}/collaborators/{login}/permission")
    return permission.get("permission") == "admin" or permission.get("role_name") in {"admin", "maintain"}


def current_pr(api, repository, number, head, compare):
    validate_identity(repository, head, compare)
    if number < 1:
        raise ApprovalError("a positive pull-request number is required")
    pr = api.get(f"repos/{repository}/pulls/{number}")
    if (pr.get("state") != "open" or pr.get("draft") is True or
            pr.get("head", {}).get("sha") != head or pr.get("base", {}).get("sha") != compare):
        raise ApprovalError("pull-request head/base changed, or the pull request is not reviewable")
    return pr


def candidate_run(api, repository, head):
    runs = api.pages(f"repos/{repository}/actions/workflows/ci.yml/runs?event=pull_request&head_sha={head}", "workflow_runs")
    for run in sorted(runs, key=lambda value: (value.get("id", 0), value.get("run_attempt", 0)), reverse=True):
        if run.get("head_sha") != head or run.get("event") != "pull_request":
            continue
        jobs = api.pages(f"repos/{repository}/actions/runs/{run['id']}/jobs", "jobs")
        if not any(job.get("name") == "candidate" and job.get("conclusion") == "success" for job in jobs):
            continue
        artifacts = api.pages(f"repos/{repository}/actions/runs/{run['id']}/artifacts", "artifacts")
        if any(item.get("name") == f"candidate-{head}" and item.get("expired") is False for item in artifacts):
            return run["id"]
    raise ApprovalError("no successful candidate job with retained evidence exists for this head")


def review_approval(api, repository, number, head, compare):
    pr = current_pr(api, repository, number, head, compare)
    reviews = api.pages(f"repos/{repository}/pulls/{number}/reviews")
    latest = {}
    for review in sorted(reviews, key=lambda value: value.get("id", 0)):
        if review.get("state") in {"APPROVED", "CHANGES_REQUESTED", "DISMISSED"}:
            latest[review.get("user", {}).get("login", "")] = review
    approvals = []
    for login, review in latest.items():
        if login == pr.get("user", {}).get("login") or not human_owner(api, repository, review.get("user", {})):
            continue
        if review.get("state") == "CHANGES_REQUESTED":
            raise ApprovalError("an owner still requests changes")
        if review.get("state") == "APPROVED" and review.get("commit_id") == head:
            approvals.append({"login": login, "review_id": review["id"], "commit_id": head})
    if not approvals:
        raise ApprovalError("a human owner or maintainer must approve this exact head")
    run_id = candidate_run(api, repository, head)
    current_pr(api, repository, number, head, compare)
    return {"head_sha": head, "compare_sha": compare, "decision": "APPROVED",
            "candidate_run_id": run_id, "human_approvals": approvals,
            "approval_evidence": f"github:{repository}/pull/{number}@{head}"}


def release_approval(api, repository, head, compare, event, environment):
    validate_identity(repository, head, compare)
    inputs = event.get("inputs", {})
    if (environment.get("GITHUB_EVENT_NAME") != "workflow_dispatch" or
            environment.get("GITHUB_REF") != "refs/heads/main" or
            environment.get("GITHUB_SHA") != head or
            environment.get("GITHUB_RUN_ATTEMPT") != "1" or
            inputs.get("source_sha") != head or inputs.get("compare_sha") != compare or
            inputs.get("reviewed_source") not in (True, "true")):
        raise ApprovalError("release needs a fresh dispatch explicitly approving the exact source and baseline")
    sender = event.get("sender", {})
    if sender.get("login") != environment.get("GITHUB_ACTOR") or not human_owner(api, repository, sender):
        raise ApprovalError("release approval must come from a human owner or maintainer")
    if api.get(f"repos/{repository}/git/ref/heads/main").get("object", {}).get("sha") != head:
        raise ApprovalError("main changed after this release approval")
    run_id = environment.get("GITHUB_RUN_ID", "")
    if not run_id.isdigit():
        raise ApprovalError("release approval lacks a valid workflow run identity")
    return {"head_sha": head, "compare_sha": compare, "decision": "APPROVED",
            "human_approvals": [{"login": sender["login"], "workflow_run_id": run_id}],
            "approval_evidence": f"github:{repository}/actions/runs/{run_id}"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("review", "current", "release"))
    parser.add_argument("--repository", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--compare", required=True)
    parser.add_argument("--number", type=int, default=0)
    args = parser.parse_args()
    api = GitHub()
    try:
        if args.action == "review":
            result = review_approval(api, args.repository, args.number, args.head, args.compare)
        elif args.action == "current":
            current_pr(api, args.repository, args.number, args.head, args.compare)
            result = {"head_sha": args.head, "compare_sha": args.compare, "current": True}
        else:
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
            result = release_approval(api, args.repository, args.head, args.compare, event, os.environ)
    except (ApprovalError, OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(1, f"approval: {error}\n")
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
