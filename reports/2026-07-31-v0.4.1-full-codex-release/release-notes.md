# code-quality v0.4.1

v0.4.1 restores the complete native Codex review path and keeps the product report-only.

## What changed

- Runs exactly one ordinary `codex exec` with the user's normal ChatGPT authentication, configuration, rules, Skills, tools, Git context, workspace write access, and shell network access. Defaults are `gpt-5.6-sol` with `max` reasoning. A per-user operating-system lease prevents nested or concurrent duplicate reviews without environment injection, PID tracking, process inspection, or stale crash markers; this intentionally permits one active review per system user.
- Freezes the final message, JSONL event stream, and stderr before classification. Present artifacts are published read-only with SHA-256 evidence; paths recorded as absent are revalidated before the freeze manifest is published.
- Uses document-level deterministic classification. Only an exact supported no-findings document becomes `PASS`; every other nonempty native response becomes `MANUAL_REVIEW`, with frozen `native-review.txt` as the authority. Failed or empty native output becomes `INCOMPLETE`.
- Removes the product-side CommonMark finding parser and code-location adapter instead of extending them for additional output formats. The schema remains version 3, and `run-codex` leaves compatibility fields `findings` and `adapter_drops` empty.
- Keeps CI behavior unchanged: reports are published, merges are never blocked, and source repositories are not modified.

## Scope

This release is qualified for Codex only. Claude Code compatibility remains deferred and is not part of the v0.4.1 capability claim.

## Upgrade note

Consumers should treat `native-review.txt` as the authoritative review whenever the result is `MANUAL_REVIEW`; they must not expect v0.4.1 to reconstruct native findings into structured JSON.
