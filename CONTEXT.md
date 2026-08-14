# code-quality v0.5.6 domain context

This file defines the project-specific language used by the v0.5.5 review-scope implementation plus the v0.5.6 integration-contract correction. The approved behavior remains authoritative in `2026-08-14-code-quality-review-scope-and-identity-spec.md` and `2026-08-14-code-quality-v0.5.6-integration-contract-spec.md`.

## Core concepts

- **Review plan**: the immutable, pre-provider decision produced from Git facts, review configuration, and an optional previous result. Its status is either `READY` or `FULL_REQUIRED`.
- **Full PR scope**: the committed change owned by the current PR direction. For ref-based and PR/MR discovery this is `merge-base(base-tip, head)..head`; for legacy exact commits it remains the caller-provided `base..target` range.
- **Provider scope**: the range shown to one native provider invocation. It equals the full PR scope for `FULL` and `previous-head..current-head` for `INCREMENTAL`.
- **Review contract**: the stable tool, result-schema, provider-output-schema, prompt, rubric, provider, model, reasoning-effort, and execution-profile facts that determine how a review is performed.
- **Review identity**: `review_key`, a versioned SHA-256 identity computed before provider execution from normalized scope, contract, goal, and lineage facts.
- **Finding identity**: a versioned SHA-256 identity computed from normalized finding content. It is independent of provider output order and session paths.
- **Previous blocker**: a P0/P1 finding in the immutable parent result. Every previous blocker must receive exactly one `RESOLVED` or `UNRESOLVED` resolution in an incremental provider response.
- **Current findings**: unresolved previous blockers plus findings newly discovered in the incremental delta. They alone determine the current `PASS` or `BLOCK` result.
- **External result envelope**: company-CI metadata that states `EXECUTED`/`REUSED` and `CURRENT`/`SUPERSEDED` without modifying the immutable CLI result.

## Invariants

1. `FULL`, `INCREMENTAL`, `EXECUTED`, `REUSED`, `CURRENT`, and `SUPERSEDED` are not one state machine. Scope, source, and lifecycle remain separate dimensions.
2. A `FULL_REQUIRED` plan is not a review result and cannot be represented as `PASS`, `BLOCK`, or `ERROR`.
3. The provider is never invoked before the plan is `READY`; `FULL_REQUIRED` always reports `provider_invocations=0`.
4. A plan records both full PR scope and provider scope. Incremental execution narrows only the provider scope; it does not redefine the PR.
5. The current head checkout is detached and read-only in production CI. Codex Exec and Claude Code do not receive branch-selection arguments.
6. All changed-file lists are clean repository-relative paths, sorted, and unique.
7. `review_key`, `contract_digest`, and finding IDs exclude time, random IDs, output paths, temporary directories, and session paths.
8. Incremental review is allowed only when repository, canonical base/head refs, base tip, goal, review contract, and parent lineage still match and the previous head is a strict ancestor of the current head.
9. Previous P2/P3 findings are not individually carried forward. P0/P1 findings cannot disappear without one validated resolution.
10. The CLI is read-only with respect to the reviewed repository, Git history, CI, pull requests, and remote state.

## Ownership boundaries

- `internal/reviewplan` owns Git discovery, ref normalization, full/incremental range construction, previous-result admission, `READY`/`FULL_REQUIRED`, and pre-provider identity inputs.
- `quality` owns versioned wire types, canonical hashing, finding identity, provider-response classification, final-result validation, and summary rendering.
- `internal/nativereview` owns the Codex/Claude invocation contracts, frozen evidence lifecycle, one provider call, and result publication.
- `internal/session` owns the detached current-head checkout and materialized provider-range diff.
- Harness owns fix/test/commit/review loops.
- Company CI owns trusted reuse, attestation, compare-and-swap publication, and lifecycle envelopes.
