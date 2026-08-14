#!/usr/bin/env python3
"""Run and score the frozen native-review restricted-adjudication experiment."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
import os
import pathlib
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any


PROFILE = "restricted_adjudication_blind_eval_v2"
CASE_PROFILE = "restricted_adjudication_frozen_case_v1"
MODEL = "gpt-5.6-sol"
REASONING_EFFORT = "max"
MAX_WORKERS = 1
CALL_TIMEOUT_SECONDS = 1200
SPEC_PATH = pathlib.Path("2026-08-14-restricted-adjudication-blind-evaluation-spec.md")
POLICY_PATH = pathlib.Path("pilot/restricted-adjudication-policy.md")
SCHEMA_PATH = pathlib.Path("schemas/restricted-adjudication-output.schema.json")
RUNNER_PATH = pathlib.Path("pilot/restricted_adjudication.py")
TEST_PATH = pathlib.Path("pilot/test_restricted_adjudication.py")
VALIDITIES = {"SUPPORTED", "CONTRADICTED", "INSUFFICIENT"}
SEVERITIES = {"S1", "S2", "S3"}
TRIGGERS = {"T1", "T2", "T3"}
EVIDENCE_LEVELS = {"E1", "E2", "E3"}
RECOMMENDATIONS = {"BLOCK", "MANUAL_REVIEW", "ADVISORY", "REJECT"}
NATIVE_CALL_LOCK = threading.Lock()


def utc_now() -> str:
    return dt.datetime.now(dt.UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def load_json(path: pathlib.Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: pathlib.Path, value: object, mode: int = 0o644) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    encoded = (json.dumps(value, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode()
    temporary = path.with_name(f".{path.name}.{secrets.token_hex(6)}.tmp")
    with temporary.open("xb") as handle:
        handle.write(encoded)
    os.chmod(temporary, mode)
    os.replace(temporary, path)


def write_bytes(path: pathlib.Path, value: bytes, mode: int = 0o644) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{secrets.token_hex(6)}.tmp")
    with temporary.open("xb") as handle:
        handle.write(value)
    os.chmod(temporary, mode)
    os.replace(temporary, path)


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run_checked(args: list[str], *, cwd: pathlib.Path | None = None) -> str:
    completed = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:3])}): {completed.stderr.strip()}")
    return completed.stdout.strip()


def git(repository: pathlib.Path, *args: str) -> str:
    return run_checked(["git", "-C", str(repository), *args])


def model_process(
    command: list[str],
    *,
    cwd: pathlib.Path,
    stdin: str | None,
    timeout_seconds: int,
) -> dict[str, Any]:
    started = time.monotonic()
    process = subprocess.Popen(
        command,
        cwd=cwd,
        stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    timed_out = False
    try:
        stdout, stderr = process.communicate(
            None if stdin is None else stdin.encode(), timeout=timeout_seconds
        )
    except subprocess.TimeoutExpired:
        timed_out = True
        os.killpg(process.pid, signal.SIGTERM)
        try:
            stdout, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
    except KeyboardInterrupt:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate()
        raise
    duration_ms = round((time.monotonic() - started) * 1000)
    return {
        "returncode": process.returncode,
        "timed_out": timed_out,
        "duration_ms": duration_ms,
        "stdout": stdout,
        "stderr": stderr,
    }


def codex_usage(stdout: bytes) -> dict[str, int | None]:
    latest: dict[str, Any] | None = None
    for raw_line in stdout.splitlines():
        try:
            event = json.loads(raw_line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict) and event.get("type") == "turn.completed":
            usage = event.get("usage")
            if isinstance(usage, dict):
                latest = usage
    if latest is None:
        return {"input_tokens": None, "output_tokens": None}
    return {
        "input_tokens": latest.get("input_tokens") if isinstance(latest.get("input_tokens"), int) else None,
        "output_tokens": latest.get("output_tokens") if isinstance(latest.get("output_tokens"), int) else None,
    }


def clone_target(
    source: pathlib.Path,
    target: pathlib.Path,
    base: str,
    head: str,
    *,
    require_first_parent: bool = True,
) -> None:
    run_checked(["git", "clone", "--quiet", "--no-hardlinks", str(source), str(target)])
    git(target, "checkout", "--quiet", "--detach", head)
    remotes = git(target, "remote").splitlines()
    for remote in remotes:
        if remote.strip():
            git(target, "remote", "remove", remote.strip())
    if git(target, "rev-parse", "HEAD") != head:
        raise ValueError("cloned target HEAD mismatch")
    if require_first_parent:
        if git(target, "rev-parse", f"{head}^1") != base:
            raise ValueError("cloned target first parent mismatch")
    else:
        run_checked(["git", "-C", str(target), "merge-base", "--is-ancestor", base, head])
    if git(target, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("cloned target is dirty")


def sanitized_command(command: list[str], policy_sha256: str | None = None) -> list[str]:
    sanitized: list[str] = []
    for value in command:
        if value.startswith("developer_instructions="):
            sanitized.append(f"developer_instructions=sha256:{policy_sha256}")
        else:
            sanitized.append(value)
    return sanitized


def find_single(root: pathlib.Path, name: str) -> pathlib.Path:
    matches = list(root.rglob(name))
    if len(matches) != 1:
        raise ValueError(f"expected exactly one {name}, found {len(matches)}")
    return matches[0]


def adjudication_prompt(base: str, target: str, findings: list[dict[str, Any]]) -> str:
    encoded = json.dumps(findings, indent=2, sort_keys=True, ensure_ascii=False)
    return f"""Adjudicate only the frozen findings below for committed change {base}..{target}.

Use target-reachable repository evidence and the exact Git diff. Do not inspect any later commit, remote, sibling experiment, historical outcome, or expected label. Do not add findings. Return one schema-valid adjudication per supplied finding_id, in the supplied order.

Frozen native findings:
{encoded}
"""


def evidence_ref_valid(repository: pathlib.Path, value: Any) -> bool:
    if not isinstance(value, dict) or set(value) != {"path", "start_line", "end_line", "support"}:
        return False
    raw_path = value.get("path")
    start = value.get("start_line")
    end = value.get("end_line")
    support = value.get("support")
    if not isinstance(raw_path, str) or not raw_path or pathlib.PurePosixPath(raw_path).is_absolute():
        return False
    if ".." in pathlib.PurePosixPath(raw_path).parts:
        return False
    if not isinstance(start, int) or not isinstance(end, int) or start < 1 or end < start:
        return False
    if not isinstance(support, str) or not support.strip() or len(support) > 500:
        return False
    path = repository / raw_path
    if path.is_symlink() or not path.is_file():
        return False
    try:
        line_count = len(path.read_bytes().splitlines())
    except OSError:
        return False
    return end <= max(1, line_count)


def computed_disposition(adjudication: dict[str, Any], *, valid_evidence_refs: bool = True) -> str:
    validity = adjudication.get("validity")
    if validity == "CONTRADICTED":
        return "REJECT"
    if validity == "INSUFFICIENT":
        return "MANUAL_REVIEW"
    block = (
        validity == "SUPPORTED"
        and adjudication.get("severity") == "S3"
        and adjudication.get("trigger_confidence") == "T3"
        and adjudication.get("evidence_level") in {"E2", "E3"}
        and adjudication.get("introduced_or_worsened_by_change") is True
        and adjudication.get("trigger_condition_is_concrete") is True
        and adjudication.get("causal_chain_is_complete") is True
        and adjudication.get("finding_is_not_style_preference") is True
        and valid_evidence_refs
    )
    if block:
        return "BLOCK"
    if (
        adjudication.get("severity") == "S3"
        or adjudication.get("trigger_confidence") == "T2"
        or adjudication.get("evidence_level") == "E1"
    ):
        return "MANUAL_REVIEW"
    return "ADVISORY"


def validate_adjudication_payload(
    payload: Any,
    findings: list[dict[str, Any]],
    repository: pathlib.Path,
) -> list[dict[str, Any]]:
    if not isinstance(payload, dict) or set(payload) != {"adjudications"}:
        raise ValueError("adjudication root must contain only adjudications")
    values = payload.get("adjudications")
    if not isinstance(values, list) or len(values) != len(findings):
        raise ValueError("adjudication count does not match frozen findings")
    expected_ids = [finding.get("id") for finding in findings]
    if any(not isinstance(value, str) or not value for value in expected_ids):
        raise ValueError("frozen findings are missing stable IDs")
    normalized: list[dict[str, Any]] = []
    required = {
        "finding_id",
        "validity",
        "severity",
        "trigger_confidence",
        "evidence_level",
        "introduced_or_worsened_by_change",
        "trigger_condition_is_concrete",
        "causal_chain_is_complete",
        "finding_is_not_style_preference",
        "recommended_disposition",
        "evidence_refs",
        "uncertainties",
        "reason",
    }
    for index, value in enumerate(values):
        if not isinstance(value, dict) or set(value) != required:
            raise ValueError(f"adjudication {index} fields are invalid")
        if value["finding_id"] != expected_ids[index]:
            raise ValueError(f"adjudication {index} finding ID/order mismatch")
        if value["validity"] not in VALIDITIES:
            raise ValueError(f"adjudication {index} validity is invalid")
        if value["severity"] not in SEVERITIES:
            raise ValueError(f"adjudication {index} severity is invalid")
        if value["trigger_confidence"] not in TRIGGERS:
            raise ValueError(f"adjudication {index} trigger confidence is invalid")
        if value["evidence_level"] not in EVIDENCE_LEVELS:
            raise ValueError(f"adjudication {index} evidence level is invalid")
        if value["recommended_disposition"] not in RECOMMENDATIONS:
            raise ValueError(f"adjudication {index} recommendation is invalid")
        for field in (
            "introduced_or_worsened_by_change",
            "trigger_condition_is_concrete",
            "causal_chain_is_complete",
            "finding_is_not_style_preference",
        ):
            if not isinstance(value[field], bool):
                raise ValueError(f"adjudication {index} {field} must be boolean")
        if not isinstance(value["reason"], str) or not value["reason"].strip() or len(value["reason"]) > 1500:
            raise ValueError(f"adjudication {index} reason is invalid")
        if not isinstance(value["uncertainties"], list) or any(
            not isinstance(item, str) or not item.strip() or len(item) > 500
            for item in value["uncertainties"]
        ):
            raise ValueError(f"adjudication {index} uncertainties are invalid")
        refs = value["evidence_refs"]
        if not isinstance(refs, list):
            raise ValueError(f"adjudication {index} evidence refs are invalid")
        refs_valid = bool(refs) and all(evidence_ref_valid(repository, ref) for ref in refs)
        computed = computed_disposition(value, valid_evidence_refs=refs_valid)
        normalized.append(
            {
                **value,
                "evidence_refs_valid": refs_valid,
                "computed_disposition": computed,
                "recommendation_matches_computed": value["recommended_disposition"] == computed,
            }
        )
    return normalized


def native_result_summary(result: dict[str, Any]) -> dict[str, Any]:
    findings = result.get("findings")
    if not isinstance(findings, list):
        raise ValueError("native result findings are invalid")
    priorities: list[int] = []
    for index, finding in enumerate(findings):
        if not isinstance(finding, dict) or not isinstance(finding.get("priority"), int):
            raise ValueError(f"native finding {index} priority is invalid")
        if finding["priority"] not in {0, 1, 2, 3}:
            raise ValueError(f"native finding {index} priority is out of range")
        priorities.append(finding["priority"])
    return {
        "finding_count": len(findings),
        "priorities": priorities,
        "predicts_block": any(priority in {0, 1} for priority in priorities),
    }


def native_call_completed(returncode: int, timed_out: bool) -> bool:
    """The product uses 0 for PASS and 3 for a valid BLOCK result."""
    return not timed_out and returncode in {0, 3}


def adjudicate_findings(
    workspace: pathlib.Path,
    evidence: pathlib.Path,
    source: pathlib.Path,
    base: str,
    target: str,
    findings: list[dict[str, Any]],
    runtime_root: pathlib.Path,
    *,
    require_first_parent: bool = True,
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    if not findings:
        return [], {
            "called": False,
            "duration_ms": 0,
            "input_tokens": 0,
            "output_tokens": 0,
        }

    adjudication_repo = runtime_root / "adjudication-repository"
    clone_target(
        source,
        adjudication_repo,
        base,
        target,
        require_first_parent=require_first_parent,
    )
    policy_path = workspace / "frozen" / POLICY_PATH.name
    schema_path = workspace / "frozen" / SCHEMA_PATH.name
    policy = policy_path.read_text(encoding="utf-8")
    policy_sha256 = file_sha256(policy_path)
    final_path = evidence / "adjudicator-final.json"
    prompt = adjudication_prompt(base, target, findings)
    write_bytes(evidence / "adjudicator-prompt.txt", prompt.encode())
    developer_override = "developer_instructions=" + json.dumps(policy, ensure_ascii=False)
    adjudicator_command = [
        "codex",
        "exec",
        "--sandbox",
        "read-only",
        "--ignore-user-config",
        "--ignore-rules",
        "--ephemeral",
        "--disable",
        "hooks",
        "--model",
        MODEL,
        "--output-schema",
        str(schema_path),
        "--config",
        f'model_reasoning_effort="{REASONING_EFFORT}"',
        "--config",
        developer_override,
        "--json",
        "--output-last-message",
        str(final_path),
        "-",
    ]
    adjudicator_run = model_process(
        adjudicator_command,
        cwd=adjudication_repo,
        stdin=prompt,
        timeout_seconds=CALL_TIMEOUT_SECONDS,
    )
    stdout = adjudicator_run.pop("stdout")
    stderr = adjudicator_run.pop("stderr")
    write_bytes(evidence / "adjudicator.stdout.jsonl", stdout)
    write_bytes(evidence / "adjudicator.stderr.log", stderr)
    usage = codex_usage(stdout)
    write_json(
        evidence / "adjudicator-run.json",
        {
            **adjudicator_run,
            **usage,
            "command": sanitized_command(adjudicator_command, policy_sha256),
            "developer_instructions_sha256": policy_sha256,
            "prompt_sha256": file_sha256(evidence / "adjudicator-prompt.txt"),
            "started_from_clean_checkout": True,
        },
    )
    if adjudicator_run["returncode"] != 0 or adjudicator_run["timed_out"]:
        raise ValueError("adjudication call did not complete")
    payload = load_json(final_path)
    normalized = validate_adjudication_payload(payload, findings, adjudication_repo)
    return normalized, {
        "called": True,
        "duration_ms": adjudicator_run["duration_ms"],
        **usage,
        "result_sha256": file_sha256(final_path),
    }


def effective_findings(
    findings: list[dict[str, Any]],
    adjudications: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    values: list[dict[str, Any]] = []
    for finding, adjudication in zip(findings, adjudications, strict=True):
        disposition = adjudication["computed_disposition"]
        effective_priority: int | None
        if disposition == "BLOCK":
            effective_priority = 0 if finding["priority"] == 0 else 1
        elif disposition in {"MANUAL_REVIEW", "ADVISORY"}:
            effective_priority = 2
        else:
            effective_priority = None
        values.append(
            {
                "finding": finding,
                "adjudication": adjudication,
                "effective_priority": effective_priority,
            }
        )
    return values


def run_sample(workspace: pathlib.Path, sample: dict[str, Any]) -> dict[str, Any]:
    sample_id = str(sample["sample_id"])
    evidence = workspace / "evidence" / sample_id
    complete_path = evidence / "complete.json"
    if complete_path.is_file():
        return load_json(complete_path)
    if evidence.exists():
        raise ValueError(f"partial evidence exists for {sample_id}; no retry is allowed")
    evidence.mkdir(parents=True)
    runtime_root = pathlib.Path(tempfile.mkdtemp(prefix=f"{sample_id}-", dir=workspace / "runtime"))
    call_count = 0
    try:
        source = pathlib.Path(str(sample["source_repository"])).resolve(strict=True)
        base = str(sample["base"])
        target = str(sample["target"])
        native_repo = runtime_root / "native-repository"
        clone_target(source, native_repo, base, target)
        native_output = evidence / "native-session"
        native_command = [
            str((workspace / "quality-review").resolve(strict=True)),
            "run-codex",
            "-repo",
            str(native_repo),
            "-base",
            base,
            "-target",
            target,
            "-diff-reason",
            f"blind restricted adjudication {sample_id}",
            "-output-root",
            str(native_output),
            "-model",
            MODEL,
            "-reasoning-effort",
            REASONING_EFFORT,
        ]
        call_count += 1
        # The shipped provider deliberately holds one OS-account-wide lease.
        # Keep native discovery serial while allowing the previous sample's
        # independent adjudication call to overlap on the second worker.
        with NATIVE_CALL_LOCK:
            native_run = model_process(
                native_command,
                cwd=native_repo,
                stdin=None,
                timeout_seconds=CALL_TIMEOUT_SECONDS,
            )
        write_bytes(evidence / "native-run.stdout.log", native_run.pop("stdout"))
        write_bytes(evidence / "native-run.stderr.log", native_run.pop("stderr"))
        write_json(
            evidence / "native-run.json",
            {
                **native_run,
                "command": sanitized_command(native_command),
                "started_from_clean_checkout": True,
            },
        )
        if not native_call_completed(native_run["returncode"], native_run["timed_out"]):
            raise ValueError("native review call did not complete")
        native_result_path = find_single(native_output, "review-result.json")
        native_final_path = find_single(native_output, "native-review.txt")
        native_transcript_path = find_single(native_output, "native-review.stdout.log")
        native_result = load_json(native_result_path)
        native_summary = native_result_summary(native_result)
        findings = native_result["findings"]
        frozen_findings = evidence / "frozen-native-findings.json"
        write_json(frozen_findings, {"findings": findings})

        normalized, adjudicator_record = adjudicate_findings(
            workspace,
            evidence,
            source,
            base,
            target,
            findings,
            runtime_root,
        )
        if findings:
            call_count += 1
        effective_values = effective_findings(findings, normalized)
        effective = {
            "schema_version": 1,
            "profile": PROFILE,
            "sample_id": sample_id,
            "base": base,
            "target": target,
            "native": native_summary,
            "treatment": {
                "predicts_block": any(
                    item["adjudication"]["computed_disposition"] == "BLOCK"
                    for item in effective_values
                ),
                "findings": effective_values,
            },
            "calls": call_count,
            "adjudicator": adjudicator_record,
        }
        write_json(evidence / "effective-result.json", effective)
        completion = {
            "schema_version": 1,
            "sample_id": sample_id,
            "status": "COMPLETE",
            "calls": call_count,
            "native_result_sha256": file_sha256(native_result_path),
            "native_final_sha256": file_sha256(native_final_path),
            "native_transcript_sha256": file_sha256(native_transcript_path),
            "frozen_findings_sha256": file_sha256(frozen_findings),
            "effective_result_sha256": file_sha256(evidence / "effective-result.json"),
            "baseline_predicts_block": native_summary["predicts_block"],
            "treatment_predicts_block": effective["treatment"]["predicts_block"],
            "completed_at": utc_now(),
        }
        write_json(complete_path, completion)
        return completion
    except Exception as error:
        failure = {
            "schema_version": 1,
            "sample_id": sample_id,
            "status": "INCOMPLETE",
            "calls": call_count,
            "error": str(error),
            "completed_at": utc_now(),
        }
        write_json(evidence / "incomplete.json", failure)
        return failure
    finally:
        shutil.rmtree(runtime_root, ignore_errors=True)


def adjudicate_frozen_case(args: argparse.Namespace) -> None:
    source = args.repository.resolve(strict=True)
    findings_source = args.findings.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        raise ValueError("--output must not already exist")
    if git(source, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("frozen case adjudication requires a clean source repository")

    base = git(source, "rev-parse", f"{args.base}^{{commit}}")
    target = git(source, "rev-parse", f"{args.target}^{{commit}}")
    run_checked(["git", "-C", str(source), "merge-base", "--is-ancestor", base, target])
    diff = subprocess.check_output(["git", "-C", str(source), "diff", "--binary", base, target, "--"])
    if not diff:
        raise ValueError("frozen case diff is empty")

    payload = load_json(findings_source)
    if not isinstance(payload, dict) or set(payload) != {"findings"}:
        raise ValueError("frozen findings input must contain only findings")
    findings = payload["findings"]
    if not isinstance(findings, list):
        raise ValueError("frozen findings must be a list")
    native_summary = native_result_summary({"findings": findings})
    for index, finding_value in enumerate(findings):
        if not isinstance(finding_value, dict):
            raise ValueError(f"frozen finding {index} is invalid")
        finding_id = finding_value.get("id")
        if not isinstance(finding_id, str) or not finding_id.startswith("finding-v1:sha256:"):
            raise ValueError(f"frozen finding {index} has no stable ID")

    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = pathlib.Path(tempfile.mkdtemp(prefix=f".{output.name}-", dir=output.parent))
    runtime_root = temporary / "runtime"
    runtime_root.mkdir()
    evidence = temporary / "evidence"
    evidence.mkdir()
    (temporary / "frozen").mkdir()
    repository_root = pathlib.Path(__file__).resolve().parents[1]
    try:
        policy_target = temporary / "frozen" / POLICY_PATH.name
        schema_target = temporary / "frozen" / SCHEMA_PATH.name
        shutil.copyfile(repository_root / POLICY_PATH, policy_target)
        shutil.copyfile(repository_root / SCHEMA_PATH, schema_target)
        frozen_findings = temporary / "frozen" / "native-findings.json"
        write_json(frozen_findings, {"findings": findings})
        manifest = {
            "schema_version": 1,
            "profile": CASE_PROFILE,
            "created_at": utc_now(),
            "source_url": args.source_url,
            "source_repository": str(source),
            "source_clean": True,
            "base": base,
            "target": target,
            "diff_sha256": sha256_bytes(diff),
            "model": MODEL,
            "reasoning_effort": REASONING_EFFORT,
            "call_timeout_seconds": CALL_TIMEOUT_SECONDS,
            "policy_sha256": file_sha256(policy_target),
            "schema_sha256": file_sha256(schema_target),
            "frozen_findings_sha256": file_sha256(frozen_findings),
        }
        write_json(temporary / "manifest.json", manifest)
        normalized, adjudicator_record = adjudicate_findings(
            temporary,
            evidence,
            source,
            base,
            target,
            findings,
            runtime_root,
            require_first_parent=False,
        )
        effective_values = effective_findings(findings, normalized)
        effective = {
            "schema_version": 1,
            "profile": CASE_PROFILE,
            "source_url": args.source_url,
            "base": base,
            "target": target,
            "native": native_summary,
            "treatment": {
                "predicts_block": any(
                    item["adjudication"]["computed_disposition"] == "BLOCK"
                    for item in effective_values
                ),
                "findings": effective_values,
            },
            "calls": 1 if findings else 0,
            "adjudicator": adjudicator_record,
        }
        write_json(temporary / "effective-result.json", effective)
        completion = {
            "schema_version": 1,
            "profile": CASE_PROFILE,
            "status": "COMPLETE",
            "calls": effective["calls"],
            "manifest_sha256": file_sha256(temporary / "manifest.json"),
            "frozen_findings_sha256": file_sha256(frozen_findings),
            "effective_result_sha256": file_sha256(temporary / "effective-result.json"),
            "baseline_predicts_block": native_summary["predicts_block"],
            "treatment_predicts_block": effective["treatment"]["predicts_block"],
            "completed_at": utc_now(),
        }
        write_json(temporary / "complete.json", completion)
        shutil.rmtree(runtime_root, ignore_errors=True)
        os.replace(temporary, output)
        print(json.dumps({"workspace": str(output), **completion}, sort_keys=True))
    except Exception as error:
        shutil.rmtree(runtime_root, ignore_errors=True)
        write_json(
            temporary / "incomplete.json",
            {
                "schema_version": 1,
                "profile": CASE_PROFILE,
                "status": "INCOMPLETE",
                "error": str(error),
                "completed_at": utc_now(),
            },
        )
        os.replace(temporary, output)
        raise


def exact_paired_pvalue(baseline_only_error: int, treatment_only_error: int) -> float:
    discordant = baseline_only_error + treatment_only_error
    if discordant == 0:
        return 1.0
    smaller = min(baseline_only_error, treatment_only_error)
    tail = sum(math.comb(discordant, index) for index in range(smaller + 1)) / (2**discordant)
    return min(1.0, 2 * tail)


def confusion(rows: list[dict[str, Any]], prediction_key: str) -> dict[str, Any]:
    tp = sum(row["label"] == "severe" and row[prediction_key] for row in rows)
    fn = sum(row["label"] == "severe" and not row[prediction_key] for row in rows)
    fp = sum(row["label"] == "normal" and row[prediction_key] for row in rows)
    tn = sum(row["label"] == "normal" and not row[prediction_key] for row in rows)
    return {
        "true_positive": tp,
        "false_negative": fn,
        "false_positive": fp,
        "true_negative": tn,
        "errors": fp + fn,
        "severe_block_retention": tp / (tp + fn) if tp + fn else None,
        "false_block_rate_on_normal": fp / (fp + tn) if fp + tn else None,
        "accuracy": (tp + tn) / len(rows) if rows else None,
    }


def score_rows(rows: list[dict[str, Any]], integrity_ok: bool) -> dict[str, Any]:
    baseline = confusion(rows, "baseline_block")
    treatment = confusion(rows, "treatment_block")
    baseline_only_error = sum(
        (row["baseline_block"] == (row["label"] != "severe"))
        and (row["treatment_block"] == (row["label"] == "severe"))
        for row in rows
    )
    treatment_only_error = sum(
        (row["treatment_block"] == (row["label"] != "severe"))
        and (row["baseline_block"] == (row["label"] == "severe"))
        for row in rows
    )
    pvalue = exact_paired_pvalue(baseline_only_error, treatment_only_error)
    retention_not_worse = (
        treatment["severe_block_retention"] is not None
        and baseline["severe_block_retention"] is not None
        and treatment["severe_block_retention"] >= baseline["severe_block_retention"]
    )
    false_blocks_reduced = treatment["false_positive"] < baseline["false_positive"]
    errors_reduced = treatment["errors"] < baseline["errors"]
    statistically_significant = pvalue < 0.05
    if not integrity_ok:
        decision = "INCOMPLETE"
    elif false_blocks_reduced and errors_reduced and retention_not_worse and statistically_significant:
        decision = "PREFER_RESTRICTED_ADJUDICATION"
    elif false_blocks_reduced and errors_reduced and retention_not_worse:
        decision = "DIRECTIONAL_IMPROVEMENT_INCONCLUSIVE"
    else:
        decision = "KEEP_CURRENT_BEHAVIOR"
    return {
        "baseline": baseline,
        "treatment": treatment,
        "paired": {
            "baseline_wrong_treatment_right": baseline_only_error,
            "baseline_right_treatment_wrong": treatment_only_error,
            "two_sided_exact_pvalue": pvalue,
        },
        "gates": {
            "integrity_ok": integrity_ok,
            "false_blocks_reduced": false_blocks_reduced,
            "total_errors_reduced": errors_reduced,
            "severe_retention_not_worse": retention_not_worse,
            "statistically_significant_at_0_05": statistically_significant,
        },
        "decision": decision,
    }


def initialize(args: argparse.Namespace) -> None:
    source = args.source.resolve(strict=True)
    historical = args.historical_workspace.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        raise ValueError("--output must not already exist")
    output.parent.mkdir(parents=True, exist_ok=True)
    if git(source, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("experiment initialization requires a clean committed source checkout")
    source_commit = git(source, "rev-parse", "HEAD^{commit}")
    operator_path = historical / "operator-manifest.json"
    operator = load_json(operator_path)
    runs = operator.get("runs") if isinstance(operator, dict) else None
    if not isinstance(runs, list) or len(runs) != 30:
        raise ValueError("historical workspace must contain exactly 30 mapped runs")
    labels = [run.get("ground_truth") for run in runs if isinstance(run, dict)]
    if labels.count("severe") != 15 or labels.count("normal") != 15:
        raise ValueError("historical workspace must contain 15 severe and 15 normal changes")

    temporary = pathlib.Path(tempfile.mkdtemp(prefix=f".{output.name}-", dir=output.parent))
    try:
        for directory in ("frozen", "private", "evidence", "runtime"):
            (temporary / directory).mkdir(parents=True)
        binary = temporary / "quality-review"
        run_checked(
            ["go", "build", "-trimpath", "-o", str(binary), "./cmd/quality-review"],
            cwd=source,
        )
        frozen_files: dict[str, dict[str, str]] = {}
        for relative in (SPEC_PATH, POLICY_PATH, SCHEMA_PATH, RUNNER_PATH, TEST_PATH):
            source_path = source / relative
            target_path = temporary / "frozen" / relative.name
            shutil.copyfile(source_path, target_path)
            frozen_files[relative.as_posix()] = {
                "path": f"frozen/{relative.name}",
                "sha256": file_sha256(target_path),
            }

        public_samples: list[dict[str, Any]] = []
        runtime_samples: list[dict[str, Any]] = []
        private_labels: list[dict[str, Any]] = []
        for mapping in runs:
            if not isinstance(mapping, dict):
                raise ValueError("historical run mapping is invalid")
            task = load_json(historical / str(mapping["task"]))
            repository = pathlib.Path(str(task["repository"])).resolve(strict=True)
            base = str(task["base"])
            target = str(task["target"])
            if git(repository, "rev-parse", "HEAD") != target:
                raise ValueError("historical repository target mismatch")
            if git(repository, "rev-parse", f"{target}^1") != base:
                raise ValueError("historical repository first-parent mismatch")
            if git(repository, "status", "--porcelain=v1", "--untracked-files=all"):
                raise ValueError("historical repository is dirty")
            diff = subprocess.check_output(
                ["git", "-C", str(repository), "diff", "--binary", base, target, "--"]
            )
            if not diff:
                raise ValueError("historical repository diff is empty")
            sample_id = "S-" + secrets.token_hex(8)
            public_samples.append(
                {
                    "sample_id": sample_id,
                    "base": base,
                    "target": target,
                    "base_tree": git(repository, "rev-parse", f"{base}^{{tree}}"),
                    "target_tree": git(repository, "rev-parse", f"{target}^{{tree}}"),
                    "diff_sha256": sha256_bytes(diff),
                }
            )
            runtime_samples.append(
                {
                    "sample_id": sample_id,
                    "base": base,
                    "target": target,
                    "source_repository": str(repository),
                }
            )
            private_labels.append(
                {
                    "sample_id": sample_id,
                    "change_id": mapping["change_id"],
                    "project_id": mapping["project_id"],
                    "ground_truth": mapping["ground_truth"],
                    "label_note": mapping["label_note"],
                }
            )

        order = list(range(len(public_samples)))
        secrets.SystemRandom().shuffle(order)
        public_samples = [public_samples[index] for index in order]
        runtime_by_id = {sample["sample_id"]: sample for sample in runtime_samples}
        labels_by_id = {sample["sample_id"]: sample for sample in private_labels}
        runtime_samples = [runtime_by_id[sample["sample_id"]] for sample in public_samples]
        private_labels = [labels_by_id[sample["sample_id"]] for sample in public_samples]

        write_json(temporary / "schedule.json", {"schema_version": 1, "samples": public_samples})
        write_json(
            temporary / "private" / "runtime-map.json",
            {"schema_version": 1, "samples": runtime_samples},
            mode=0o600,
        )
        write_json(
            temporary / "private" / "labels.json",
            {"schema_version": 1, "samples": private_labels},
            mode=0o600,
        )
        codex_version = run_checked(["codex", "--version"])
        manifest = {
            "schema_version": 1,
            "profile": PROFILE,
            "created_at": utc_now(),
            "source_commit": source_commit,
            "source_clean": True,
            "quality_review_sha256": file_sha256(binary),
            "codex_version": codex_version,
            "model": MODEL,
            "reasoning_effort": REASONING_EFFORT,
            "max_workers": MAX_WORKERS,
            "call_timeout_seconds": CALL_TIMEOUT_SECONDS,
            "maximum_calls": 60,
            "sample_count": 30,
            "frozen_files": frozen_files,
            "historical_operator_manifest_sha256": file_sha256(operator_path),
            "schedule_sha256": file_sha256(temporary / "schedule.json"),
            "runtime_map_sha256": file_sha256(temporary / "private" / "runtime-map.json"),
            "labels_sha256": file_sha256(temporary / "private" / "labels.json"),
        }
        write_json(temporary / "manifest.json", manifest)
        preflight = {
            "schema_version": 1,
            "profile": PROFILE,
            "model_calls": 0,
            "ready": True,
            "sample_count": len(public_samples),
            "severe_count": labels.count("severe"),
            "normal_count": labels.count("normal"),
            "manifest_sha256": file_sha256(temporary / "manifest.json"),
            "completed_at": utc_now(),
        }
        write_json(temporary / "preflight.json", preflight)
        os.replace(temporary, output)
    except Exception:
        shutil.rmtree(temporary, ignore_errors=True)
        raise
    print(json.dumps({"workspace": str(output), "model_calls": 0, "ready": True}))


def batch(args: argparse.Namespace) -> None:
    workspace = args.workspace.resolve(strict=True)
    workers = args.workers
    if workers < 1 or workers > MAX_WORKERS:
        raise ValueError(f"--workers must be between 1 and {MAX_WORKERS}")
    manifest = load_json(workspace / "manifest.json")
    if manifest.get("profile") != PROFILE or manifest.get("source_clean") is not True:
        raise ValueError("workspace manifest is invalid")
    if file_sha256(workspace / "schedule.json") != manifest.get("schedule_sha256"):
        raise ValueError("schedule hash mismatch")
    if file_sha256(workspace / "private" / "runtime-map.json") != manifest.get("runtime_map_sha256"):
        raise ValueError("runtime map hash mismatch")
    runtime = load_json(workspace / "private" / "runtime-map.json")
    samples = runtime.get("samples") if isinstance(runtime, dict) else None
    if not isinstance(samples, list) or len(samples) != manifest.get("sample_count"):
        raise ValueError("runtime sample set is invalid")
    results: list[dict[str, Any]] = []
    for sample in samples:
        sample_id = str(sample["sample_id"])
        try:
            result = run_sample(workspace, sample)
        except Exception as error:
            result = {"sample_id": sample_id, "status": "INCOMPLETE", "error": str(error)}
        results.append(result)
        print(json.dumps(result, sort_keys=True), flush=True)
        if result.get("status") != "COMPLETE":
            break
    write_json(
        workspace / "batch-summary.json",
        {
            "schema_version": 1,
            "profile": PROFILE,
            "complete": sum(result.get("status") == "COMPLETE" for result in results),
            "incomplete": sum(result.get("status") != "COMPLETE" for result in results),
            "calls": sum(int(result.get("calls", 0)) for result in results),
            "finished_at": utc_now(),
        },
    )


def verify_workspace(workspace: pathlib.Path, require_complete: bool) -> dict[str, Any]:
    manifest = load_json(workspace / "manifest.json")
    errors: list[str] = []
    if manifest.get("profile") != PROFILE:
        errors.append("profile mismatch")
    for relative, expected in (
        ("schedule.json", manifest.get("schedule_sha256")),
        ("private/runtime-map.json", manifest.get("runtime_map_sha256")),
        ("private/labels.json", manifest.get("labels_sha256")),
    ):
        path = workspace / relative
        if not path.is_file() or file_sha256(path) != expected:
            errors.append(f"hash mismatch: {relative}")
    frozen = manifest.get("frozen_files")
    if not isinstance(frozen, dict):
        errors.append("frozen file manifest is invalid")
        frozen = {}
    for record in frozen.values():
        if not isinstance(record, dict):
            errors.append("frozen file record is invalid")
            continue
        path = workspace / str(record.get("path"))
        if not path.is_file() or file_sha256(path) != record.get("sha256"):
            errors.append(f"frozen file hash mismatch: {path.name}")
    schedule = load_json(workspace / "schedule.json")
    samples = schedule.get("samples") if isinstance(schedule, dict) else None
    if not isinstance(samples, list):
        errors.append("schedule samples are invalid")
        samples = []
    complete = 0
    incomplete = 0
    calls = 0
    for sample in samples:
        sample_id = str(sample.get("sample_id"))
        evidence = workspace / "evidence" / sample_id
        completion_path = evidence / "complete.json"
        if not completion_path.is_file():
            incomplete += 1
            if require_complete:
                errors.append(f"sample incomplete: {sample_id}")
            continue
        completion = load_json(completion_path)
        calls += int(completion.get("calls", 0))
        if completion.get("status") != "COMPLETE" or completion.get("sample_id") != sample_id:
            errors.append(f"completion identity invalid: {sample_id}")
            continue
        effective = evidence / "effective-result.json"
        frozen_findings = evidence / "frozen-native-findings.json"
        if not effective.is_file() or file_sha256(effective) != completion.get("effective_result_sha256"):
            errors.append(f"effective result hash mismatch: {sample_id}")
        if not frozen_findings.is_file() or file_sha256(frozen_findings) != completion.get("frozen_findings_sha256"):
            errors.append(f"frozen finding hash mismatch: {sample_id}")
        prompt = evidence / "adjudicator-prompt.txt"
        if prompt.is_file():
            lowered = prompt.read_text(encoding="utf-8").lower()
            if "ground_truth" in lowered or "label_note" in lowered:
                errors.append(f"label key leaked into prompt: {sample_id}")
        complete += 1
    if calls > int(manifest.get("maximum_calls", 0)):
        errors.append("call ceiling exceeded")
    return {
        "profile": PROFILE,
        "valid": not errors,
        "complete": complete,
        "incomplete": incomplete,
        "calls": calls,
        "errors": errors,
    }


def verify(args: argparse.Namespace) -> None:
    result = verify_workspace(args.workspace.resolve(strict=True), args.require_complete)
    print(json.dumps(result, indent=2, sort_keys=True))
    if not result["valid"]:
        raise SystemExit(1)


def score(args: argparse.Namespace) -> None:
    workspace = args.workspace.resolve(strict=True)
    verification = verify_workspace(workspace, require_complete=True)
    labels_payload = load_json(workspace / "private" / "labels.json")
    labels = {
        str(item["sample_id"]): item
        for item in labels_payload.get("samples", [])
        if isinstance(item, dict)
    }
    schedule = load_json(workspace / "schedule.json")["samples"]
    rows: list[dict[str, Any]] = []
    for sample in schedule:
        sample_id = str(sample["sample_id"])
        completion_path = workspace / "evidence" / sample_id / "complete.json"
        if not completion_path.is_file() or sample_id not in labels:
            continue
        completion = load_json(completion_path)
        rows.append(
            {
                "sample_id": sample_id,
                "change_id": labels[sample_id]["change_id"],
                "project_id": labels[sample_id]["project_id"],
                "label": labels[sample_id]["ground_truth"],
                "baseline_block": bool(completion["baseline_predicts_block"]),
                "treatment_block": bool(completion["treatment_predicts_block"]),
            }
        )
    scoring = score_rows(rows, integrity_ok=bool(verification["valid"]))
    outcome = {
        "schema_version": 1,
        "profile": PROFILE,
        "sample_count": len(rows),
        "verification": verification,
        "scoring": scoring,
        "rows": rows,
        "scored_at": utc_now(),
    }
    write_json(workspace / "outcome.json", outcome)
    baseline = scoring["baseline"]
    treatment = scoring["treatment"]
    paired = scoring["paired"]
    report = f"""# Restricted Adjudication Blind Evaluation Result

Decision: `{scoring['decision']}`

Completed samples: {verification['complete']}/{len(schedule)}

Model calls: {verification['calls']}/{load_json(workspace / 'manifest.json')['maximum_calls']}

Evidence integrity: `{verification['valid']}`

| Metric | Native baseline | Restricted adjudication |
|---|---:|---:|
| False blocks on 15 normal changes | {baseline['false_positive']} | {treatment['false_positive']} |
| Severe changes retained as block | {baseline['true_positive']} | {treatment['true_positive']} |
| False negatives on severe changes | {baseline['false_negative']} | {treatment['false_negative']} |
| Total classification errors | {baseline['errors']} | {treatment['errors']} |

Baseline-wrong/treatment-right pairs: {paired['baseline_wrong_treatment_right']}

Baseline-right/treatment-wrong pairs: {paired['baseline_right_treatment_wrong']}

Two-sided exact paired p-value: `{paired['two_sided_exact_pvalue']:.6g}`

The decision follows the frozen gates in the experiment specification. This reused historical benchmark is an architecture qualification pilot, not a population-level superiority claim and not release authority.
"""
    write_bytes(workspace / "report.md", report.encode())
    print(json.dumps({"decision": scoring["decision"], "outcome": str(workspace / "outcome.json")}, sort_keys=True))


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    initialize_parser = subparsers.add_parser("initialize")
    initialize_parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    initialize_parser.add_argument("--historical-workspace", required=True, type=pathlib.Path)
    initialize_parser.add_argument("--output", required=True, type=pathlib.Path)
    initialize_parser.set_defaults(handler=initialize)

    batch_parser = subparsers.add_parser("batch")
    batch_parser.add_argument("--workspace", required=True, type=pathlib.Path)
    batch_parser.add_argument("--workers", type=int, default=MAX_WORKERS)
    batch_parser.set_defaults(handler=batch)

    verify_parser = subparsers.add_parser("verify")
    verify_parser.add_argument("--workspace", required=True, type=pathlib.Path)
    verify_parser.add_argument("--require-complete", action="store_true")
    verify_parser.set_defaults(handler=verify)

    score_parser = subparsers.add_parser("score")
    score_parser.add_argument("--workspace", required=True, type=pathlib.Path)
    score_parser.set_defaults(handler=score)

    case_parser = subparsers.add_parser("adjudicate-frozen")
    case_parser.add_argument("--repository", required=True, type=pathlib.Path)
    case_parser.add_argument("--base", required=True)
    case_parser.add_argument("--target", required=True)
    case_parser.add_argument("--findings", required=True, type=pathlib.Path)
    case_parser.add_argument("--output", required=True, type=pathlib.Path)
    case_parser.add_argument("--source-url", required=True)
    case_parser.set_defaults(handler=adjudicate_frozen_case)

    args = parser.parse_args()
    args.handler(args)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"error: {error}", file=sys.stderr)
        raise SystemExit(1)
