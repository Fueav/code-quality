# Native Review Evaluation Admission Specification

Status: approved locked evaluation contract
Scope: evidence required before any future quality comparison; no model execution or product prompt change.

## Decision

The 60 legacy synthetic cases remain the offline V1.2 rule inventory and regression fixtures. They are not automatic accuracy labels and cannot authorize or block the native-review architecture.

This replaces the earlier attempt to qualify handcrafted counterexamples as universal clean negatives. A fixture test can prove named properties, but it cannot prove that no other actionable defect exists.

## Benchmark authority

A future A/B may be scored only against a benchmark assembled independently of the product treatment:

- Positive samples come from real historical defects whose introducing change, accepted fix, root cause, and material impact are independently evidenced.
- Negative samples come from real reviewed and merged changes with repository-visible contracts and tests. They represent the best available clean evidence, not proof of universal defect absence.
- Product prompts and sessions receive only the repository, committed scope, and a separately frozen non-label-leaking goal.
- Expected roots, labels, lane identity, and adjudication material remain outside every model session.

## Sample states

Every sample is frozen in exactly one state before scoring:

- `qualified`: evidence is sufficient for the declared expected behavior.
- `ambiguous`: more than one reasonable verdict remains or a reviewer finds a plausible previously unknown defect.
- `excluded`: provenance, materialization, isolation, or label evidence is invalid.

Only `qualified` samples enter accuracy totals. `ambiguous` and `excluded` samples remain visible in the audit but cannot count as product failures or wins.

## Separation of gates

- Product conformance is deterministic: exact scope, isolated checkout, one native call, optional-goal prompt boundary, native-text retention, trusted adaptation, and report-only publication.
- Model quality is statistical: paired fresh sessions, identical model and reasoning, frozen lane order, blind adjudication, and accepted finding-level outcomes.
- Fixing product code cannot change a frozen benchmark. Fixing benchmark evidence requires a new benchmark version and a fresh qualification audit before any model calls.

## Decision rule

The baseline is the official native review. The treatment is the same native review with only an explicitly supplied goal.

A treatment is preferred only when the frozen paired comparison shows a meaningful increase in accepted true findings without an increase in accepted false positives. A tie or inconclusive result keeps native review as the default and the goal as an optional user feature.

## Admission acceptance

- Benchmark provenance and repository materialization are reproducible from frozen identifiers.
- No synthetic counterexample is used as a universal clean label.
- A qualification audit is committed before model execution.
- Lane mapping remains sealed until raw evidence and blind judgments are frozen.
- Call ceilings, session isolation, model, reasoning, and no-retry rules are fixed before execution.
- No result authorizes release, push, tag, deployment, or a population-level superiority claim by itself.
