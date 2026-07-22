#!/usr/bin/env python3
"""Run one opaque qualification task in a fresh Claude Code or Codex session."""

from __future__ import annotations

import argparse
import json
import pathlib
import subprocess
import sys
import time


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


def claude_metrics(raw: str) -> tuple[int, int]:
    payload = json.loads(raw)
    if not isinstance(payload, dict) or payload.get("is_error") is True:
        raise ValueError("Claude Code did not return a successful JSON result")
    usage = payload.get("usage")
    if not isinstance(usage, dict):
        raise ValueError("Claude Code output did not contain usage metrics")
    input_fields = ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")
    input_tokens = sum(value for field in input_fields if isinstance((value := usage.get(field)), int))
    output_tokens = usage.get("output_tokens")
    if input_tokens < 1 or not isinstance(output_tokens, int) or output_tokens < 1:
        raise ValueError("Claude Code usage metrics are invalid")
    return input_tokens, output_tokens


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
    if not isinstance(input_tokens, int) or input_tokens < 1 or not isinstance(output_tokens, int) or output_tokens < 1:
        raise ValueError("Codex usage metrics are invalid")
    return input_tokens, output_tokens


def next_attempt(operator_directory: pathlib.Path) -> int:
    attempts: list[int] = []
    for path in operator_directory.glob("attempt-*.metadata.json"):
        try:
            attempts.append(int(path.name.split("-", 1)[1].split(".", 1)[0]))
        except ValueError:
            continue
    return max(attempts, default=0) + 1


def host_command(
    host: str,
    workspace: pathlib.Path,
    session_root: pathlib.Path,
    binary: pathlib.Path,
    model: str | None = None,
) -> list[str]:
    if host == "claude-code":
        command = [
            "claude",
            "--print",
            "--output-format",
            "json",
            "--no-session-persistence",
            "--safe-mode",
            "--permission-mode",
            "acceptEdits",
            "--add-dir",
            str(workspace / "plugin" / "code-quality"),
            "--allowedTools",
            f"Bash({binary} *),Read,Write,Glob,Grep,Agent",
        ]
        if model:
            command[1:1] = ["--model", model]
        return command
    if host == "codex":
        command = [
            "codex",
            "exec",
            "--ignore-user-config",
            "--ignore-rules",
            "--sandbox",
            "workspace-write",
            "--skip-git-repo-check",
            "--cd",
            str(session_root),
            "--json",
            "-",
        ]
        if model:
            command[2:2] = ["--model", model]
        return command
    raise ValueError(f"unsupported qualification host: {host}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--timeout-seconds", type=int, default=1200)
    parser.add_argument("--claude-model")
    parser.add_argument("--codex-model")
    args = parser.parse_args()

    if args.timeout_seconds < 1:
        raise ValueError("--timeout-seconds must be positive")
    workspace = args.workspace.resolve(strict=True)
    baseline = load_json(workspace / "baseline.json")
    if baseline.get("source_dirty") is not False or baseline.get("development_only") is not False:
        raise ValueError("development or dirty workspace cannot collect qualification evidence")
    operator = load_json(workspace / "operator-manifest.json")
    mappings = operator.get("runs")
    if not isinstance(mappings, list):
        raise ValueError("operator manifest is invalid")
    matches = [mapping for mapping in mappings if isinstance(mapping, dict) and mapping.get("run_id") == args.run_id]
    if len(matches) != 1:
        raise ValueError("run ID is unknown or duplicated")
    mapping = matches[0]
    host = mapping.get("host")
    task_relative = mapping.get("task")
    if host not in {"claude-code", "codex"} or task_relative != f"tasks/{host}/{args.run_id}.json":
        raise ValueError("operator task mapping is invalid")
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
    if (workspace / "replay-records" / f"{args.run_id}.json").exists():
        raise ValueError("run already has replay evidence")

    before_results = set(session_root.glob("review-*/output/review-result.json"))
    binary = workspace / "quality-review"
    if host == "claude-code":
        command = host_command(host, workspace, session_root, binary, args.claude_model)
        version = command_output("claude", "--version")
        expected_version = baseline.get("claude_code_version")
    else:
        command = host_command(host, workspace, session_root, binary, args.codex_model)
        version = command_output("codex", "--version")
        expected_version = baseline.get("codex_version")
    if not isinstance(expected_version, str) or version != expected_version:
        raise ValueError(f"{host} version no longer matches the frozen baseline")

    attempt = next_attempt(operator_directory)
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

    suffix = "json" if host == "claude-code" else "jsonl"
    transcript = operator_directory / f"attempt-{attempt}.stdout.{suffix}"
    error_log = operator_directory / f"attempt-{attempt}.stderr.log"
    transcript.write_text(stdout, encoding="utf-8")
    error_log.write_text(stderr, encoding="utf-8")
    metadata = {
        "schema_version": 1,
        "run_id": args.run_id,
        "host": host,
        "host_version": version,
        "attempt": attempt,
        "returncode": returncode,
        "duration_ms": duration_ms,
        "stdout": transcript.name,
        "stderr": error_log.name,
    }
    metadata_path = operator_directory / f"attempt-{attempt}.metadata.json"
    metadata_path.write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if returncode != 0:
        raise ValueError(f"{host} exited with status {returncode}; see {error_log}")

    input_tokens, output_tokens = claude_metrics(stdout) if host == "claude-code" else codex_metrics(stdout)
    after_results = set(session_root.glob("review-*/output/review-result.json"))
    new_results = after_results - before_results
    if len(new_results) != 1:
        raise ValueError(f"host run produced {len(new_results)} new finalized results; expected exactly one")
    result = new_results.pop()
    collector = pathlib.Path(__file__).with_name("qualification_collect.py")
    collected = command_output(
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
        "--input-tokens",
        str(input_tokens),
        "--output-tokens",
        str(output_tokens),
        "--duration-ms",
        str(duration_ms),
    )
    json.dump(
        {
            "run_id": args.run_id,
            "host": host,
            "attempt": attempt,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "duration_ms": duration_ms,
            "collection": json.loads(collected),
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
