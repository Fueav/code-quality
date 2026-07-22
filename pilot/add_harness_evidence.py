#!/usr/bin/env python3
"""Create commit-bound synthetic Harness evidence for the consumer pilot only."""

from __future__ import annotations

import hashlib
import json
import pathlib
import subprocess
import sys


def canonical(path: pathlib.Path, value: object) -> bytes:
    raw = (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()
    path.write_bytes(raw)
    return raw


def main() -> int:
    if len(sys.argv) != 2:
        raise SystemExit("usage: add_harness_evidence.py <pilot-repository>")
    repo = pathlib.Path(sys.argv[1]).resolve()
    head = subprocess.run(
        ["git", "-C", str(repo), "rev-parse", "HEAD^{commit}"],
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    ).stdout.strip()
    target = repo / ".artifacts" / "change"
    target.mkdir(parents=True, exist_ok=False)
    gate_raw = canonical(target / "gates.json", {"kind": "pilot_fixture", "status": "passed"})
    record = {
        "path": "gates.json",
        "sha256": hashlib.sha256(gate_raw).hexdigest(),
        "size_bytes": len(gate_raw),
    }
    manifest_raw = canonical(
        target / "artifact_manifest.json",
        {"artifacts": [record], "schema_version": 1},
    )
    canonical(
        target / "summary.json",
        {
            "artifact_manifest_sha256": hashlib.sha256(manifest_raw).hexdigest(),
            "artifacts": [record],
            "git": {"head_sha": head},
            "mode": "change",
            "overall": "passed",
            "pilot_fixture": True,
            "schema_version": 1,
        },
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
