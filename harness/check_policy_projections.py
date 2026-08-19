#!/usr/bin/env python3
"""Reject duplicated or stale Harness policy projections."""

import json, os, sys
from pathlib import Path

root = Path(os.environ.get("HARNESS_PROJECT_ROOT", Path(__file__).resolve().parent.parent))
owner = (root / "docs/harness-workflows.md").read_text(encoding="utf-8")
workflow_ids = json.loads((root / "docs/harness-workflows.json").read_text(encoding="utf-8"))["workflow_classes"]
projections = ("AGENTS.md", "BLUEPRINT.md", "README.md", "CONTRIBUTING.md", "docs/ai-tools/README.md")
retired = ("specs/harness_workflows/", "check-harness-workflows", "Loop-first", "`observe`", "`focused`", "`governed`")
errors = []
for workflow_id in workflow_ids:
    if owner.count(f"`{workflow_id}`") != 1: errors.append(f"docs/harness-workflows.md must own {workflow_id} exactly once")
for relative in projections:
    text = (root / relative).read_text(encoding="utf-8")
    if "docs/harness-workflows.md" not in text: errors.append(f"{relative} must point to docs/harness-workflows.md")
    for value in workflow_ids + list(retired):
        if value in text: errors.append(f"{relative} duplicates or retains {value}")
if errors: print("\n".join(f"policy_projections: {error}" for error in errors), file=sys.stderr); raise SystemExit(1)
print("policy_projections: passed")
