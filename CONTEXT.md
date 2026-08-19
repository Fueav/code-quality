# code-quality v0.5.8 domain context

This file defines the project-specific language used by the review-scope, integration-contract, restricted-adjudication, and stage-recovery implementation. The approved behavior remains authoritative in `2026-08-14-code-quality-review-scope-and-identity-spec.md`, the historical v0.5.6 contract spec, `2026-08-14-code-quality-v0.5.7-restricted-adjudication-spec.md`, and `2026-08-19-code-quality-v0.5.8-restricted-resume-spec.md`.

## Core concepts

- **Review plan**: the immutable, pre-provider decision produced from Git facts, review configuration, and an optional previous result. Its status is `READY`, `FULL_REQUIRED`, or `MANUAL_REQUIRED`.
- **Full PR scope**: the committed change owned by the current PR direction. For ref-based and PR/MR discovery this is `merge-base(base-tip, head)..head`; for legacy exact commits it remains the caller-provided `base..target` range.
- **Provider scope**: the range shown to native discovery. It equals the full PR scope for `FULL` and `previous-head..current-head` for `INCREMENTAL`.
- **Review contract**: the stable tool, result-schema, provider-output-schema, prompt, rubric, restricted policy/schema, provider, model, reasoning-effort, and execution-profile facts that determine how a review is performed.
- **Review identity**: `review_key`, a versioned SHA-256 identity computed before provider execution from normalized scope, contract, goal, and lineage facts.
- **Finding identity**: a versioned SHA-256 identity computed from normalized finding content. It is independent of provider output order and session paths.
- **Frozen native candidate**: a P0/P1 emitted by native discovery and retained in read-only raw evidence before any public result is produced.
- **Restricted adjudication**: the optional stage that judges only frozen P0/P1 candidates under the V1.2 production floor. Trusted code recomputes retention from its factual fields; the stage has at most two independent attempts without creating a new review round.
- **Recovery checkpoint**: the owner-only, digest-bound state that freezes review identity, input/contract artifacts, Native evidence/outcome, Restricted attempt ledger, and publication state.
- **Stage attempt**: one Provider process in Native or Restricted. Native has exactly one attempt; Restricted has at most an initial attempt and one formal resume attempt.
- **Previous blocker**: a P0/P1 finding in the immutable parent result. Every previous blocker receives one `RESOLVED`, `UNRESOLVED`, or post-adjudication `DISMISSED` resolution.
- **Current findings**: advisories plus unresolved/new P0/P1 candidates that survive restricted adjudication. They alone determine the public `PASS` or `BLOCK` result.
- **External result envelope**: company-CI metadata that states `EXECUTED`/`REUSED` and `CURRENT`/`SUPERSEDED` without modifying the immutable CLI result.

## Invariants

1. `FULL`, `INCREMENTAL`, `EXECUTED`, `REUSED`, `CURRENT`, and `SUPERSEDED` are not one state machine. Scope, source, and lifecycle remain separate dimensions.
2. Plan-level `FULL_REQUIRED`/`MANUAL_REQUIRED` and session-level `MANUAL_REQUIRED` are not review results and cannot be represented as `PASS`, `BLOCK`, or `ERROR`.
3. The provider is never invoked before the plan is `READY`; both terminal plan statuses report `provider_invocations=0`.
4. A plan records both full PR scope and provider scope. Incremental execution narrows only the provider scope; it does not redefine the PR.
5. The current head checkout is detached and read-only in production CI. Codex Exec and Claude Code do not receive branch-selection arguments.
6. All changed-file lists are clean repository-relative paths, sorted, and unique.
7. `review_key`, `contract_digest`, and finding IDs exclude time, random IDs, output paths, temporary directories, and session paths.
8. Incremental review is allowed only when repository, canonical base/head refs, base tip, goal, review contract, and parent lineage still match and the previous head is a strict ancestor of the current head.
9. Native discovery runs exactly once. Restricted adjudication runs only when Native produced P0/P1 and has at most two attempts, so total Provider attempts are between one and three.
10. A candidate blocks only when trusted code verifies the exact conjunction defined by `policy/v1.2/restricted-adjudication.md`; no other artifact redefines that floor.
11. Rejected candidate prose is absent from public result, Markdown, and summary. A retryable Restricted failure publishes no result; terminal/manual state fails closed as external `ERROR/HOLD` and never falls back to Native priority.
12. An automatic lineage is at most `FULL → INCREMENTAL`. A further INCREMENTAL returns `MANUAL_REQUIRED` before session creation; callers must not auto-fallback to FULL.
13. Previous P2/P3 findings are not individually carried forward. P0/P1 findings cannot disappear without one validated resolution or restricted `DISMISSED` record.
14. Resume validates every bound artifact and the local target object before any Provider call, rebuilds an exact detached checkout without fetching, and never reruns Native.
15. The CLI is read-only with respect to the reviewed repository, Git history, CI, pull requests, and remote state.

## Ownership boundaries

- `internal/reviewplan` owns Git discovery, ref normalization, full/incremental range construction, previous-result admission, `READY`/`FULL_REQUIRED`/`MANUAL_REQUIRED`, and pre-provider identity inputs.
- `quality` owns versioned wire types, canonical hashing, finding identity, native classification, restricted filtering, final-result validation, and summary rendering.
- `internal/nativereview` owns the Codex/Claude invocation contracts, recovery state machine, attempt ledger, locks, frozen evidence lifecycles, and result publication.
- `internal/session` owns the owner-controlled session layout, detached current-head checkout, materialized provider-range diff, and exact target checkout rebuild.
- Harness owns fix/test/commit/review loops.
- Company CI owns persistent session/object-store indexing, distributed CAS, trusted reuse, attestation, compare-and-swap publication, and lifecycle envelopes.

## Harness handoff language

- **Scaffold Source**: the canonical `ai-first-go-template` repository root from which one Template Delivery begins.
- **Target Repository**: this repository while it receives the Repository Contract and retains ownership of all Code Quality facts and extensions.
- **Template Delivery**: one explicit bootstrap or upgrade that begins in the Scaffold Source and ends after this clean Target Repository proves convergence.
- **Repository Contract**: the target-local instructions, workflows, profiles, gates, locks, and verification interfaces required for Daily Harness Work.
- **Daily Harness Work**: normal product, incident, maintenance, release, and deployment work after Template Delivery.
- **Suite Conformance**: release evidence that exact Scaffold Source, Harness Template Sync, and Harness Driven Development revisions honor the same handoff contract.
