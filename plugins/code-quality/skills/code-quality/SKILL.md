---
name: code-quality
summary: Run a full-capability, single-call Codex report-only review of one committed change.
description: Use when asked to review a committed change for actionable defects, optionally with a user-supplied goal.
---

# Code Quality

Use the product's full Codex discovery path; do not reproduce review logic in the host prompt.

Before considering a requested `--repo`, first check whether `CODE_QUALITY_NATIVE_DISCOVERY_MARKER` points to the regular product marker, then resolve the current working tree's Git root and check `../.code-quality-native-discovery-child-v1`. If either check identifies the active discovery child, do not invoke `quality-review`; review the requested change directly with the current Codex tools. This prevents only self-recursion, including after `chdir`, and leaves every other Skill and tool available.

1. Run `quality-review run-codex --repo <repo>`. Add `--goal <intent>` only when the user supplied change intent or a specific concern. When the user supplies both endpoints, pass `--base <base> --target <target>`; add `--diff-reason <reason>` only when the user supplied that reason. Never pass only one endpoint.
2. The CLI resolves and isolates the committed scope, invokes exactly one full `codex exec` with the normal Codex configuration, tools, and Skills, then freezes the raw output before deterministic classification. The optional goal is user context, not a review boundary.
3. Report status, semantic result, model-call count, raw freeze path, metrics path, and final report paths. Never delete the session directory or reports. The review never changes CI success.
