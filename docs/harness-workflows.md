# Harness Workflows

This file owns Daily Harness Work routing after `harness/repository_verification.py ready`; `docs/harness-workflows.json` contains only the four class IDs. Template Delivery runs separately from the canonical Scaffold Source and exits before this workflow begins. Verification delegates to the exact `harnessctl` version in `harness/harness.lock`.

## Route By Semantic Intent

| Workflow class | Use when | Artifact |
| --- | --- | --- |
| `HARNESS-VERIFICATION-INCIDENT` | Read-only diagnosis, triage, review, or claim verification | Findings and existing evidence; reroute before mutation |
| `HARNESS-FOCUSED-CHANGE` | Exact bounded correction or behavior-preserving refactor with known truth | Focused checklist only when useful; no new specification |
| `HARNESS-SPEC-FIRST-FEATURE` | New or intentionally changed review, policy, schema, provider, plugin, or release semantics | One approved, date-prefixed root `*-spec.md` with specification, plan, and closeout evidence |
| `HARNESS-MAINTENANCE` | Harness, Skill, eval, template, prompt, or process upkeep | Existing maintenance contract or concise checklist; never a self-specification |

Approval, protected paths, security relevance, release intent, and file type are independent controls; they do not choose the workflow. Non-trivial work keeps `workflow_class`, `target_truth`, and `stop_condition` in chat or an owning project artifact. `active_spec` exists only for Spec-first work.

## Verify Proportionally

| Profile | Use |
| --- | --- |
| `change` | Dirty development feedback plus the native project test gate |
| `pull_request` | Clean review candidate plus the native release check |
| `release` | Exact final release SHA plus the native release check |

Run a required full release once per exact final SHA; reuse only evidence accepted by `harnessctl evidence verify`. Repository instructions, approved specs, `CONTEXT.md`, policy, schemas, tests, boundaries, and branch rules remain authoritative. Stop on ambiguity, missing approval, untrusted privileged input, or a failed gate.
