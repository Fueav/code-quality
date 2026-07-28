---
name: code-quality
summary: Run an ordinary report-only review focused on V1.1 production-floor defects.
description: Use when asked to review a committed change for material correctness, data, reliability, security, or compatibility defects under the V1 report-only policy.
---

# Code Quality

Use the current Claude Code or Codex session as the Agent runtime; never configure or launch a separate model backend.

1. Before `prepare`, explain that it prefers a detached worktree (writing `.git/worktrees`) but falls back to a session-local shared clone without writing repository Git metadata when the host rejects that write; cleanup removes only that checkout. Get explicit approval. Do not run `prepare` until the user approves; stop if approval is denied or unavailable.
2. After approval, use the host's permission mechanism to run `quality-review prepare --host claude-code` in Claude Code or `quality-review prepare --host codex` in Codex. Pass `--base`, `--target`, and `--diff-reason` only when the user supplied that baseline.
3. Follow `workflow_path` exactly. Review `repository_dir` with native tools, write only `main_review_path`, and never modify the review worktree or inputs.
4. Run `quality-review finalize --session <session_dir>`; if it returns `REREVIEW_REQUIRED`, independently review again, write only `next_review_path`, then finalize once more. Never delete the session directory or either final report; cleanup is limited to the CLI-managed detached worktree.
5. Report the returned status, semantic result, review rounds, and final report paths. V1 never changes CI success.
