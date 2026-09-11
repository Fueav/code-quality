# PROJECT CONTRACT

This repository ships the `quality-review` CLI and its Codex and Claude Code plugins. Correctness, auditability, read-only review semantics, evidence integrity, and release safety outrank convenience.

## Commands

Use `make build`, `make test`, `make release-check`, `make verify-change`, `make verify-candidate`, `make verify-release`, and `make verify-suite`.

## Repository Authority

- Keep AI entry surfaces thin. `BLUEPRINT.md` owns the AI architecture, `CONTEXT.md` owns review-domain language and invariants, `docs/harness-workflows.md` owns verification and completion, and `docs/branch-collaboration.md` owns branch/worktree collaboration.
- Approved, date-prefixed root `*-spec.md` files own intentional product semantics and their implementation evidence. Tests and schemas follow those contracts rather than raw chat.
- `policy/v1.2/` owns the production floor; `schemas/` owns wire contracts; `README.md` and `docs/` own public onboarding and CI integration guidance.
- `cmd/quality-review` is the CLI boundary. `internal/reviewplan`, `internal/nativereview`, `internal/session`, and `quality` retain the ownership recorded in `CONTEXT.md`.
- Commits use Conventional Commits.

## Safety

- The CLI remains read-only with respect to reviewed repositories, Git history, CI, pull requests, and remote state.
- Do not invoke a Provider before the review plan is `READY`, weaken the restricted production floor, publish rejected blocker prose, or turn `FULL_REQUIRED`/`MANUAL_REQUIRED` into review results.
- Do not extend an automatic review lineage beyond `FULL → INCREMENTAL`; remaining blockers return to a human.
- Do not commit credentials, login state, raw Provider secrets, private evidence roots, or release signing material.
- Public result schemas, policy semantics, provider contracts, installer/plugin behavior, and release gates require tests and owner review.
- Do not bypass formatting, vet, native tests, boundary checks, repository contracts, or release verification.

## Verification

- New or changed Go behavior needs meaningful regression tests. Schema or wire changes need compatibility tests; policy or prompt changes need focused eval regression evidence.
- Preserve the native `make release-check` gate. Harness wraps it as a project-owned gate rather than replacing it.
- Use `make verify-change` on dirty work, `make verify-candidate` for a clean review candidate, and `make verify-release` once for an exact final release SHA. Always set a resolvable `VERIFY_COMPARE_REF`.
- Changes classified by `.ai-boundaries.yml` as approval-required need explicit owner approval evidence.

## AI Boundaries

`@.ai-boundaries.yml` is authoritative. `CLAUDE.md` must remain a symlink to this file.
