# Native Review Evaluation Admission Specification

Status: implementation baseline
Baseline: `2117b95`
Scope: admission of the existing 60 synthetic cases to a future native-review comparison; no model execution or product prompt change.

## Decision

The 60 legacy cases remain the offline V1.2 rule inventory, but they are not all automatic accuracy labels for the simplified native-review product.

- `positive`: admitted only when the materialized repository proves at least one actionable defect introduced by the target. One matching root cause is sufficient; additional valid findings are allowed.
- `counterexample`: admitted only when the materialized repository proves the changed behavior is safe and the target introduces no unrelated actionable defect.
- `insufficient`: retained for human calibration but excluded from automatic pass/fail scoring because the expected result depends on unresolved facts by construction.

Exact rule ID, S/T/E fields, and finding count remain diagnostic metadata. They are not vNext accuracy gates.

## Evidence boundary

The review Agent may use only the materialized base, target, exact diff, and a separately frozen non-label-leaking goal. Private `evals/cases.json` facts explain the human label but cannot repair evidence absent from the repository.

A case is not admitted when any of these holds:

- a label-critical business, deployment, scale, ownership, or lifecycle fact exists only in private metadata;
- stable pre-existing evidence contradicts or is missing from one side without the change intentionally introducing it;
- the fixture metadata materially disagrees with the checked-in source or contract;
- a counterexample changes a public surface or behavior unrelated to the safety property being tested;
- more than one reasonable automatic verdict remains after reading all repository-visible evidence.

Target-only evidence is allowed only when that evidence is itself part of the reviewed change. Stable contracts and deployment facts must otherwise be byte-identical in base and target.

## Frozen outputs

`evals/native-review-admission.json` is the machine admission authority. It must freeze the SHA-256 of `evals/cases.json` and the complete fixture tree, cover every case exactly once, and classify cases as:

- `qualified`: eligible for a later bounded native-review comparison;
- `human_only`: retained but not automatically scored;
- `blocked`: excluded until its recorded defect is repaired and re-audited.

The expected clean closeout is 40 qualified cases (`20 positive + 20 counterexample`), 20 human-only `insufficient` cases, and zero blocked cases.

## Acceptance

- A RED contract test precedes the validator and manifest implementation.
- Every fixture remains a reviewable committed base/target pair.
- Known collateral counterexample changes and metadata/source mismatches are repaired.
- The admission validator, fixture tests, all Go tests, `go vet`, `go build`, Python suites, and `git diff --check` pass.
- The audit report records per-rule human conclusions and residual uncertainty.
- No model call, large A/B run, push, tag, release, or deployment occurs in this step.
