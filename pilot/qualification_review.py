#!/usr/bin/env python3
"""Promote one immutable pending observation after independent human review."""

from __future__ import annotations

import argparse
import fcntl
import json
import pathlib
import subprocess
import sys


def load_json(path: pathlib.Path) -> dict[str, object]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"expected regular file: {path}")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", required=True, type=pathlib.Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--status", required=True, choices=("confirmed", "overturned"))
    parser.add_argument("--reviewer", required=True)
    parser.add_argument("--note", required=True)
    parser.add_argument("--overturn-reason")
    args = parser.parse_args()

    if args.status == "overturned" and not args.overturn_reason:
        raise ValueError("overturned review requires --overturn-reason")
    if args.status == "confirmed" and args.overturn_reason:
        raise ValueError("confirmed review must not have --overturn-reason")
    if not args.reviewer.strip() or not args.note.strip():
        raise ValueError("reviewer and note must be non-empty")
    workspace = args.workspace.resolve(strict=True)
    record = load_json(workspace / "replay-records" / f"{args.run_id}.json")
    human = record.get("human_review")
    if not isinstance(human, dict) or human.get("status") != "pending":
        raise ValueError("only a pending replay record may receive a human decision")
    evidence = load_json(workspace / "run-evidence" / f"{args.run_id}.json")
    session = workspace / str(evidence["session"])
    collector = pathlib.Path(__file__).with_name("qualification_collect.py")
    command = [
        sys.executable,
        str(collector),
        "--workspace",
        str(workspace),
        "--run-id",
        args.run_id,
        "--result",
        str(session / "output" / "review-result.json"),
        "--transcript",
        str(workspace / str(evidence["transcript"])),
        "--runner-metadata",
        str(workspace / str(evidence["runner_metadata"])),
        "--input-tokens",
        str(evidence["input_tokens"]),
        "--output-tokens",
        str(evidence["output_tokens"]),
        "--duration-ms",
        str(evidence["duration_ms"]),
        "--human-status",
        args.status,
        "--reviewer",
        args.reviewer,
        "--review-note",
        args.note,
    ]
    if args.overturn_reason:
        command.extend(["--overturn-reason", args.overturn_reason])
    with (workspace / ".collect.lock").open("a+", encoding="utf-8") as collection_lock:
        fcntl.flock(collection_lock.fileno(), fcntl.LOCK_EX)
        completed = subprocess.run(command, check=False, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        fcntl.flock(collection_lock.fileno(), fcntl.LOCK_UN)
    if completed.returncode != 0:
        raise ValueError(f"human review collection failed: {completed.stderr.strip()}")
    sys.stdout.write(completed.stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
