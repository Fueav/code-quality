# v0.5.7

This release keeps native Codex Exec / Claude Code review as the discovery layer and adds a deterministic production-floor gate before P0/P1 findings become release blockers.

## What changed

- Freeze native review evidence first, then send only frozen P0/P1 candidates to one restricted adjudication call using the same provider, model, and reasoning effort.
- Inject the V1.2 production-floor policy as trusted Codex developer instructions or a Claude system prompt; the adjudication call is always read-only and ignores host customizations.
- Recompute blocker retention in ordinary code from supported severity, reachability, evidence, change causality, concrete trigger, and complete causal-chain facts. Model recommendations do not decide the gate.
- Silently remove P0/P1 candidates that do not meet the floor from public JSON, Markdown, and summaries while retaining raw frozen evidence for audit.
- Fail closed with `ERROR/HOLD` when restricted adjudication or its evidence protocol is invalid.
- Stop automatic review after `FULL → INCREMENTAL`; a further incremental request returns `MANUAL_REQUIRED`, zero provider invocations, and exit code 5 before creating a session.

## Contracts

- Native result schema advances to v9 with one-or-two provider invocations and the `DISMISSED` incremental resolution.
- Prompt contract advances to v3.
- Company CI envelope v2 wraps schema-v9 results; envelope v1 and schema v8 remain immutable.
- Existing P2/P3 findings remain non-blocking advisories and do not trigger restricted adjudication.
