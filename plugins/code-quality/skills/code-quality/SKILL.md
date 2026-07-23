---
name: code-quality
summary: Run an ordinary report-only review focused on V1.1 production-floor defects.
description: Use when asked to review a committed change for material correctness, data, reliability, security, or compatibility defects under the V1 report-only policy.
---

# Code Quality

Use the current Claude Code or Codex session as the Agent runtime; never configure or launch a separate model backend.

1. Run `quality-review prepare --host claude-code` in Claude Code, or `quality-review prepare --host codex` in Codex. Pass explicit `--base`, `--target`, and `--diff-reason` only when the user supplied that baseline.
2. Follow the returned `workflow_path` exactly. Write only the returned main-review output path; do not modify repository files or trusted input artifacts.
3. Continue calling `quality-review finalize --session <session_dir>` as directed by its machine-readable status until it returns `COMPLETE` or `INCOMPLETE`.
4. Report the semantic result and final JSON/Markdown paths. V1 never changes CI success.
