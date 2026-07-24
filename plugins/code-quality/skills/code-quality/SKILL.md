---
name: code-quality
summary: Run an ordinary report-only review focused on V1.1 production-floor defects.
description: Use when asked to review a committed change for material correctness, data, reliability, security, or compatibility defects under the V1 report-only policy.
---

# Code Quality

Use the current Claude Code or Codex session as the Agent runtime; never configure or launch a separate model backend.

0. Ensure the `quality-review` CLI is available (`quality-review version`). If it is missing, install the matching release and confirm `~/.local/bin` is on PATH:
   `curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh`
1. Run `quality-review prepare --host claude-code` in Claude Code, or `quality-review prepare --host codex` in Codex. Pass explicit `--base`, `--target`, and `--diff-reason` only when the user supplied that baseline.
2. Follow the returned `workflow_path` exactly. Review the git worktree under `repository_dir` with your native tools. Write only the returned `main_review_path`; do not modify the worktree or any input file.
3. Run `quality-review finalize --session <session_dir>` once. It returns `COMPLETE` or `INCOMPLETE`.
4. Report the semantic result and final JSON/Markdown paths. V1 never changes CI success.
