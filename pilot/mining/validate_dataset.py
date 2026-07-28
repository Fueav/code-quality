#!/usr/bin/env python3
"""Validate frozen dataset structure and report the preregistered size gates."""

from __future__ import annotations

import argparse
from collections import Counter
import json
from pathlib import Path


CLASSES = {"wrong_code", "missing_safeguard"}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("targets", type=Path)
    parser.add_argument("--allow-unfrozen", action="store_true")
    args = parser.parse_args()

    targets = json.loads(args.targets.read_text())
    if not isinstance(targets, list):
        raise SystemExit("targets root must be an array")
    seen = set()
    for index, target in enumerate(targets):
        key = (target.get("repo"), target.get("introducing_commit"))
        if not all(isinstance(value, str) and value for value in key):
            raise SystemExit(f"target {index}: invalid repo or introducing_commit")
        if key in seen:
            raise SystemExit(f"target {index}: duplicate {key}")
        seen.add(key)
        if target.get("defect_class") not in CLASSES:
            raise SystemExit(f"target {index}: invalid defect_class")
        if not isinstance(target.get("defect_class_basis"), str) or not target["defect_class_basis"]:
            raise SystemExit(f"target {index}: missing defect_class_basis")
        if not isinstance(target.get("max_difficulty"), int) or not 1 <= target["max_difficulty"] <= 5:
            raise SystemExit(f"target {index}: invalid max_difficulty")
        if not isinstance(target.get("defects"), list) or not target["defects"]:
            raise SystemExit(f"target {index}: defects must be non-empty")

    classes = Counter(target["defect_class"] for target in targets)
    summary = {
        "n": len(targets),
        "missing_safeguard": classes["missing_safeguard"],
        "wrong_code": classes["wrong_code"],
        "difficulty_3_plus": sum(target["max_difficulty"] >= 3 for target in targets),
        "gates": {
            "n_at_least_30": len(targets) >= 30,
            "missing_safeguard_at_least_10": classes["missing_safeguard"] >= 10,
            "difficulty_3_plus_at_least_15": sum(target["max_difficulty"] >= 3 for target in targets) >= 15,
        },
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    passed = all(summary["gates"].values())
    return 0 if passed or args.allow_unfrozen else 1


if __name__ == "__main__":
    raise SystemExit(main())
