#!/usr/bin/env python3
"""Run the shipped CLI and normalize current review evidence without model calls of our own."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tempfile
import time


RESULT_SCHEMA = 10
GROUP_FIELDS = ("tool_version", "contract_digest", "provider", "model", "reasoning_effort", "review_scope")
TIMING_FIELDS = ("duration_ms", "process_duration_ms", "freeze_duration_ms")
TOKEN_FIELDS = ("input_tokens", "output_tokens", "cached_input_tokens")
METRIC_FIELDS = TIMING_FIELDS + TOKEN_FIELDS


def read_json(path: Path) -> dict:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected a regular non-symlink JSON file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected a JSON object: {path}")
    return value


def digest(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def persist_exclusive(path: Path, value: dict) -> None:
    """A partial/existing intent is a hold, never permission to start another process."""
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        json.dump(value, stream, sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    directory = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def integer(value: object, minimum: int = 0) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= minimum


def validate_result(result: dict) -> None:
    """Validate consumed fields; the CLI's validate command owns the complete wire contract."""
    if result.get("schema_version") != RESULT_SCHEMA:
        raise ValueError("only native result schema v10 is supported; legacy results need explicit migration")
    for field, prefix in (("review_key", "review-v1"), ("contract_digest", "contract-v1")):
        if not re.fullmatch(prefix + r":sha256:[0-9a-f]{64}", str(result.get(field, ""))):
            raise ValueError(f"invalid {field}")
    if result.get("review_scope") not in ("FULL", "INCREMENTAL"):
        raise ValueError("invalid review_scope")
    request, contract, execution = (result.get(key) for key in ("request", "contract", "execution"))
    if not all(isinstance(value, dict) for value in (request, contract, execution)):
        raise ValueError("request, contract and execution are required")
    if not result.get("current_head") or result["current_head"] != request.get("target_commit"):
        raise ValueError("current head does not match request")
    if contract.get("result_schema_version") != RESULT_SCHEMA or not contract.get("tool_version"):
        raise ValueError("invalid result contract")
    for key, execution_key in (("provider_host", "host"), ("model", "model"), ("reasoning_effort", "reasoning_effort"), ("execution_profile", "execution_profile")):
        if not isinstance(contract.get(key), str) or not contract[key] or contract[key] != execution.get(execution_key):
            raise ValueError(f"execution does not match contract: {key}")
    findings = result.get("findings")
    adjudication = result.get("adjudication")
    if not isinstance(adjudication, dict):
        raise ValueError("adjudication is required")
    outcome = adjudication.get("semantic_result")
    if not isinstance(findings, list) or outcome not in ("PASS", "BLOCK", "ERROR"):
        raise ValueError("invalid native result outcome")
    seen = set()
    for finding in findings:
        if not isinstance(finding, dict) or not re.fullmatch(r"finding-v1:sha256:[0-9a-f]{64}", str(finding.get("id", ""))):
            raise ValueError("invalid finding identity")
        if finding["id"] in seen:
            raise ValueError("duplicate finding identity")
        seen.add(finding["id"])
        if not integer(finding.get("priority")) or finding["priority"] > 3:
            raise ValueError("invalid finding priority")
        if any(not isinstance(finding.get(key), str) or not finding[key].strip() for key in ("title", "reason", "suggestion")):
            raise ValueError("finding content is incomplete")
        location = finding.get("code_location")
        if not isinstance(location, dict):
            raise ValueError("finding location is required")
        path = location.get("path", "")
        if not path or not isinstance(path, str) or PurePosixPath(path).is_absolute() or ".." in PurePosixPath(path).parts or "\\" in path:
            raise ValueError("invalid finding path")
        if not integer(location.get("start_line"), 1) or not integer(location.get("end_line"), location["start_line"]):
            raise ValueError("invalid finding range")
    blockers = sum(finding["priority"] <= 1 for finding in findings)
    if (outcome == "PASS" and blockers) or (outcome == "BLOCK" and not blockers) or (outcome == "ERROR" and findings):
        raise ValueError("findings contradict native gate result")


def adapted_findings(result: dict) -> list[dict]:
    validate_result(result)
    if result["adjudication"]["semantic_result"] == "ERROR":
        raise ValueError("ERROR is an incomplete review, not an adjudication sample")
    return [{
        "finding_id": finding["id"], "priority": finding["priority"],
        "dimension": "unknown", "rule_id": None,
        "code_locations": [{"path": finding["code_location"]["path"], "line": finding["code_location"]["start_line"], "end_line": finding["code_location"]["end_line"]}],
        "production_impact": finding["reason"], "minimal_fix": finding["suggestion"],
        "status": "open", "decisions": [], "independent_confirmation": None,
    } for finding in result["findings"]]


def stage_metrics(session: Path, result: dict) -> list[dict]:
    execution = result["execution"]
    expected = [("NATIVE", 1, session / "output/native-run-metrics.json")]
    count = execution.get("restricted_attempts")
    if not integer(count) or count > 2:
        raise ValueError("invalid restricted attempt count")
    expected += [("RESTRICTED", attempt, session / f"output/restricted-attempts/{attempt:04d}/restricted-adjudication-metrics.json") for attempt in range(1, count + 1)]
    rows = []
    for stage, attempt, path in expected:
        row = {"stage": stage, "attempt": attempt, **{key: None for key in METRIC_FIELDS}}
        if path.exists():
            path = contained(path, session)
            value = read_json(path)
            if value.get("schema_version") != 2 or value.get("stage") != stage or value.get("attempt") != attempt:
                raise ValueError("stage metrics identity mismatch")
            for key in METRIC_FIELDS:
                metric = value.get(key)
                if metric is not None and not integer(metric):
                    raise ValueError(f"invalid metric: {key}")
                row[key] = metric
            row["artifact_sha256"] = digest(path)
            row["artifact_path"] = str(path)
        rows.append(row)
    return rows


def record_from_result(result: dict, session: Path, wall_ms: int | None = None) -> dict:
    validate_result(result)
    contract = result["contract"]
    outcome = result["adjudication"]["semantic_result"]
    return {
        "schema_version": 2, "origin": "production_cli", "status": "COMPLETE" if outcome in ("PASS", "BLOCK") else "FAILED",
        "semantic_result": outcome, "review_key": result["review_key"], "contract_digest": result["contract_digest"],
        "tool_version": contract["tool_version"], "provider": contract["provider_host"],
        "model": contract["model"], "reasoning_effort": contract["reasoning_effort"],
        "review_scope": result["review_scope"], "execution_profile": contract["execution_profile"],
        "session_dir": str(session.resolve()),
        "current_head": result["current_head"], "finding_count": len(result["findings"]),
        "blocking_issues": sum(finding["priority"] <= 1 for finding in result["findings"]),
        "wall_duration_ms": wall_ms, "stage_metrics": stage_metrics(session, result),
    }


def contained(path: Path, root: Path) -> Path:
    boundary = root.resolve(strict=True)
    resolved = path.resolve(strict=True)
    if not path.is_absolute() or not resolved.is_relative_to(boundary):
        raise ValueError("CLI evidence escaped the supplied output root")
    for component in (path, *path.parents):
        if component.resolve() == boundary:
            break
        if component.is_symlink():
            raise ValueError("CLI evidence contains a symlink")
    return resolved


def retained_record(status: dict, boundary: Path, args: argparse.Namespace) -> tuple[dict, Path]:
    """Keep failure telemetry without treating a checkpoint as a validated result."""
    session = contained(Path(status.get("session_dir", "")), boundary)
    checkpoint = read_json(contained(session / "checkpoint.json", boundary))
    plan = checkpoint.get("plan", {})
    contract = plan.get("contract", {})
    if status.get("schema_version") != 1 or checkpoint.get("schema_version") != 1:
        raise ValueError("unsupported retained session schema")
    for key in ("review_key", "contract_digest"):
        if not status.get(key) or plan.get(key) != status[key]:
            raise ValueError("retained session identity mismatch")
    if plan.get("request", {}).get("base_commit") != args.base or plan.get("current_head") != args.target:
        raise ValueError("retained session does not match requested commits")
    if contract.get("result_schema_version") != RESULT_SCHEMA or contract.get("execution_profile") != "production-ci":
        raise ValueError("retained session has an unsupported contract")
    attempts = checkpoint.get("restricted_attempts")
    if not isinstance(attempts, list) or len(attempts) > 2:
        raise ValueError("invalid retained Restricted attempts")
    return {
        "review_key": plan["review_key"], "contract_digest": plan["contract_digest"],
        "tool_version": contract.get("tool_version"), "provider": contract.get("provider_host"),
        "model": contract.get("model"), "reasoning_effort": contract.get("reasoning_effort"),
        "review_scope": plan.get("review_scope"), "execution_profile": contract["execution_profile"],
        "current_head": plan["current_head"], "session_dir": str(session), "session_state": checkpoint.get("state"),
        "stage_metrics": stage_metrics(session, {"execution": {"restricted_attempts": len(attempts)}}),
    }, session


def accept_result(args: argparse.Namespace, record: dict, session: Path, boundary: Path) -> dict:
    result_path = contained(session / "output/review-result.json", boundary)
    validation = subprocess.run([str(args.quality_bin), "validate", str(result_path)], text=True, capture_output=True, check=False)
    if validation.returncode:
        raise ValueError("CLI rejected the frozen native result")
    result = read_json(result_path)
    validate_result(result)
    if result["request"]["base_commit"] != args.base or result["current_head"] != args.target:
        raise ValueError("result does not match requested commits")
    if result["contract"]["execution_profile"] != "production-ci":
        raise ValueError("live result must use production-ci")
    for key in ("review_key", "contract_digest"):
        if record.get(key) and record[key] != result[key]:
            raise ValueError("published result differs from retained session")
    record.update(record_from_result(result, session, record["wall_duration_ms"]))
    record["result_path"] = str(result_path)
    record["result_sha256"] = digest(result_path)
    return result


def run(args: argparse.Namespace) -> int:
    archive = args.archive.resolve()
    archive.mkdir(parents=True, exist_ok=True)
    hold = archive / "operator-required.json"
    retained_pointer = archive / "retained-session.json"
    intent_path = archive / "attempt-intent.json"
    record = {"schema_version": 2, "origin": "production_cli", "status": "FAILED", "semantic_result": None,
              "stage_metrics": [], "wall_duration_ms": None, "repo": args.repo_name, "sha": args.target,
              "reviewed_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")}
    attempt = None
    try:
        if hold.exists() or retained_pointer.exists():
            attempt = Path(tempfile.mkdtemp(prefix="observation-", dir=archive))
            retained, session = retained_record(read_json(hold if hold.exists() else retained_pointer), archive, args)
            record.update(retained)
            if retained["session_state"] != "PUBLISHED" or not (session / "output/review-result.json").exists():
                record["status"] = "WAITING_OPERATOR"
                raise ValueError("retained session requires operator handling; refusing a new Native attempt")
            accept_result(args, record, session, archive)
            if record["status"] != "COMPLETE":
                raise ValueError("ERROR review is excluded from completed samples")
            record["recovered"] = True
            # Multiple invocations and operator idle time have no measured total wall time.
            shutil.copyfile(record["result_path"], args.result_destination)
            if hold.exists():
                hold.replace(retained_pointer)
            return save_record(args, record, attempt)
        if intent_path.exists():
            # Persisted before subprocess launch: an interrupted adapter must never imply
            # that a possibly paid invocation did not happen.
            attempt = Path(tempfile.mkdtemp(prefix="observation-", dir=archive))
            record["status"] = "WAITING_OPERATOR"
            intent = read_json(intent_path)
            if intent.get("schema_version") != 1 or intent.get("base") != args.base or intent.get("target") != args.target:
                raise ValueError("retained attempt intent does not match requested commits")
            output_root = contained(Path(intent.get("output_root", "")), archive)
            completion_path = output_root.parent / "completion.json"
            if completion_path.exists():
                completion = read_json(contained(completion_path, archive))
                if completion.get("schema_version") != 1 or not isinstance(completion.get("exit_code"), int) or isinstance(completion["exit_code"], bool) or not integer(completion.get("wall_duration_ms")):
                    raise ValueError("retained process completion is invalid")
                record.update({key: completion[key] for key in ("exit_code", "wall_duration_ms")})
            summary = read_json(contained(output_root.parent / "stdout.json", archive))
            if summary.get("state", summary.get("status")) in ("RESTRICTED_RETRYABLE", "MANUAL_REQUIRED", "TERMINAL_ERROR"):
                hold.write_text(json.dumps(summary, indent=2) + "\n")
                retained, _ = retained_record(summary, output_root, args)
                record.update(retained)
                raise ValueError("retained session requires operator handling")
            session = contained(Path(summary.get("evidence_dir", "")), output_root)
            result = accept_result(args, record, session, output_root)
            outcome = result["adjudication"]["semantic_result"]
            if record.get("exit_code", {"PASS": 0, "BLOCK": 3, "ERROR": 1}[outcome]) != {"PASS": 0, "BLOCK": 3, "ERROR": 1}[outcome]:
                raise ValueError("retained CLI exit status contradicts frozen result")
            if record["status"] != "COMPLETE" or summary.get("result") != outcome:
                raise ValueError("retained attempt has no valid completed publication")
            record["recovered"] = True
            shutil.copyfile(record["result_path"], args.result_destination)
            return save_record(args, record, attempt)
        config = read_json(args.config)
        command = [str(args.quality_bin), "run-codex", "--repo", str(args.repository),
                   "--base", args.base, "--target", args.target, "--diff-reason", "live-forward-review",
                   "--review-scope", "full", "--execution-profile", "production-ci", "--progress-format", "jsonl"]
        for key, flag in (("model", "--model"), ("reasoning_effort", "--reasoning-effort")):
            if key in config:
                if not isinstance(config[key], str) or not config[key].strip():
                    raise ValueError(f"invalid live config {key}")
                command += [flag, config[key]]
        attempt = Path(tempfile.mkdtemp(prefix="attempt-", dir=archive))
        output_root = attempt / "evidence"
        output_root.mkdir(mode=0o700)
        command += ["--output-root", str(output_root)]
        persist_exclusive(intent_path, {"schema_version": 1, "base": args.base, "target": args.target,
                                        "output_root": str(output_root)})
        started = time.monotonic()
        with (attempt / "stdout.json").open("w") as stdout, (attempt / "stderr.jsonl").open("w") as stderr:
            completed = subprocess.run(command, stdout=stdout, stderr=stderr, check=False)
        record["wall_duration_ms"] = round((time.monotonic() - started) * 1000)
        record["exit_code"] = completed.returncode
        persist_exclusive(attempt / "completion.json", {"schema_version": 1, "exit_code": completed.returncode,
                                                        "wall_duration_ms": record["wall_duration_ms"]})
        summary = read_json(attempt / "stdout.json")
        state = summary.get("state", summary.get("status"))
        if state in ("RESTRICTED_RETRYABLE", "MANUAL_REQUIRED", "TERMINAL_ERROR"):
            hold.write_text(json.dumps(summary, indent=2) + "\n")
            retained, _ = retained_record(summary, output_root, args)
            record.update(retained)
            raise ValueError(f"{state}: retained session requires operator handling")
        session = contained(Path(summary.get("evidence_dir", "")), output_root)
        result = accept_result(args, record, session, output_root)
        outcome = result["adjudication"]["semantic_result"]
        if completed.returncode != {"PASS": 0, "BLOCK": 3, "ERROR": 1}[outcome] or summary.get("result") != outcome:
            raise ValueError("CLI exit status or summary contradicts frozen result")
        if record["status"] != "COMPLETE":
            raise ValueError("ERROR review is excluded from completed samples")
        shutil.copyfile(record["result_path"], args.result_destination)
    except (OSError, ValueError, KeyError, TypeError) as error:
        if isinstance(error, FileExistsError) and error.filename == str(intent_path):
            record["status"] = "WAITING_OPERATOR"
        elif record["status"] != "WAITING_OPERATOR":
            record["status"] = "FAILED"
        record["error"] = str(error)
    return save_record(args, record, attempt)


def save_record(args: argparse.Namespace, record: dict, attempt: Path | None) -> int:
    if attempt is not None:
        record["record_path"] = str(attempt / "record.json")
        (attempt / "record.json").write_text(json.dumps(record, sort_keys=True) + "\n")
    args.record.write_text(json.dumps(record, sort_keys=True) + "\n")
    return 0 if record["status"] == "COMPLETE" else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--quality-bin", type=Path, required=True)
    parser.add_argument("--repository", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--repo-name", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--result-destination", type=Path, required=True)
    parser.add_argument("--record", type=Path, required=True)
    return run(parser.parse_args())


if __name__ == "__main__":
    raise SystemExit(main())
