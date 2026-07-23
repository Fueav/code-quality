#!/usr/bin/env python3
"""Run one opaque report-only smoke task in a fresh local Codex session."""

from __future__ import annotations

import argparse
import fcntl
import json
import pathlib
import subprocess
import sys
import time


QUALIFICATION_MODEL = "gpt-5.6-terra"
QUALIFICATION_REASONING_EFFORT = "high"
BUILTIN_REVIEW_SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "additionalProperties": False,
    "required": ["schema_version", "findings"],
    "properties": {
        "schema_version": {"const": 1},
        "findings": {
            "type": "array",
            "items": {
                "type": "object",
                "additionalProperties": False,
                "required": ["path", "line", "problem", "impact", "fix"],
                "properties": {
                    "path": {"type": "string", "minLength": 1},
                    "line": {"type": "integer", "minimum": 1},
                    "problem": {"type": "string", "minLength": 1},
                    "impact": {"type": "string", "minLength": 1},
                    "fix": {"type": "string", "minLength": 1},
                },
            },
        },
    },
}


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def command_output(*args: str) -> str:
    completed = subprocess.run(
        list(args),
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:2])}): {completed.stderr.strip()}")
    return completed.stdout.strip()


def codex_metrics(raw: str) -> tuple[int, int]:
    events: list[dict[str, object]] = []
    for line_number, line in enumerate(raw.splitlines(), start=1):
        if not line.strip():
            continue
        value = json.loads(line)
        if not isinstance(value, dict):
            raise ValueError(f"Codex event {line_number} is not an object")
        events.append(value)
    completed = [event for event in events if event.get("type") == "turn.completed"]
    if not completed:
        raise ValueError("Codex output did not contain turn.completed usage metrics")
    usage = completed[-1].get("usage")
    if not isinstance(usage, dict):
        raise ValueError("Codex turn.completed event did not contain usage metrics")
    input_tokens = usage.get("input_tokens")
    output_tokens = usage.get("output_tokens")
    if not isinstance(input_tokens, int) or input_tokens < 0 or not isinstance(output_tokens, int) or output_tokens < 0:
        raise ValueError("Codex usage metrics are invalid")
    return input_tokens, output_tokens


def next_attempt(operator_directory: pathlib.Path) -> int:
    attempts: list[int] = []
    for pattern, prefix in (("attempt-*.metadata.json", "attempt-"), ("builtin-attempt-*.metadata.json", "builtin-attempt-")):
        for path in operator_directory.glob(pattern):
            try:
                attempts.append(int(path.name.removeprefix(prefix).split(".", 1)[0]))
            except ValueError:
                continue
    return max(attempts, default=0) + 1


def codex_command(
    session_root: pathlib.Path,
    extra_writable: pathlib.Path | None = None,
) -> list[str]:
    # The skill's real usage is a human invoking it inside a Claude/Codex
    # session, where the host agent runs `prepare` (git worktree add) in the
    # user's real environment. Pilot must mirror that, not a .git-protecting
    # sandbox, so it bypasses the sandbox for the skill lane only.
    _ = extra_writable
    return [
        "codex",
        "exec",
        "--model",
        QUALIFICATION_MODEL,
        "--config",
        f'model_reasoning_effort="{QUALIFICATION_REASONING_EFFORT}"',
        "--ignore-user-config",
        "--ignore-rules",
        "--dangerously-bypass-approvals-and-sandbox",
        "--skip-git-repo-check",
        "--cd",
        str(session_root),
        "--json",
        "-",
    ]


def builtin_codex_command(repository: pathlib.Path, target: str, schema: pathlib.Path) -> list[str]:
    return [
        "codex",
        "exec",
        "--model",
        QUALIFICATION_MODEL,
        "--config",
        f'model_reasoning_effort="{QUALIFICATION_REASONING_EFFORT}"',
        "--ignore-user-config",
        "--ignore-rules",
        "--sandbox",
        "read-only",
        "--skip-git-repo-check",
        "--cd",
        str(repository),
        "--json",
        "-",
    ]


def builtin_result(raw: str) -> dict[str, object]:
    messages: list[str] = []
    for line in raw.splitlines():
        if not line.strip():
            continue
        event = json.loads(line)
        item = event.get("item") if isinstance(event, dict) else None
        if event.get("type") == "item.completed" and isinstance(item, dict) and item.get("type") == "agent_message":
            text = item.get("text")
            if isinstance(text, str) and text.strip():
                messages.append(text.strip())
    if not messages:
        raise ValueError("built-in review did not produce a final agent message")
    value = json.loads(messages[-1])
    if not isinstance(value, dict) or set(value) != {"schema_version", "findings"} or value.get("schema_version") != 1:
        raise ValueError("built-in review result has an invalid top-level structure")
    findings = value.get("findings")
    if not isinstance(findings, list):
        raise ValueError("built-in review findings must be an array")
    for index, finding in enumerate(findings):
        if not isinstance(finding, dict) or set(finding) != {"path", "line", "problem", "impact", "fix"}:
            raise ValueError(f"built-in review finding {index} has invalid fields")
        path = finding.get("path")
        relative = pathlib.PurePosixPath(path) if isinstance(path, str) else None
        if relative is None or relative.is_absolute() or ".." in relative.parts or not path.strip():
            raise ValueError(f"built-in review finding {index} has an invalid path")
        if not isinstance(finding.get("line"), int) or isinstance(finding.get("line"), bool) or finding["line"] < 1:
            raise ValueError(f"built-in review finding {index} has an invalid line")
        for field in ("problem", "impact", "fix"):
            if not isinstance(finding.get(field), str) or not finding[field].strip():
                raise ValueError(f"built-in review finding {index} has an invalid {field}")
    return value


def run_builtin_review(
    workspace: pathlib.Path,
    task: dict[str, object],
    prompt: str,
    operator_directory: pathlib.Path,
    attempt: int,
    timeout_seconds: int,
    version: str,
) -> dict[str, object]:
    source_repository = pathlib.Path(str(task["repository"])).resolve(strict=True)
    schema = workspace / "builtin-review.schema.json"
    if schema.is_symlink() or not schema.is_file():
        raise ValueError("frozen built-in review schema is invalid")
    worktree = operator_directory / f"builtin-attempt-{attempt}.worktree"
    subprocess.run(
        ["git", "-C", str(source_repository), "worktree", "add", "--quiet", "--detach", str(worktree), str(task["target"])],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    command = builtin_codex_command(worktree, str(task["target"]), schema)
    started_at = time.monotonic()
    try:
        try:
            completed = subprocess.run(
                command,
                cwd=worktree,
                input=prompt,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=timeout_seconds,
                check=False,
            )
            stdout = completed.stdout
            stderr = completed.stderr
            returncode = completed.returncode
        except subprocess.TimeoutExpired as error:
            stdout = error.stdout.decode(errors="replace") if isinstance(error.stdout, bytes) else (error.stdout or "")
            stderr = error.stderr.decode(errors="replace") if isinstance(error.stderr, bytes) else (error.stderr or "")
            returncode = 124
        duration_ms = round((time.monotonic() - started_at) * 1000)
        transcript = operator_directory / f"builtin-attempt-{attempt}.stdout.jsonl"
        error_log = operator_directory / f"builtin-attempt-{attempt}.stderr.log"
        transcript.write_text(stdout, encoding="utf-8")
        error_log.write_text(stderr, encoding="utf-8")
        metadata = {
            "schema_version": 1,
            "run_id": task["run_id"],
            "lane": "builtin",
            "host": "codex",
            "host_version": version,
            "model": QUALIFICATION_MODEL,
            "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
            "attempt": attempt,
            "command": command,
            "returncode": returncode,
            "duration_ms": duration_ms,
            "stdout": transcript.name,
            "stderr": error_log.name,
        }
        metadata_path = operator_directory / f"builtin-attempt-{attempt}.metadata.json"
        metadata_path.write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        if returncode != 0:
            raise ValueError(f"built-in Codex review exited with status {returncode}; see {error_log}")
        if command_output("git", "-C", str(worktree), "status", "--porcelain=v1", "--untracked-files=all"):
            raise ValueError("built-in review modified its read-only worktree")
        input_tokens, output_tokens = codex_metrics(stdout)
        result = builtin_result(stdout)
        result_path = operator_directory / f"builtin-attempt-{attempt}.result.json"
        result_path.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        return {
            "result": result_path,
            "transcript": transcript,
            "runner_metadata": metadata_path,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "duration_ms": duration_ms,
            "worktree": worktree,
        }
    finally:
        removed = subprocess.run(
            ["git", "-C", str(source_repository), "worktree", "remove", "--force", str(worktree)],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        if removed.returncode != 0:
            raise ValueError(f"failed to remove built-in review worktree: {removed.stderr.strip()}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--timeout-seconds", type=int, default=1200)
    args = parser.parse_args()

    if args.timeout_seconds < 1:
        raise ValueError("--timeout-seconds must be positive")
    workspace = args.workspace.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    profile = baseline.get("profile")
    if profile not in {"report_only_smoke", "report_only_historical_pilot"}:
        raise ValueError("workspace profile is not runnable")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot collect formal evidence")
    operator = load_json(workspace / "operator-manifest.json")
    mappings = operator.get("runs")
    if not isinstance(mappings, list):
        raise ValueError("operator manifest is invalid")
    matches = [mapping for mapping in mappings if isinstance(mapping, dict) and mapping.get("run_id") == args.run_id]
    if len(matches) != 1:
        raise ValueError("run ID is unknown or duplicated")
    mapping = matches[0]
    task_relative = mapping.get("task")
    if mapping.get("host") != "codex" or task_relative != f"tasks/codex/{args.run_id}.json":
        raise ValueError("operator task mapping is invalid")
    host = "codex"
    task_path = workspace / str(task_relative)
    task = load_json(task_path)
    prompt_path = task_path.with_suffix(".md")
    if prompt_path.is_symlink() or not prompt_path.is_file():
        raise ValueError("blind task prompt is invalid")
    prompt = prompt_path.read_text(encoding="utf-8")

    session_root = pathlib.Path(str(task.get("output_root"))).resolve()
    if session_root != workspace / "sessions" / args.run_id:
        raise ValueError("blind task session root is invalid")
    repository = pathlib.Path(str(task.get("repository"))).resolve(strict=True)
    if repository.parent != workspace / "repositories":
        raise ValueError("blind task repository is invalid")
    session_root.mkdir(parents=True, exist_ok=True)
    operator_directory = session_root / "operator"
    operator_directory.mkdir(mode=0o700, exist_ok=True)
    completed_directory = "replay-records" if profile == "report_only_smoke" else "observations"
    if (workspace / completed_directory / f"{args.run_id}.json").exists():
        raise ValueError("run already has immutable evidence")

    before_results = set(session_root.glob("**/review-*/output/review-result.json"))
    command = codex_command(session_root, repository)
    version = command_output("codex", "--version")
    expected_version = baseline.get("codex_version")
    if not isinstance(expected_version, str) or version != expected_version:
        raise ValueError(f"{host} version no longer matches the frozen baseline")

    attempt = next_attempt(operator_directory)
    builtin: dict[str, object] | None = None
    if profile == "report_only_historical_pilot":
        builtin_prompt_path = task_path.with_name(f"{args.run_id}.builtin.md")
        if builtin_prompt_path.is_symlink() or not builtin_prompt_path.is_file():
            raise ValueError("blind built-in task prompt is invalid")
        builtin = run_builtin_review(
            workspace,
            task,
            builtin_prompt_path.read_text(encoding="utf-8"),
            operator_directory,
            attempt,
            args.timeout_seconds,
            version,
        )
    started_at = time.monotonic()
    try:
        completed = subprocess.run(
            command,
            cwd=session_root,
            input=prompt,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=args.timeout_seconds,
            check=False,
        )
        stdout = completed.stdout
        stderr = completed.stderr
        returncode = completed.returncode
    except subprocess.TimeoutExpired as error:
        stdout = error.stdout.decode(errors="replace") if isinstance(error.stdout, bytes) else (error.stdout or "")
        stderr = error.stderr.decode(errors="replace") if isinstance(error.stderr, bytes) else (error.stderr or "")
        returncode = 124
    duration_ms = round((time.monotonic() - started_at) * 1000)

    transcript = operator_directory / f"attempt-{attempt}.stdout.jsonl"
    error_log = operator_directory / f"attempt-{attempt}.stderr.log"
    transcript.write_text(stdout, encoding="utf-8")
    error_log.write_text(stderr, encoding="utf-8")
    metadata = {
        "schema_version": 1,
        "run_id": args.run_id,
        "host": host,
        "host_version": version,
        "model": QUALIFICATION_MODEL,
        "reasoning_effort": QUALIFICATION_REASONING_EFFORT,
        "attempt": attempt,
        "command": command,
        "returncode": returncode,
        "duration_ms": duration_ms,
        "stdout": transcript.name,
        "stderr": error_log.name,
    }
    metadata_path = operator_directory / f"attempt-{attempt}.metadata.json"
    metadata_path.write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if returncode != 0:
        raise ValueError(f"{host} exited with status {returncode}; see {error_log}")

    input_tokens, output_tokens = codex_metrics(stdout)
    after_results = set(session_root.glob("**/review-*/output/review-result.json"))
    new_results = after_results - before_results
    if len(new_results) != 1:
        raise ValueError(f"host run produced {len(new_results)} new finalized results; expected exactly one")
    result = new_results.pop()
    collector_name = "qualification_collect.py" if profile == "report_only_smoke" else "historical_pilot_collect.py"
    collector = pathlib.Path(__file__).with_name(collector_name)
    with (workspace / ".collect.lock").open("a+", encoding="utf-8") as collection_lock:
        fcntl.flock(collection_lock.fileno(), fcntl.LOCK_EX)
        collector_command = [
            sys.executable,
            str(collector),
            "--workspace",
            str(workspace),
            "--run-id",
            args.run_id,
            "--result",
            str(result),
            "--transcript",
            str(transcript),
            "--runner-metadata",
            str(metadata_path),
            "--input-tokens",
            str(input_tokens),
            "--output-tokens",
            str(output_tokens),
            "--duration-ms",
            str(duration_ms),
        ]
        if builtin is not None:
            collector_command.extend(
                [
                    "--builtin-result",
                    str(builtin["result"]),
                    "--builtin-transcript",
                    str(builtin["transcript"]),
                    "--builtin-runner-metadata",
                    str(builtin["runner_metadata"]),
                    "--builtin-input-tokens",
                    str(builtin["input_tokens"]),
                    "--builtin-output-tokens",
                    str(builtin["output_tokens"]),
                    "--builtin-duration-ms",
                    str(builtin["duration_ms"]),
                ]
            )
        collected = command_output(*collector_command)
        fcntl.flock(collection_lock.fileno(), fcntl.LOCK_UN)
    json.dump(
        {
            "run_id": args.run_id,
            "host": host,
            "attempt": attempt,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "duration_ms": duration_ms,
            "builtin": {
                "input_tokens": builtin["input_tokens"],
                "output_tokens": builtin["output_tokens"],
                "duration_ms": builtin["duration_ms"],
            }
            if builtin is not None
            else None,
            "collection": json.loads(collected),
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
