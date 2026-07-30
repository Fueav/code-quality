---
name: code-quality
summary: Run a single-call Codex-native report-only review of one committed change.
description: Use when asked to review a committed change for actionable defects, optionally with a user-supplied goal.
---

# Code Quality

Use the product's native-review path; do not reproduce review logic in the host prompt.

1. Run `quality-review run-codex --repo <repo>`. Add `--goal <intent>` only when the user supplied change intent or a specific concern; add baseline flags only when the user supplied all three.
2. The CLI resolves and isolates the committed scope, then invokes exactly one native `codex exec review`; the optional goal is a focus, not a review boundary.
3. Report status, semantic result, model-call count, and final report paths. Never delete the session directory or reports. The review never changes CI success.
