# Code Quality AI Architecture Blueprint

This repository uses a compact Harness contract around the `quality-review` CLI. Review semantics, schemas, policy, evidence, and release behavior remain project-owned.

## Repository Map

| Layer | Source of truth | Mechanical proof |
| --- | --- | --- |
| Repository guidance | `AGENTS.md`, `CONTEXT.md`, `.ai-boundaries.yml`, branch rules | Boundary, symlink, and Git-state checks |
| Task routing | `docs/harness-workflows.md` | `docs/harness-workflows.json` and repository-module tests |
| Review semantics | Approved root `*-spec.md` files and `CONTEXT.md` | Go/Python tests and contract fixtures |
| Policy and wire contracts | `policy/v1.2/`, `schemas/` | Validation, compatibility, and adjudication tests |
| Product implementation | `cmd/quality-review`, `internal/`, `quality/` | Native `make test` and `make release-check` |
| Delivery handoff | Suite contract, scaffold manifest/lock, delivery and readiness modules | Scaffold audit, `ready`, and pinned `harnessctl` evidence |

## AI Architecture Constitution

- `AGENTS.md` is a thin entry surface; this Blueprint, `CONTEXT.md`, approved specs, policy, schemas, tests, and public docs own unique facts and evidence.
- Skills route to repository authority. Repository modules expose delivery and verification interfaces while `harness/harness.lock` pins the external engine.
- Unknown project content stays target-owned. Native tests, release checks, plugin packaging, and CI contracts remain independently visible gates.
- Relocate valid facts before slimming; post-delivery Harness or prompt maintenance is net-zero or net-negative and creates no self-referential process artifact.

## Safety And Verification

- `.ai-boundaries.yml` fails closed. Workflow class, approval, security relevance, release intent, and changed paths remain independent decisions.
- Dirty development uses the change profile; clean candidates and releases compare against an explicit integration baseline.
- Harness calls the native test and release gates without changing Code Quality's read-only review contract or Provider-invocation rules.
- Template Delivery records semantic resolutions and ends only after a clean scaffold audit plus `harness/repository_verification.py ready`.

## Template Boundary

`ai-first-go-template` supplies the shared Repository Contract and verification interfaces. Code Quality retains its root specification convention, domain glossary, policy, schemas, release workflow, plugin surfaces, and evidence corpus. After delivery, daily product and Harness work uses Harness Driven Development and does not call Template Sync.
