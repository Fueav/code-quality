---
name: code-quality
summary: Run a Codex-native report-only review of one committed change.
description: Use when asked to review a committed change for actionable defects with an optional change goal.
---

# Code Quality

Run the product's thin native-review path; do not reproduce its review logic in the host prompt.

1. Run `quality-review run-codex --repo <repo>`. Add `--goal <intent>` only when the user supplied change intent or a specific concern; add the baseline flags only when the user supplied all three.
2. The CLI resolves and isolates the committed scope, invokes one native `codex exec review`, and runs one candidate-only verifier only when findings exist.
3. Report the returned status, semantic result, model-call count, verifier status, and final report paths. Never delete the session directory or reports. The review never changes CI success.
