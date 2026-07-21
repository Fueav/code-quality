---
name: code-quality
summary: Review a committed Git increment for V1.1 production-floor defects.
description: Use when asked to run the code-quality review, check a branch or pull request for production-floor defects, or produce the V1 report-only quality result.
---

# Code Quality

Use the current Claude Code or Codex session as the Agent runtime; never configure or launch a separate model backend.

1. Run `quality-review prepare` in the repository. Pass explicit `--base`, `--target`, and `--diff-reason` only when the user supplied that baseline.
2. Follow the returned `workflow_path` exactly. Write only the returned main/verifier output paths; do not modify repository files or trusted input artifacts.
3. Continue calling `quality-review finalize --session <session_dir>` as directed by its machine-readable status until it returns `COMPLETE` or `INCOMPLETE`.
4. Report the semantic result and final JSON/Markdown paths. `BLOCK` remains report-only in V1.
