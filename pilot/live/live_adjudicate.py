#!/usr/bin/env python3
"""Forward-adjudicate archived live findings against later Git changes."""

from __future__ import annotations

import argparse
import collections
import contextlib
import datetime as dt
import fcntl
import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
from typing import Callable, Iterable

from native_records import adapted_findings, digest, read_json, validate_result


LABELS = ("open", "confirmed_by_later_fix", "superseded", "stale_probable_noise")
FINAL_STATUSES = frozenset({"confirmed_by_later_fix", "superseded", "stale_probable_noise"})
JUDGMENTS = ("fixes", "touches_only", "unclear")
DIMENSIONS = ("D1", "D2", "D3", "D4", "unknown")
HUNK_HEADER = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


def run_git(repository: pathlib.Path, *arguments: str, check: bool = True) -> str:
    completed = subprocess.run(
        ["git", "-C", str(repository), *arguments],
        text=True,
        capture_output=True,
        check=check,
    )
    return completed.stdout.strip()


def hunk_touches_line(diff: str, line: int, radius: int = 10) -> bool:
    for raw_line in diff.splitlines():
        match = HUNK_HEADER.match(raw_line)
        if not match:
            continue
        old_start, old_count, new_start, new_count = (
            int(match.group(1)),
            int(match.group(2) or "1"),
            int(match.group(3)),
            int(match.group(4) or "1"),
        )
        for start, count in ((old_start, old_count), (new_start, new_count)):
            end = start + max(count, 1) - 1
            if start - radius <= line <= end + radius:
                return True
    return False


def transition_status(current: str, decisions: Iterable[str], age_days: int, has_candidates: bool) -> str:
    if current in FINAL_STATUSES:
        return current
    values = list(decisions)
    if "fixes" in values:
        return "confirmed_by_later_fix"
    if "touches_only" in values:
        return "superseded"
    if age_days >= 30 and not has_candidates:
        return "stale_probable_noise"
    return "open"


def ratio(numerator: int, denominator: int) -> float | None:
    return numerator / denominator if denominator else None


def count_labels(findings: list[dict[str, object]]) -> dict[str, dict[str, object]]:
    counts = collections.Counter(str(finding["status"]) for finding in findings)
    return {label: {"count": counts[label], "ratio": ratio(counts[label], len(findings))} for label in LABELS}


def build_summary(
    reviews: list[dict[str, object]],
    findings: list[dict[str, object]],
    generated_at: dt.datetime | None = None,
) -> dict[str, object]:
    generated_at = generated_at or dt.datetime.now(dt.timezone.utc)
    zero_count = sum(1 for review in reviews if int(review.get("finding_count", 0)) == 0)
    labels = count_labels(findings)
    adopted = labels["confirmed_by_later_fix"]["count"]
    noise = labels["stale_probable_noise"]["count"]
    dimensions: dict[str, object] = {}
    for dimension in DIMENSIONS:
        selected = [finding for finding in findings if finding.get("dimension") == dimension]
        dimensions[dimension] = {"total": len(selected), "labels": count_labels(selected)}
    repositories: dict[str, object] = {}
    names = sorted({str(review["repo"]) for review in reviews} | {str(finding["repo"]) for finding in findings})
    for name in names:
        repo_reviews = [review for review in reviews if review["repo"] == name]
        repo_findings = [finding for finding in findings if finding["repo"] == name]
        repositories[name] = {
            "reports": len(repo_reviews),
            "zero_findings": sum(1 for review in repo_reviews if int(review.get("finding_count", 0)) == 0),
            "findings": len(repo_findings),
            "labels": count_labels(repo_findings),
        }
    return {
        "schema_version": 1,
        "profile": "live_git_forward_adjudication",
        "generated_at": generated_at.astimezone(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        "reports": {
            "total": len(reviews),
            "zero_findings": {"count": zero_count, "ratio": ratio(zero_count, len(reviews))},
        },
        "findings": {
            "total": len(findings),
            "independent_confirmations": sum(1 for finding in findings if finding.get("independent_confirmation") is True),
            "labels": labels,
            "mapped_judgments": {
                "adopted": {"count": adopted, "ratio": ratio(int(adopted), len(findings))},
                "noise": {"count": noise, "ratio": ratio(int(noise), len(findings))},
            },
            "dimensions": dimensions,
        },
        "repositories": repositories,
    }


def write_atomically(destination: pathlib.Path, contents: str) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{destination.name}-", dir=destination.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(contents)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, destination)
        directory = os.open(destination.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@contextlib.contextmanager
def adjudication_lock(data_root: pathlib.Path):
    """Serialize cron/manual runs; the kernel releases this lock after a crash."""
    directory = data_root / "run"
    directory.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(directory / "live-adjudicate.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("live adjudication is already running") from error
        yield
    finally:
        os.close(descriptor)


def persist_state(path: pathlib.Path, state: dict) -> None:
    write_atomically(path, json.dumps(state, indent=2, sort_keys=True) + "\n")


def load_config(source: pathlib.Path) -> dict[str, object]:
    if source.is_symlink() or not source.is_file():
        raise ValueError("config must be a regular non-symlink file")
    document = json.loads(source.read_text(encoding="utf-8"))
    if document.get("schema_version") != 1 or not isinstance(document.get("repositories"), list):
        raise ValueError("invalid live config")
    return document


def load_reviews(data_root: pathlib.Path) -> list[dict[str, object]]:
    index_path = data_root / "index.jsonl"
    if not index_path.exists():
        return []
    records: dict[tuple[str, str], dict[str, object]] = {}
    for line_number, line in enumerate(index_path.read_text(encoding="utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        record = json.loads(line)
        if record.get("status") != "COMPLETE":
            continue
        repo = record.get("repo")
        sha = record.get("sha")
        if not isinstance(repo, str) or not isinstance(sha, str):
            raise ValueError(f"invalid index record at line {line_number}")
        records[(repo, sha)] = record
    return [records[key] for key in sorted(records)]


def initialize_state(repo: str, sha: str, result: dict[str, object]) -> dict[str, object]:
    findings = adapted_findings(result)
    return {"schema_version": 2, "repo": repo, "sha": sha, "review_key": result["review_key"], "findings": findings}


def archived_result(review: dict, result_path: pathlib.Path) -> dict:
    if review.get("schema_version") != 2 or review.get("origin") != "production_cli":
        raise ValueError("live index requires explicit schema migration")
    result = read_json(result_path)
    validate_result(result)
    if digest(result_path) != review.get("result_sha256"):
        raise ValueError("archived result digest differs from validated index")
    if result["current_head"] != review["sha"] or result["review_key"] != review.get("review_key"):
        raise ValueError("archived result identity differs from validated index")
    if result["adjudication"]["semantic_result"] == "ERROR":
        raise ValueError("ERROR is not a completed review")
    return result


def candidate_commits(
    repository: pathlib.Path,
    review_sha: str,
    locations: list[dict[str, object]],
    head: str,
) -> list[str]:
    ancestry = subprocess.run(
        ["git", "-C", str(repository), "merge-base", "--is-ancestor", review_sha, head],
        text=True,
        capture_output=True,
        check=False,
    )
    if ancestry.returncode != 0:
        return []
    commits = run_git(repository, "rev-list", "--reverse", "--topo-order", f"{review_sha}..{head}").splitlines()
    candidates = []
    for commit in commits:
        parents = run_git(repository, "rev-list", "--parents", "-n", "1", commit).split()
        if len(parents) != 2:
            continue
        for location in locations:
            relative = str(location.get("path", ""))
            line = location.get("line")
            if not relative or pathlib.PurePath(relative).is_absolute() or ".." in pathlib.PurePath(relative).parts:
                continue
            if not isinstance(line, int) or isinstance(line, bool) or line < 1:
                continue
            diff = run_git(repository, "diff", "--unified=0", f"{commit}^", commit, "--", relative)
            if hunk_touches_line(diff, line, radius=10):
                candidates.append(commit)
                break
    return candidates


def hardened_clone(source: pathlib.Path, target: str, destination: pathlib.Path) -> None:
    subprocess.run(["git", "clone", "--shared", "--no-checkout", str(source), str(destination)], check=True, capture_output=True)
    run_git(destination, "checkout", "--detach", target)
    run_git(destination, "remote", "remove", "origin")
    refs = run_git(destination, "for-each-ref", "--format=%(refname)").splitlines()
    for ref in refs:
        run_git(destination, "update-ref", "-d", ref)
    run_git(destination, "reflog", "expire", "--expire=now", "--all")
    run_git(destination, "repack", "-a", "-d")
    alternates = destination / ".git" / "objects" / "info" / "alternates"
    alternates.unlink(missing_ok=True)
    if run_git(destination, "for-each-ref", "--format=%(refname)") or run_git(destination, "remote") or alternates.exists():
        raise RuntimeError("isolated clone retained refs, remote, or alternates")
    run_git(destination, "cat-file", "-e", f"{target}^{{commit}}")


def candidate_diff(repository: pathlib.Path, commit: str, locations: list[dict[str, object]]) -> str:
    files = sorted({str(location.get("path")) for location in locations if location.get("path")})
    arguments = ["diff", "--unified=20", f"{commit}^", commit, "--", *files]
    raw = run_git(repository, *arguments)
    return raw[:1_000_000]


def adjudicate_pair(
    repository: pathlib.Path,
    candidate: str,
    finding: dict[str, object],
    codex_bin: pathlib.Path,
) -> dict[str, str]:
    injected = os.environ.get("LIVE_ADJUDICATION_RUNNER")
    if injected:
        completed = subprocess.run(
            [injected, str(repository), candidate, json.dumps(finding, sort_keys=True)],
            check=True,
            text=True,
            capture_output=True,
        )
        decision = json.loads(completed.stdout)
    else:
        if not codex_bin.is_file() or not os.access(codex_bin, os.X_OK):
            raise ValueError("Codex binary is not executable")
        with tempfile.TemporaryDirectory(prefix="code-quality-adjudicate.") as temporary:
            root = pathlib.Path(temporary)
            clone = root / "repository"
            hardened_clone(repository, candidate, clone)
            schema = root / "decision.schema.json"
            schema.write_text(
                json.dumps(
                    {
                        "type": "object",
                        "additionalProperties": False,
                        "required": ["judgment", "reason"],
                        "properties": {
                            "judgment": {"enum": list(JUDGMENTS)},
                            "reason": {"type": "string", "minLength": 1},
                        },
                    }
                ),
                encoding="utf-8",
            )
            output = root / "decision.json"
            prompt = (
                "你在做后续 Git 修复裁决，不评价原审查风格。判断候选提交是否修复了 finding 描述的同一问题。"
                "只可返回 fixes、touches_only、unclear。\nFinding:\n"
                + json.dumps(finding, ensure_ascii=False, sort_keys=True)
                + "\nCandidate diff:\n"
                + candidate_diff(repository, candidate, list(finding.get("code_locations", [])))
            )
            environment = os.environ.copy()
            completed = subprocess.run(
                [
                    str(codex_bin),
                    "exec",
                    "-s",
                    "read-only",
                    "-C",
                    str(clone),
                    "--ignore-user-config",
                    "--ignore-rules",
                    "--ephemeral",
                    "--output-schema",
                    str(schema),
                    "--output-last-message",
                    str(output),
                    prompt,
                ],
                text=True,
                capture_output=True,
                env=environment,
                timeout=int(os.environ.get("LIVE_CODEX_TIMEOUT_SECONDS", "1800")),
                check=True,
            )
            decision = json.loads(output.read_text(encoding="utf-8"))
    if decision.get("judgment") not in JUDGMENTS or not isinstance(decision.get("reason"), str) or not decision["reason"].strip():
        raise ValueError("invalid adjudication decision")
    return {"judgment": str(decision["judgment"]), "reason": str(decision["reason"])}


def parse_time(value: str) -> dt.datetime:
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(dt.timezone.utc)


def render_summary(summary: dict[str, object]) -> str:
    reports = summary["reports"]
    findings = summary["findings"]
    lines = [
        "# Live Git Forward Adjudication",
        "",
        f"Generated: `{summary['generated_at']}`",
        f"Status: `{summary.get('status', 'COMPLETE')}`",
        "",
        "## Reports",
        "",
        f"- Total: {reports['total']}",
        f"- Zero findings: {reports['zero_findings']['count']} ({format_ratio(reports['zero_findings']['ratio'])})",
        "",
        "## Finding Labels",
        "",
        f"- Independent confirmations: {findings['independent_confirmations']}",
        "",
        "| Label | Count | Ratio |",
        "|---|---:|---:|",
    ]
    for label in LABELS:
        value = findings["labels"][label]
        lines.append(f"| `{label}` | {value['count']} | {format_ratio(value['ratio'])} |")
    lines.extend(["", "`confirmed_by_later_fix` maps to adopted; `stale_probable_noise` maps to noise.", "", "## Dimensions", ""])
    lines.extend(["| Dimension | Findings |", "|---|---:|"])
    for dimension in DIMENSIONS:
        lines.append(f"| {dimension} | {findings['dimensions'][dimension]['total']} |")
    lines.extend(["", "## Repositories", "", "| Repository | Reports | Zero | Findings |", "|---|---:|---:|---:|"])
    for name, value in summary["repositories"].items():
        lines.append(f"| `{name}` | {value['reports']} | {value['zero_findings']} | {value['findings']} |")
    return "\n".join(lines) + "\n"


def format_ratio(value: float | None) -> str:
    return "n/a" if value is None else f"{value:.1%}"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=pathlib.Path, default=pathlib.Path.home() / "AiProject" / "code-quality-live" / "config.json")
    parser.add_argument("--data-root", type=pathlib.Path, default=pathlib.Path.home() / "AiProject" / "code-quality-live")
    parser.add_argument("--now", help="UTC ISO timestamp for deterministic tests")
    args = parser.parse_args()
    try:
        with adjudication_lock(args.data_root):
            return run_adjudications(args)
    except (OSError, RuntimeError) as error:
        print(f"HOLD: {error}", file=os.sys.stderr)
        return 1


def run_adjudications(args: argparse.Namespace) -> int:
    now = parse_time(args.now) if args.now else dt.datetime.now(dt.timezone.utc)
    config = load_config(args.config.resolve())
    repositories = {entry["name"]: pathlib.Path(entry["path"]).resolve() for entry in config["repositories"]}
    refs = {entry["name"]: entry.get("ref", "HEAD") for entry in config["repositories"]}
    codex_bin = pathlib.Path(str(config.get("codex_bin", shutil.which("codex") or "")))
    reviews = load_reviews(args.data_root)
    all_findings: list[dict[str, object]] = []
    accepted_reviews = []
    failures = []

    for review in reviews:
        repo_name = str(review["repo"])
        sha = str(review["sha"])
        if repo_name not in repositories:
            failures.append(f"{repo_name}/{sha}: repository is absent from config")
            continue
        result_path = args.data_root / "reviews" / repo_name / sha / "review-result.json"
        try:
            result = archived_result(review, result_path)
            state_path = args.data_root / "adjudications" / repo_name / f"{sha}.json"
            state = json.loads(state_path.read_text(encoding="utf-8")) if state_path.exists() else initialize_state(repo_name, sha, result)
            if state.get("schema_version") != 2 or state.get("review_key") != result["review_key"]:
                raise ValueError("adjudication state requires explicit schema migration")
        except (OSError, ValueError, KeyError) as error:
            failures.append(f"{repo_name}/{sha}: {error}")
            continue
        accepted_reviews.append(review)
        repository = repositories[repo_name]
        head = run_git(repository, "rev-parse", f"{refs[repo_name]}^{{commit}}")
        reviewed_at = parse_time(str(review["reviewed_at"]))
        age_days = max(0, (now - reviewed_at).days)
        for finding in state["findings"]:
            if finding.get("pending_adjudication") is not None:
                failures.append(f"{repo_name}/{sha}/{finding['finding_id']}: HOLD: prior pair outcome is unknown; inspect {state_path} before authorizing another call")
                continue
            if finding["status"] in FINAL_STATUSES:
                continue
            try:
                candidates = candidate_commits(repository, sha, list(finding["code_locations"]), head)
                decided = {decision["candidate_sha"] for decision in finding["decisions"]}
                for candidate in candidates:
                    if candidate in decided:
                        continue
                    finding["pending_adjudication"] = {
                        "candidate_sha": candidate,
                        "started_at": now.isoformat().replace("+00:00", "Z"),
                    }
                    # Persist intent before the potentially paid call. Any interruption
                    # after this point is operator-held rather than silently retried.
                    persist_state(state_path, state)
                    decision = adjudicate_pair(repository, candidate, finding, codex_bin)
                    finding["decisions"].append(
                        {
                            "candidate_sha": candidate,
                            "judgment": decision["judgment"],
                            "reason": decision["reason"],
                            "adjudicated_at": now.isoformat().replace("+00:00", "Z"),
                        }
                    )
                    del finding["pending_adjudication"]
                    finding["status"] = transition_status(
                        str(finding["status"]),
                        [str(value["judgment"]) for value in finding["decisions"]],
                        age_days, True,
                    )
                    # Each completed paid pair becomes durable independently of the
                    # remaining findings and summary publication.
                    persist_state(state_path, state)
                    if decision["judgment"] in ("fixes", "touches_only"):
                        break
                finding["status"] = transition_status(
                    str(finding["status"]),
                    [str(decision["judgment"]) for decision in finding["decisions"]],
                    age_days,
                    bool(candidates),
                )
            except Exception as error:  # an existing intent holds uncertain paid calls
                failures.append(f"{repo_name}/{sha}/{finding['finding_id']}: {error}")
        persist_state(state_path, state)
        for finding in state["findings"]:
            all_findings.append({"repo": repo_name, "sha": sha, **finding})

    summary = build_summary(accepted_reviews, all_findings, generated_at=now)
    summary["excluded_reviews"] = len(reviews) - len(accepted_reviews)
    summary["status"] = "HOLD" if failures else "COMPLETE"
    markdown = render_summary(summary)
    write_atomically(args.data_root / "summary.md", markdown)
    iso_year, iso_week, _ = now.isocalendar()
    write_atomically(args.data_root / "snapshots" / f"{iso_year}-W{iso_week:02d}.md", markdown)
    write_atomically(args.data_root / "summary.json", json.dumps(summary, indent=2, sort_keys=True) + "\n")
    for failure in failures:
        print(f"warning: {failure}", file=os.sys.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
