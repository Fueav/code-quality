#!/usr/bin/env python3
"""Inspect the frozen native-review goal-mode feasibility experiment."""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import os
import pathlib
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any

from native_review_admission import audit_inventory, file_sha256, tree_sha256


PROFILE_PATTERN = re.compile(r"^native_review_goal_feasibility_v[1-9][0-9]*$")
RETIRED_PROFILES = {
    "native_review_goal_feasibility_v1",
    "native_review_goal_feasibility_v2",
}
MODEL = "gpt-5.6-sol"
REASONING_EFFORT = "high"
LANES = ("baseline_native", "goal_native")
NATIVE_HEADER = re.compile(r"^- \[P([0-3])\] (.+?) — (.+):(\d+)(?:-(\d+))?$")
NATIVE_HEADINGS = {
    "Review comment:",
    "Review comments:",
    "Full review comment:",
    "Full review comments:",
}
FORBIDDEN_GOAL_TERMS = ("positive", "counterexample", "expected", "finding", "defect", "bug", "pass", "fail")


def read_json(path: pathlib.Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def write_json(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def utc_now() -> str:
    return dt.datetime.now(dt.UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def run_checked(
    args: list[str],
    *,
    cwd: pathlib.Path | None = None,
    env: dict[str, str] | None = None,
) -> str:
    return subprocess.check_output(args, cwd=cwd, env=env, text=True, stderr=subprocess.STDOUT)


def resolve_project_path(root: pathlib.Path, raw: object, field: str) -> pathlib.Path:
    if not isinstance(raw, str) or not raw.strip():
        raise ValueError(f"{field} must be a nonempty path")
    path = (root / raw).resolve()
    try:
        path.relative_to(root.resolve())
    except ValueError as error:
        raise ValueError(f"{field} escapes the repository") from error
    return path


def audit_protocol(root: pathlib.Path, manifest_path: pathlib.Path) -> dict[str, Any]:
    root = root.resolve(strict=True)
    manifest_path = manifest_path.resolve(strict=True)
    manifest = read_json(manifest_path)
    errors: list[str] = []

    profile = manifest.get("profile")
    if manifest.get("schema_version") != 1 or not isinstance(profile, str) or not PROFILE_PATTERN.fullmatch(profile):
        errors.append("protocol header is invalid")
    elif profile in RETIRED_PROFILES:
        errors.append("protocol is retired; retain it as historical evidence only")

    specification = manifest.get("specification")
    admission_ref = manifest.get("admission")
    runtime = manifest.get("runtime")
    lanes = manifest.get("lanes")
    cases = manifest.get("cases")
    sessions = manifest.get("session_order")
    if not isinstance(specification, dict):
        errors.append("specification is invalid")
        specification = {}
    if not isinstance(admission_ref, dict):
        errors.append("admission is invalid")
        admission_ref = {}
    if not isinstance(runtime, dict):
        errors.append("runtime is invalid")
        runtime = {}
    if not isinstance(lanes, dict):
        errors.append("lanes are invalid")
        lanes = {}
    if not isinstance(cases, list):
        errors.append("cases must be an array")
        cases = []
    if not isinstance(sessions, list):
        errors.append("session_order must be an array")
        sessions = []

    try:
        spec_path = resolve_project_path(root, specification.get("path"), "specification.path")
        if file_sha256(spec_path) != specification.get("sha256"):
            errors.append("specification hash mismatch")
    except (FileNotFoundError, ValueError) as error:
        errors.append(str(error))
        spec_path = root

    try:
        admission_path = resolve_project_path(root, admission_ref.get("path"), "admission.path")
        if file_sha256(admission_path) != admission_ref.get("sha256"):
            errors.append("admission manifest hash mismatch")
        admission_report = audit_inventory(root / "evals" / "cases.json", root / "pilot" / "fixtures", admission_path)
        if not admission_report["admission_ready"]:
            errors.append("admission inventory is not ready")
        if admission_report["cases_sha256"] != admission_ref.get("cases_sha256"):
            errors.append("admitted cases hash mismatch")
        if admission_report["fixtures_sha256"] != admission_ref.get("fixtures_sha256"):
            errors.append("admitted fixture tree hash mismatch")
        admission = read_json(admission_path)
    except (FileNotFoundError, ValueError, KeyError) as error:
        errors.append(f"admission validation failed: {error}")
        admission_report = {}
        admission = {"qualified_case_ids": []}

    source_commit = manifest.get("product_source_commit")
    if not isinstance(source_commit, str) or len(source_commit) != 40:
        errors.append("product_source_commit is invalid")
    else:
        source_check = subprocess.run(
            ["git", "-C", str(root), "cat-file", "-e", source_commit + "^{commit}"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if source_check.returncode != 0:
            errors.append("product source commit is unavailable")

    cases_payload = read_json(root / "evals" / "cases.json")
    case_by_id = {case["id"]: case for case in cases_payload.get("cases", []) if isinstance(case, dict) and "id" in case}
    qualified = set(admission.get("qualified_case_ids", []))
    opaque_ids: set[str] = set()
    selected_ids: set[str] = set()
    kind_totals: dict[str, int] = {}
    dimension_totals: dict[str, int] = {}
    source_paths = ["evals/cases.json", "evals/native-review-admission.json"]

    for index, selected in enumerate(cases):
        if not isinstance(selected, dict):
            errors.append(f"cases[{index}] is invalid")
            continue
        opaque_id = selected.get("opaque_id")
        case_id = selected.get("case_id")
        if not isinstance(opaque_id, str) or not opaque_id:
            errors.append(f"cases[{index}].opaque_id is invalid")
            continue
        if opaque_id in opaque_ids:
            errors.append(f"duplicate opaque id {opaque_id}")
        opaque_ids.add(opaque_id)
        if not isinstance(case_id, str) or case_id not in case_by_id:
            errors.append(f"{opaque_id}: unknown case id")
            continue
        if case_id in selected_ids:
            errors.append(f"duplicate selected case {case_id}")
        selected_ids.add(case_id)
        canonical = case_by_id[case_id]
        if case_id not in qualified:
            errors.append(f"{opaque_id}: case is not automatically admitted")
        for field in ("kind", "dimension"):
            if selected.get(field) != canonical.get(field):
                errors.append(f"{opaque_id}: {field} disagrees with cases.json")
        kind = str(selected.get("kind"))
        dimension = str(selected.get("dimension"))
        kind_totals[kind] = kind_totals.get(kind, 0) + 1
        dimension_totals[dimension] = dimension_totals.get(dimension, 0) + 1
        fixture_root = root / "pilot" / "fixtures" / case_id.lower()
        if tree_sha256(fixture_root) != selected.get("fixture_sha256"):
            errors.append(f"{opaque_id}: fixture hash mismatch")
        source_paths.append(f"pilot/fixtures/{case_id.lower()}")
        goal = selected.get("goal")
        if not isinstance(goal, str) or not goal.strip() or len(goal) > 4000:
            errors.append(f"{opaque_id}: goal is invalid")
        else:
            lowered = goal.lower()
            if case_id.lower() in lowered or str(canonical.get("rule_id", "")).lower() in lowered:
                errors.append(f"{opaque_id}: goal leaks case identity")
            for term in FORBIDDEN_GOAL_TERMS:
                if re.search(rf"\b{re.escape(term)}\b", lowered):
                    errors.append(f"{opaque_id}: goal contains forbidden label term {term}")
        if not isinstance(selected.get("expected_root"), str) or not selected["expected_root"].strip():
            errors.append(f"{opaque_id}: expected_root is required")

    if kind_totals != {"positive": 4, "counterexample": 4}:
        errors.append("sample must contain four positives and four counterexamples")
    if dimension_totals != {"D1": 2, "D2": 2, "D3": 2, "D4": 2}:
        errors.append("sample must contain one matched pair per dimension")

    if isinstance(source_commit, str) and len(source_commit) == 40:
        source_diff = subprocess.run(
            ["git", "-C", str(root), "diff", "--quiet", source_commit, "--", *source_paths],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if source_diff.returncode != 0:
            errors.append("frozen product source differs from selected evaluation inputs")

    if runtime.get("model") != MODEL or runtime.get("reasoning_effort") != REASONING_EFFORT:
        errors.append("runtime model or reasoning effort is not frozen to sol/high")
    if runtime.get("codex_cli_version") != "codex-cli 0.145.0":
        errors.append("Codex CLI version is not frozen")
    if runtime.get("sandbox") != "read-only" or not all(
        runtime.get(field) is True for field in ("ignore_user_config", "ignore_rules", "ephemeral")
    ):
        errors.append("runtime isolation flags are invalid")
    if runtime.get("max_parallel_sessions") != 2 or runtime.get("session_timeout_seconds") != 1200:
        errors.append("runtime concurrency or timeout is invalid")

    if set(lanes) != set(LANES):
        errors.append("lane set is invalid")
    lane_limits = {
        lane: config.get("maximum_calls_per_case")
        for lane, config in lanes.items()
        if isinstance(config, dict)
    }
    if lane_limits != {"baseline_native": 1, "goal_native": 2}:
        errors.append("lane call limits are invalid")

    run_ids: set[str] = set()
    scheduled: set[tuple[str, str]] = set()
    lane_totals: dict[str, int] = {}
    for index, session in enumerate(sessions, start=1):
        if not isinstance(session, dict):
            errors.append(f"session_order[{index - 1}] is invalid")
            continue
        run_id = session.get("run_id")
        opaque_id = session.get("opaque_id")
        lane = session.get("lane")
        if run_id != f"R{index:02d}" or run_id in run_ids:
            errors.append(f"session {index}: run id is invalid")
        if isinstance(run_id, str):
            run_ids.add(run_id)
        if opaque_id not in opaque_ids or lane not in LANES:
            errors.append(f"session {index}: case or lane is invalid")
            continue
        pair = (str(opaque_id), str(lane))
        if pair in scheduled:
            errors.append(f"session {index}: duplicate case/lane")
        scheduled.add(pair)
        lane_totals[str(lane)] = lane_totals.get(str(lane), 0) + 1
    expected_schedule = {(opaque_id, lane) for opaque_id in opaque_ids for lane in LANES}
    if scheduled != expected_schedule:
        errors.append("session order does not cover the case/lane matrix exactly once")
    if lane_totals != {"baseline_native": 8, "goal_native": 8}:
        errors.append("lane totals are invalid")
    computed_maximum_calls = sum(lane_limits.get(lane, 0) for _, lane in scheduled)
    if computed_maximum_calls != 24 or manifest.get("maximum_model_calls") != computed_maximum_calls:
        errors.append("experiment call ceiling is invalid")

    return {
        "schema_version": 1,
        "profile": profile,
        "ready": not errors,
        "errors": errors,
        "case_count": len(cases),
        "kind_totals": dict(sorted(kind_totals.items())),
        "dimension_totals": dict(sorted(dimension_totals.items())),
        "session_count": len(sessions),
        "lane_totals": dict(sorted(lane_totals.items())),
        "maximum_model_calls": computed_maximum_calls,
        "model": runtime.get("model"),
        "reasoning_effort": runtime.get("reasoning_effort"),
        "admission_ready": admission_report.get("admission_ready", False),
        "manifest_sha256": file_sha256(manifest_path),
        "specification_sha256": file_sha256(spec_path) if spec_path.is_file() else "",
    }


def copy_fixture_tree(source: pathlib.Path, destination: pathlib.Path) -> None:
    for path in sorted(source.rglob("*")):
        relative = path.relative_to(source)
        target = destination / relative
        if path.is_symlink():
            raise ValueError(f"fixture contains symlink: {relative}")
        if path.is_dir():
            target.mkdir(parents=True, exist_ok=True)
        elif path.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
        else:
            raise ValueError(f"fixture contains unsupported path: {relative}")


def clear_materialized_worktree(repository: pathlib.Path) -> None:
    for child in repository.iterdir():
        if child.name == ".git":
            continue
        if child.is_symlink() or child.is_file():
            child.unlink()
        elif child.is_dir():
            shutil.rmtree(child)
        else:
            raise ValueError(f"unsupported materialized path: {child}")


def materialize_fixture(fixture_root: pathlib.Path, repository: pathlib.Path) -> dict[str, Any]:
    fixture_root = fixture_root.resolve(strict=True)
    if repository.exists():
        raise ValueError(f"materialized repository already exists: {repository}")
    if not (fixture_root / "base").is_dir() or not (fixture_root / "target").is_dir():
        raise ValueError("fixture requires base and target directories")
    repository.mkdir(parents=True)
    run_checked(["git", "init", "--quiet", "--initial-branch=main", str(repository)])
    run_checked(["git", "-C", str(repository), "config", "user.name", "Frozen Review Fixture"])
    run_checked(["git", "-C", str(repository), "config", "user.email", "fixture@example.invalid"])
    commit_env = os.environ.copy()
    commit_env.update(
        {
            "GIT_AUTHOR_NAME": "Frozen Review Fixture",
            "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
            "GIT_COMMITTER_NAME": "Frozen Review Fixture",
            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
            "GIT_AUTHOR_DATE": "2000-01-01T00:00:00Z",
            "GIT_COMMITTER_DATE": "2000-01-01T00:00:00Z",
        }
    )
    copy_fixture_tree(fixture_root / "base", repository)
    run_checked(["git", "-C", str(repository), "add", "-A"])
    run_checked(["git", "-C", str(repository), "commit", "--quiet", "--no-gpg-sign", "-m", "base snapshot"], env=commit_env)
    base = run_checked(["git", "-C", str(repository), "rev-parse", "HEAD"]).strip()
    clear_materialized_worktree(repository)
    copy_fixture_tree(fixture_root / "target", repository)
    run_checked(["git", "-C", str(repository), "add", "-A"])
    run_checked(["git", "-C", str(repository), "commit", "--quiet", "--no-gpg-sign", "-m", "candidate change"], env=commit_env)
    target = run_checked(["git", "-C", str(repository), "rev-parse", "HEAD"]).strip()
    diff = subprocess.check_output(
        ["git", "-C", str(repository), "diff", "--no-ext-diff", "--unified=6", base, target, "--"]
    )
    changed_files = run_checked(
        ["git", "-C", str(repository), "diff", "--name-only", base, target, "--"]
    ).splitlines()
    return {
        "base_commit": base,
        "target_commit": target,
        "diff_sha256": sha256_bytes(diff),
        "changed_files": changed_files,
    }


def build_baseline_command(codex_binary: pathlib.Path, output_path: pathlib.Path) -> list[str]:
    return [
        str(codex_binary),
        "exec",
        "--sandbox",
        "read-only",
        "--ignore-user-config",
        "--ignore-rules",
        "--ephemeral",
        "review",
        "--model",
        MODEL,
        "--config",
        f'model_reasoning_effort="{REASONING_EFFORT}"',
        "--commit",
        "HEAD",
        "--json",
        "--output-last-message",
        str(output_path),
    ]


def build_goal_command(
    quality_review_binary: pathlib.Path,
    repository: pathlib.Path,
    base_commit: str,
    target_commit: str,
    goal: str,
    output_root: pathlib.Path,
) -> list[str]:
    return [
        str(quality_review_binary),
        "run-codex",
        "--repo",
        str(repository),
        "--base",
        base_commit,
        "--target",
        target_commit,
        "--diff-reason",
        "frozen_goal_feasibility_v2",
        "--goal",
        goal,
        "--model",
        MODEL,
        "--reasoning-effort",
        REASONING_EFFORT,
        "--output-root",
        str(output_root),
    ]


def normalize_native_review(raw: str, repository_root: pathlib.Path) -> list[dict[str, Any]]:
    lines = raw.replace("\r\n", "\n").splitlines()
    heading = next((index for index, line in enumerate(lines) if line.strip() in NATIVE_HEADINGS), -1)
    if heading < 0:
        if any(NATIVE_HEADER.match(line.strip()) for line in lines):
            raise ValueError("native finding appears without a Review comment section")
        return []

    root = repository_root.resolve(strict=True)
    findings: list[dict[str, Any]] = []
    current: dict[str, Any] | None = None
    body: list[str] = []
    trailing = False

    def flush() -> None:
        nonlocal current, body
        if current is None:
            return
        text = "\n".join(body).strip()
        if not text:
            raise ValueError("native finding has no body")
        current["body"] = text
        findings.append(current)
        current = None
        body = []

    for line in lines[heading + 1 :]:
        stripped = line.strip()
        match = NATIVE_HEADER.match(stripped)
        if match:
            if trailing:
                raise ValueError("native finding appears after trailing assessment")
            flush()
            raw_path = pathlib.Path(match.group(3))
            if not raw_path.is_absolute():
                raise ValueError("native finding path is not absolute")
            resolved = raw_path.resolve()
            try:
                relative = resolved.relative_to(root).as_posix()
            except ValueError as error:
                raise ValueError("native finding path escapes repository") from error
            start = int(match.group(4))
            end = int(match.group(5) or match.group(4))
            if start < 1 or end < start:
                raise ValueError("native finding line range is invalid")
            current = {
                "title": match.group(2).strip(),
                "body": "",
                "priority": int(match.group(1)),
                "path": relative,
                "start_line": start,
                "end_line": end,
            }
            continue
        if not stripped:
            continue
        if current is None:
            raise ValueError(f"unexpected native review section text: {stripped}")
        if line.startswith((" ", "\t")):
            if not trailing:
                body.append(stripped)
            continue
        if not body:
            raise ValueError("native finding has no body")
        trailing = True
    flush()
    if not findings:
        raise ValueError("Review comment section contains no findings")
    return findings


def normalize_product_result(result: dict[str, Any]) -> list[dict[str, Any]]:
    normalized = []
    for finding in result.get("findings", []):
        location = finding["code_location"]
        normalized.append(
            {
                "title": finding["title"],
                "body": finding["body"],
                "priority": finding["priority"],
                "path": location["path"],
                "start_line": location["start_line"],
                "end_line": location["end_line"],
            }
        )
    return normalized


def build_product_binary(root: pathlib.Path, manifest: dict[str, Any], working_root: pathlib.Path) -> pathlib.Path:
    source = working_root / "product-source"
    binary = working_root / "quality-review"
    run_checked(["git", "clone", "--quiet", "--shared", "--no-checkout", str(root), str(source)])
    run_checked(["git", "-C", str(source), "checkout", "--quiet", "--detach", manifest["product_source_commit"]])
    expected = manifest["runtime"]["product_binary"]
    expected_version = expected.get("version")
    if not isinstance(expected_version, str) or not expected_version.startswith("quality-review "):
        raise ValueError("product binary version is invalid")
    build_version = expected_version.removeprefix("quality-review ")
    command = [
        "go",
        "-C",
        str(source),
        "build",
        "-trimpath",
        "-ldflags",
        f"-s -w -X main.version={build_version}",
        "-o",
        str(binary),
        "./cmd/quality-review",
    ]
    run_checked(command)
    if file_sha256(binary) != expected["sha256"]:
        raise ValueError("product binary hash mismatch")
    if run_checked([str(binary), "version"]).strip() != expected["version"]:
        raise ValueError("product binary version mismatch")
    return binary


def check_runtime(codex_binary: pathlib.Path, auth_path: pathlib.Path, manifest: dict[str, Any]) -> list[str]:
    errors = []
    if not codex_binary.is_file():
        errors.append("Codex binary is unavailable")
    else:
        try:
            version = run_checked([str(codex_binary), "--version"]).strip()
            if version != manifest["runtime"]["codex_cli_version"]:
                errors.append(f"Codex version mismatch: {version}")
        except subprocess.CalledProcessError as error:
            errors.append(f"Codex version check failed: {error}")
    if auth_path.is_symlink() or not auth_path.is_file() or auth_path.stat().st_size == 0:
        errors.append("Codex auth file is unavailable or unsafe")
    else:
        auth_env = os.environ.copy()
        auth_env["CODEX_HOME"] = str(auth_path.parent)
        try:
            status = run_checked([str(codex_binary), "login", "status"], env=auth_env).strip()
            if "Logged in" not in status:
                errors.append("Codex login status is not authenticated")
        except subprocess.CalledProcessError as error:
            errors.append(f"Codex login status failed: {error}")
    return errors


def run_preflight(
    root: pathlib.Path,
    manifest_path: pathlib.Path,
    codex_binary: pathlib.Path,
    auth_path: pathlib.Path,
) -> dict[str, Any]:
    manifest = read_json(manifest_path)
    audit = audit_protocol(root, manifest_path)
    errors = list(audit["errors"])
    errors.extend(check_runtime(codex_binary, auth_path, manifest))
    materialized: dict[str, Any] = {}
    binary_sha256 = ""
    binary_version = ""
    if not errors:
        with tempfile.TemporaryDirectory(prefix="native-review-feasibility-preflight-") as directory:
            working = pathlib.Path(directory)
            try:
                binary = build_product_binary(root, manifest, working)
                binary_sha256 = file_sha256(binary)
                binary_version = run_checked([str(binary), "version"]).strip()
                for selected in manifest["cases"]:
                    opaque_id = selected["opaque_id"]
                    fixture = root / "pilot" / "fixtures" / selected["case_id"].lower()
                    repository = working / "materialized" / opaque_id / "repo"
                    record = materialize_fixture(fixture, repository)
                    baseline = build_baseline_command(codex_binary, working / "baseline.txt")
                    goal = build_goal_command(
                        binary,
                        repository,
                        record["base_commit"],
                        record["target_commit"],
                        selected["goal"],
                        working / "sessions",
                    )
                    if "--commit" not in baseline or "--goal" not in goal:
                        raise ValueError("lane command contract is invalid")
                    record["fixture_sha256"] = tree_sha256(fixture)
                    materialized[opaque_id] = record
            except (OSError, ValueError, subprocess.CalledProcessError) as error:
                errors.append(f"preflight execution failed: {error}")
    return {
        "schema_version": 1,
        "profile": manifest.get("profile"),
        "generated_at": utc_now(),
        "ready": not errors,
        "model_calls": 0,
        "errors": errors,
        "protocol_manifest_sha256": file_sha256(manifest_path),
        "protocol_audit": audit,
        "codex_cli_version": manifest["runtime"]["codex_cli_version"],
        "auth_available": not any("auth" in error.lower() or "login" in error.lower() for error in errors),
        "product_binary_sha256": binary_sha256,
        "product_binary_version": binary_version,
        "materialized_cases": materialized,
    }


def prepare_lane_clone(source: pathlib.Path, destination: pathlib.Path, target_commit: str) -> None:
    run_checked(["git", "clone", "--quiet", "--shared", "--no-checkout", str(source), str(destination)])
    run_checked(["git", "-C", str(destination), "checkout", "--quiet", "--detach", target_commit])
    remotes = run_checked(["git", "-C", str(destination), "remote"]).splitlines()
    for remote in remotes:
        run_checked(["git", "-C", str(destination), "remote", "remove", remote])
    refs = run_checked(["git", "-C", str(destination), "for-each-ref", "--format=%(refname)"]).splitlines()
    for ref in refs:
        run_checked(["git", "-C", str(destination), "update-ref", "-d", ref])
    if run_checked(["git", "-C", str(destination), "remote"]).strip():
        raise ValueError("lane clone retains a remote")
    if run_checked(["git", "-C", str(destination), "for-each-ref", "--format=%(refname)"]).strip():
        raise ValueError("lane clone retains a ref")


def execute_process(
    command: list[str],
    *,
    cwd: pathlib.Path,
    env: dict[str, str],
    stdout_path: pathlib.Path,
    stderr_path: pathlib.Path,
    timeout: int,
) -> tuple[int, str | None]:
    stdout_path.parent.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    try:
        with stdout_path.open("w", encoding="utf-8") as stdout, stderr_path.open("w", encoding="utf-8") as stderr:
            process = subprocess.run(command, cwd=cwd, env=env, stdout=stdout, stderr=stderr, text=True, timeout=timeout)
        return process.returncode, None
    except subprocess.TimeoutExpired:
        duration = int(time.monotonic() - started)
        return 124, f"session timed out after {duration} seconds"


def run_session(
    root: pathlib.Path,
    manifest: dict[str, Any],
    selected: dict[str, Any],
    session: dict[str, Any],
    materialized: dict[str, Any],
    canonical_repository: pathlib.Path,
    quality_review_binary: pathlib.Path,
    codex_binary: pathlib.Path,
    auth_path: pathlib.Path,
    evidence_root: pathlib.Path,
    working_root: pathlib.Path,
    print_lock: threading.Lock,
) -> dict[str, Any]:
    run_id = session["run_id"]
    opaque_id = session["opaque_id"]
    lane = session["lane"]
    evidence = evidence_root / opaque_id / lane
    if evidence.exists():
        raise ValueError(f"refusing to overwrite evidence: {evidence}")
    input_dir = evidence / "input"
    output_dir = evidence / "output"
    input_dir.mkdir(parents=True)
    output_dir.mkdir(parents=True)
    run_work = working_root / "runs" / run_id
    repository = run_work / "repo"
    codex_home = run_work / "codex-home"
    codex_home.mkdir(parents=True)
    os.chmod(codex_home, 0o700)
    shutil.copyfile(auth_path, codex_home / "auth.json")
    os.chmod(codex_home / "auth.json", 0o600)
    prepare_lane_clone(canonical_repository, repository, materialized["target_commit"])
    diff = subprocess.check_output(
        [
            "git",
            "-C",
            str(repository),
            "diff",
            "--no-ext-diff",
            "--unified=6",
            materialized["base_commit"],
            materialized["target_commit"],
            "--",
        ]
    )
    (input_dir / "trusted.diff").write_bytes(diff)
    write_json(
        input_dir / "run-input.json",
        {
            "schema_version": 1,
            "run_id": run_id,
            "opaque_id": opaque_id,
            "lane": lane,
            "base_commit": materialized["base_commit"],
            "target_commit": materialized["target_commit"],
            "diff_sha256": sha256_bytes(diff),
            "model": MODEL,
            "reasoning_effort": REASONING_EFFORT,
            "goal": selected["goal"] if lane == "goal_native" else None,
        },
    )
    env = os.environ.copy()
    env["CODEX_HOME"] = str(codex_home)
    env["PATH"] = str(codex_binary.parent) + os.pathsep + env.get("PATH", "")
    started_at = utc_now()
    started = time.monotonic()
    with print_lock:
        print(f"START {run_id} {opaque_id} {lane} {started_at}", flush=True)
    errors: list[str] = []
    result_path = ""
    native_path = output_dir / "native-review.txt"
    summary_path = output_dir / "runner.stdout.log"
    stderr_path = output_dir / "runner.stderr.log"
    model_calls = 1
    semantic_result = "INCOMPLETE"
    verifier_status = "not_applicable"
    command: list[str]
    if lane == "baseline_native":
        command = build_baseline_command(codex_binary, native_path)
        exit_code, process_error = execute_process(
            command,
            cwd=repository,
            env=env,
            stdout_path=summary_path,
            stderr_path=stderr_path,
            timeout=manifest["runtime"]["session_timeout_seconds"],
        )
        if process_error:
            errors.append(process_error)
        findings: list[dict[str, Any]] = []
        if exit_code == 0 and native_path.is_file():
            try:
                findings = normalize_native_review(native_path.read_text(encoding="utf-8"), repository)
                semantic_result = "MANUAL_REVIEW" if findings else "PASS"
            except (OSError, ValueError) as error:
                errors.append(f"native output normalization failed: {error}")
        else:
            errors.append(f"baseline process failed with exit {exit_code}")
        write_json(output_dir / "normalized-findings.json", {"schema_version": 1, "findings": findings})
    else:
        sessions_root = evidence / "sessions"
        command = build_goal_command(
            quality_review_binary,
            repository,
            materialized["base_commit"],
            materialized["target_commit"],
            selected["goal"],
            sessions_root,
        )
        exit_code, process_error = execute_process(
            command,
            cwd=repository,
            env=env,
            stdout_path=summary_path,
            stderr_path=stderr_path,
            timeout=manifest["runtime"]["session_timeout_seconds"],
        )
        if process_error:
            errors.append(process_error)
        summary: dict[str, Any] = {}
        if summary_path.is_file() and summary_path.stat().st_size:
            try:
                summary = read_json(summary_path)
            except (OSError, ValueError, json.JSONDecodeError) as error:
                errors.append(f"product summary is invalid: {error}")
        candidate = pathlib.Path(summary.get("result_path", "")) if summary.get("result_path") else None
        if candidate and candidate.is_file():
            result_path = str(candidate)
        else:
            candidates = sorted(sessions_root.glob("review-*/output/review-result.json"))
            if len(candidates) == 1:
                candidate = candidates[0]
                result_path = str(candidate)
        findings = []
        if result_path:
            try:
                result = read_json(pathlib.Path(result_path))
                findings = normalize_product_result(result)
                execution = result.get("execution", {})
                adjudication = result.get("adjudication", {})
                model_calls = execution.get("model_calls", 0)
                verifier_status = execution.get("verifier_status", "unknown")
                semantic_result = adjudication.get("semantic_result", "INCOMPLETE")
                native_candidate = pathlib.Path(result_path).parent / "native-review.txt"
                if native_candidate.is_file():
                    shutil.copyfile(native_candidate, native_path)
            except (OSError, ValueError, KeyError) as error:
                errors.append(f"product result is invalid: {error}")
        else:
            errors.append(f"product process produced no result (exit {exit_code})")
        if semantic_result == "INCOMPLETE":
            errors.append("product semantic result is INCOMPLETE")
        write_json(output_dir / "normalized-findings.json", {"schema_version": 1, "findings": findings})

    call_limit = manifest["lanes"][lane]["maximum_calls_per_case"]
    if not isinstance(model_calls, int) or model_calls < 1 or model_calls > call_limit:
        errors.append(f"model-call count {model_calls} exceeds lane contract")
    duration = round(time.monotonic() - started, 3)
    status = "COMPLETE" if not errors else "INCOMPLETE"
    finished_at = utc_now()
    metadata = {
        "schema_version": 1,
        "run_id": run_id,
        "opaque_id": opaque_id,
        "lane": lane,
        "status": status,
        "semantic_result": semantic_result,
        "model": MODEL,
        "reasoning_effort": REASONING_EFFORT,
        "model_calls": model_calls,
        "call_limit": call_limit,
        "verifier_status": verifier_status,
        "exit_code": exit_code,
        "started_at": started_at,
        "finished_at": finished_at,
        "duration_seconds": duration,
        "errors": errors,
        "command": command,
        "result_path": result_path,
        "native_review_sha256": file_sha256(native_path) if native_path.is_file() else "",
        "normalized_findings_sha256": file_sha256(output_dir / "normalized-findings.json"),
    }
    write_json(evidence / "run-metadata.json", metadata)
    with print_lock:
        print(
            f"DONE {run_id} {opaque_id} {lane} status={status} result={semantic_result} "
            f"calls={model_calls} duration={duration}s",
            flush=True,
        )
    return metadata


def execute_experiment(
    root: pathlib.Path,
    manifest_path: pathlib.Path,
    preflight_path: pathlib.Path,
    evidence_root: pathlib.Path,
    codex_binary: pathlib.Path,
    auth_path: pathlib.Path,
) -> dict[str, Any]:
    manifest = read_json(manifest_path)
    audit = audit_protocol(root, manifest_path)
    if not audit["ready"]:
        raise ValueError("protocol audit is not ready: " + "; ".join(audit["errors"]))
    preflight = read_json(preflight_path)
    if not preflight.get("ready") or preflight.get("model_calls") != 0:
        raise ValueError("clean zero-call preflight is required")
    if preflight.get("profile") != manifest.get("profile"):
        raise ValueError("preflight profile mismatch")
    if preflight.get("protocol_manifest_sha256") != file_sha256(manifest_path):
        raise ValueError("preflight protocol hash mismatch")
    runtime_errors = check_runtime(codex_binary, auth_path, manifest)
    if runtime_errors:
        raise ValueError("; ".join(runtime_errors))
    if evidence_root.exists():
        raise ValueError(f"refusing to overwrite evidence root: {evidence_root}")
    evidence_root.mkdir(parents=True)
    write_json(
        evidence_root / "execution-plan.json",
        {
            "schema_version": 1,
            "profile": manifest.get("profile"),
            "protocol_manifest_sha256": file_sha256(manifest_path),
            "preflight_sha256": file_sha256(preflight_path),
            "maximum_model_calls": manifest["maximum_model_calls"],
            "session_order": manifest["session_order"],
        },
    )
    case_by_opaque = {case["opaque_id"]: case for case in manifest["cases"]}
    print_lock = threading.Lock()
    records: list[dict[str, Any]] = []
    runner_errors: list[str] = []
    started_at = utc_now()
    with tempfile.TemporaryDirectory(prefix="native-review-feasibility-execution-") as directory:
        working = pathlib.Path(directory)
        quality_review_binary = build_product_binary(root, manifest, working)
        canonical: dict[str, pathlib.Path] = {}
        materialized: dict[str, dict[str, Any]] = {}
        for selected in manifest["cases"]:
            opaque_id = selected["opaque_id"]
            repository = working / "materialized" / opaque_id / "repo"
            fixture = root / "pilot" / "fixtures" / selected["case_id"].lower()
            materialized[opaque_id] = materialize_fixture(fixture, repository)
            expected = preflight["materialized_cases"][opaque_id]
            if materialized[opaque_id] != {
                key: expected[key] for key in ("base_commit", "target_commit", "diff_sha256", "changed_files")
            }:
                raise ValueError(f"{opaque_id}: materialization differs from preflight")
            canonical[opaque_id] = repository

        with concurrent.futures.ThreadPoolExecutor(max_workers=manifest["runtime"]["max_parallel_sessions"]) as pool:
            futures = []
            for session in manifest["session_order"]:
                selected = case_by_opaque[session["opaque_id"]]
                futures.append(
                    pool.submit(
                        run_session,
                        root,
                        manifest,
                        selected,
                        session,
                        materialized[session["opaque_id"]],
                        canonical[session["opaque_id"]],
                        quality_review_binary,
                        codex_binary,
                        auth_path,
                        evidence_root,
                        working,
                        print_lock,
                    )
                )
            for future in concurrent.futures.as_completed(futures):
                try:
                    records.append(future.result())
                except Exception as error:  # Preserve every other frozen run even if the harness fails once.
                    runner_errors.append(str(error))
                    with print_lock:
                        print(f"RUNNER_ERROR {error}", flush=True)

    records.sort(key=lambda record: record["run_id"])
    actual_calls = sum(record["model_calls"] for record in records if isinstance(record.get("model_calls"), int))
    summary = {
        "schema_version": 1,
        "profile": manifest.get("profile"),
        "started_at": started_at,
        "finished_at": utc_now(),
        "protocol_manifest_sha256": file_sha256(manifest_path),
        "preflight_sha256": file_sha256(preflight_path),
        "maximum_model_calls": manifest["maximum_model_calls"],
        "actual_model_calls": actual_calls,
        "call_ceiling_respected": actual_calls <= manifest["maximum_model_calls"],
        "session_count": len(records),
        "complete_sessions": sum(record["status"] == "COMPLETE" for record in records),
        "incomplete_sessions": [record["run_id"] for record in records if record["status"] != "COMPLETE"],
        "runner_errors": runner_errors,
        "ready_for_adjudication": len(records) == 16 and not runner_errors and actual_calls <= manifest["maximum_model_calls"],
        "sessions": records,
    }
    write_json(evidence_root / "execution-summary.json", summary)
    return summary


def create_blind_packet(
    manifest_path: pathlib.Path,
    evidence_root: pathlib.Path,
    packet_path: pathlib.Path,
    mapping_path: pathlib.Path,
    judgments_path: pathlib.Path,
) -> None:
    manifest = read_json(manifest_path)
    summary = read_json(evidence_root / "execution-summary.json")
    if not summary.get("ready_for_adjudication"):
        raise ValueError("execution is not ready for adjudication")
    randomizer = secrets.SystemRandom()
    mapping_cases = []
    judgment_cases = []
    markdown = [
        "# Blind Goal-mode Feasibility Adjudication Packet",
        "",
        "Judge each output only against the frozen expected root. Lane identity is sealed separately.",
        "For positives, pass only when at least one finding states the introduced root and concrete impact. For counterexamples, pass only when there is no actionable introduced finding. Use blocked only for a genuine defect that invalidates admission.",
        "",
    ]
    for selected in manifest["cases"]:
        opaque_id = selected["opaque_id"]
        lane_order = list(LANES)
        randomizer.shuffle(lane_order)
        labels = ("Output 1", "Output 2")
        mapping_cases.append(
            {
                "opaque_id": opaque_id,
                "outputs": [
                    {"label": label, "lane": lane}
                    for label, lane in zip(labels, lane_order, strict=True)
                ],
            }
        )
        outputs = []
        markdown.extend(
            [
                f"## {opaque_id}",
                "",
                f"Kind: `{selected['kind']}`",
                "",
                f"Expected root: {selected['expected_root']}",
                "",
            ]
        )
        for label, lane in zip(labels, lane_order, strict=True):
            metadata = read_json(evidence_root / opaque_id / lane / "run-metadata.json")
            normalized = read_json(evidence_root / opaque_id / lane / "output" / "normalized-findings.json")
            outputs.append({"label": label, "verdict": "", "reason": ""})
            markdown.extend(
                [
                    f"### {label}",
                    "",
                    f"Operational status: `{metadata['status']}`",
                    "",
                    "```json",
                    json.dumps(normalized["findings"], indent=2, sort_keys=True),
                    "```",
                    "",
                ]
            )
        judgment_cases.append(
            {
                "opaque_id": opaque_id,
                "kind": selected["kind"],
                "expected_root": selected["expected_root"],
                "outputs": outputs,
            }
        )
    profile = manifest.get("profile")
    mapping = {"schema_version": 1, "profile": profile, "cases": mapping_cases}
    judgments = {"schema_version": 1, "profile": profile, "status": "unadjudicated", "cases": judgment_cases}
    write_json(mapping_path, mapping)
    os.chmod(mapping_path, 0o600)
    write_json(judgments_path, judgments)
    packet_path.parent.mkdir(parents=True, exist_ok=True)
    packet_path.write_text("\n".join(markdown), encoding="utf-8")


def score_judgments(
    manifest_path: pathlib.Path,
    evidence_root: pathlib.Path,
    mapping_path: pathlib.Path,
    judgments_path: pathlib.Path,
) -> dict[str, Any]:
    manifest = read_json(manifest_path)
    mapping = read_json(mapping_path)
    judgments = read_json(judgments_path)
    profile = manifest.get("profile")
    if mapping.get("profile") != profile or judgments.get("profile") != profile:
        raise ValueError("blind artifacts do not match the protocol profile")
    if judgments.get("status") != "adjudicated":
        raise ValueError("judgments are not frozen as adjudicated")
    case_by_opaque = {case["opaque_id"]: case for case in manifest["cases"]}
    map_by_opaque = {
        case["opaque_id"]: {output["label"]: output["lane"] for output in case["outputs"]}
        for case in mapping["cases"]
    }
    totals = {
        lane: {
            "positive": {"pass": 0, "fail": 0},
            "counterexample": {"pass": 0, "fail": 0},
        }
        for lane in LANES
    }
    blocked_cases: set[str] = set()
    records = []
    for judgment in judgments.get("cases", []):
        opaque_id = judgment["opaque_id"]
        selected = case_by_opaque[opaque_id]
        if judgment.get("expected_root") != selected["expected_root"] or judgment.get("kind") != selected["kind"]:
            raise ValueError(f"{opaque_id}: judgment target drifted")
        for output in judgment.get("outputs", []):
            label = output.get("label")
            verdict = output.get("verdict")
            reason = output.get("reason")
            if verdict not in {"pass", "fail", "blocked"} or not isinstance(reason, str) or not reason.strip():
                raise ValueError(f"{opaque_id} {label}: judgment is incomplete")
            lane = map_by_opaque[opaque_id][label]
            metadata = read_json(evidence_root / opaque_id / lane / "run-metadata.json")
            effective = verdict
            if metadata["status"] != "COMPLETE" and verdict == "pass":
                raise ValueError(f"{opaque_id} {label}: incomplete run cannot pass")
            if verdict == "blocked":
                blocked_cases.add(opaque_id)
            else:
                totals[lane][selected["kind"]][verdict] += 1
            records.append(
                {
                    "opaque_id": opaque_id,
                    "case_id": selected["case_id"],
                    "kind": selected["kind"],
                    "lane": lane,
                    "blind_label": label,
                    "verdict": effective,
                    "reason": reason,
                }
            )
    if len(records) != 16:
        raise ValueError("judgments must cover all 16 outputs")
    summary = read_json(evidence_root / "execution-summary.json")
    goal_positive = totals["goal_native"]["positive"]["pass"]
    goal_counter = totals["goal_native"]["counterexample"]["pass"]
    base_positive = totals["baseline_native"]["positive"]["pass"]
    base_counter = totals["baseline_native"]["counterexample"]["pass"]
    conditions = {
        "all_sessions_complete": summary.get("complete_sessions") == 16,
        "goal_positive_passes_at_least_three": goal_positive >= 3,
        "goal_counterexample_passes_at_least_three": goal_counter >= 3,
        "goal_not_worse_on_positive": goal_positive >= base_positive,
        "goal_not_worse_on_counterexample": goal_counter >= base_counter,
        "no_blocked_cases": not blocked_cases,
        "all_hashes_match": summary.get("protocol_manifest_sha256") == file_sha256(manifest_path),
    }
    return {
        "schema_version": 1,
        "profile": profile,
        "generated_at": utc_now(),
        "expand_to_40": all(conditions.values()),
        "conditions": conditions,
        "blocked_cases": sorted(blocked_cases),
        "totals": totals,
        "actual_model_calls": summary.get("actual_model_calls"),
        "maximum_model_calls": summary.get("maximum_model_calls"),
        "records": records,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--manifest",
        type=pathlib.Path,
        default=pathlib.Path("evals/native-review-feasibility-v2.json"),
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("audit")

    preflight = subparsers.add_parser("preflight")
    preflight.add_argument("--codex-bin", type=pathlib.Path, required=True)
    preflight.add_argument("--auth", type=pathlib.Path, required=True)
    preflight.add_argument("--output", type=pathlib.Path, required=True)

    execute = subparsers.add_parser("execute")
    execute.add_argument("--codex-bin", type=pathlib.Path, required=True)
    execute.add_argument("--auth", type=pathlib.Path, required=True)
    execute.add_argument("--preflight", type=pathlib.Path, required=True)
    execute.add_argument("--evidence", type=pathlib.Path, required=True)

    blind = subparsers.add_parser("blind")
    blind.add_argument("--evidence", type=pathlib.Path, required=True)
    blind.add_argument("--packet", type=pathlib.Path, required=True)
    blind.add_argument("--mapping", type=pathlib.Path, required=True)
    blind.add_argument("--judgments", type=pathlib.Path, required=True)

    score = subparsers.add_parser("score")
    score.add_argument("--evidence", type=pathlib.Path, required=True)
    score.add_argument("--mapping", type=pathlib.Path, required=True)
    score.add_argument("--judgments", type=pathlib.Path, required=True)
    score.add_argument("--output", type=pathlib.Path, required=True)

    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent.parent
    manifest_path = args.manifest.resolve(strict=True)
    try:
        if args.command == "audit":
            report = audit_protocol(root, manifest_path)
            json.dump(report, sys.stdout, indent=2, sort_keys=True)
            sys.stdout.write("\n")
            return 0 if report["ready"] else 1
        if args.command == "preflight":
            report = run_preflight(root, manifest_path, args.codex_bin.resolve(strict=True), args.auth.resolve(strict=True))
            write_json(args.output, report)
            json.dump(report, sys.stdout, indent=2, sort_keys=True)
            sys.stdout.write("\n")
            return 0 if report["ready"] else 1
        if args.command == "execute":
            summary = execute_experiment(
                root,
                manifest_path,
                args.preflight.resolve(strict=True),
                args.evidence.resolve(),
                args.codex_bin.resolve(strict=True),
                args.auth.resolve(strict=True),
            )
            return 0 if summary["ready_for_adjudication"] else 1
        if args.command == "blind":
            create_blind_packet(
                manifest_path,
                args.evidence.resolve(strict=True),
                args.packet.resolve(),
                args.mapping.resolve(),
                args.judgments.resolve(),
            )
            return 0
        if args.command == "score":
            outcome = score_judgments(
                manifest_path,
                args.evidence.resolve(strict=True),
                args.mapping.resolve(strict=True),
                args.judgments.resolve(strict=True),
            )
            write_json(args.output, outcome)
            json.dump(outcome, sys.stdout, indent=2, sort_keys=True)
            sys.stdout.write("\n")
            return 0
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"native-review-feasibility: {error}", file=sys.stderr)
        return 1
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
