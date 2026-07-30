# Native Review Goal-mode Feasibility v2

Status: implementation contract

Supersedes only the failed v1 feasibility gate. The v1 specification, evidence, judgments, outcome, and report remain immutable historical evidence.

## Objective

Repeat the same bounded eight-case comparison only after correcting the two causes isolated by v1:

1. the goal-mode adapter rejected a valid native candidate section headed `Full review comments:`; and
2. DES-004 and COR-004 counterexamples contained unrelated actionable performance defects and were not valid negatives.

This iteration tests those corrections. It must not add a runtime checklist, private PASS marker, label-bearing prompt, extra review phase, retry, or 40-case execution.

## Product correction

Native candidate parsing accepts exactly the observed heading grammar `^(?:Review|Full review) comments?:$`. All accepted headings feed the same strict finding parser.

The adapter must continue to reject:

- empty output;
- an empty candidate section;
- orphan or malformed `[P0]` through `[P3]` headers;
- a no-findings result followed by any accepted candidate heading or finding; and
- unexpected unindented content inside a candidate section.

The retained v1 S04 native response is the primary deterministic replay fixture. No model call is required to prove this correction.

## Counterexample corrections

### DES-004 counterexample

Base and target expose the same authority and cache interfaces plus an identical repository-visible cache contract.

- `Authority.Version` is a cheap coherence read and does not fetch the authoritative decision.
- Equal versions identify the same decision snapshot.
- A matching cache hit returns the cached decision without calling `Authority.Current`.
- A cache miss or version mismatch synchronously returns `Authority.Current`.

Behavioral tests must prove the hit and fallback call paths. The target must preserve the exported API present in the base.

### COR-004 counterexample

Base and target retain an identical outbox contract. The target must:

- commit the order and outbox event under one database lock without cloning historical orders or events;
- keep placement work independent of historical order count;
- reconcile only bounded snapshots outside the database lock;
- delete an outbox event only after delivery;
- make repeated event-keyed delivery idempotent; and
- drain persisted events across bounded batches.

Behavioral tests must prove atomic visibility, stable map identity across placement, the batch bound, draining, and idempotent retry.

## Frozen comparison

Use the same four matched pairs and opaque IDs as v1:

- D1: DES-004 positive and corrected counterexample;
- D2: COR-004 positive and corrected counterexample;
- D3: REL-004 positive and counterexample;
- D4: SEC-001 positive and counterexample.

The product receives only the opaque repository history and the same non-label-leaking change goal. Expected roots and adjudication labels remain outside all model sessions.

Both lanes use standalone Codex CLI `0.145.0`, `gpt-5.6-sol`, `high` reasoning, read-only execution, fresh isolated clones, a unique authentication-only `CODEX_HOME`, `--ignore-user-config`, `--ignore-rules`, and `--ephemeral`.

- Baseline: one official `codex exec review --commit HEAD` call with no custom prompt.
- Goal mode: one native discovery call and, only when candidates exist, one candidate-only verifier call.

Maximum calls remain 8 baseline plus 16 goal-mode, 24 total. No retry is allowed.

## RED-to-GREEN and zero-model gate

Before implementation, observe focused failures for the retained heading replay and both corrected counterexample contracts. After implementation:

- focused adapter and fixture tests pass;
- the 60-case admission inventory is ready with refreshed cases and fixture hashes;
- the v2 protocol audit freezes source, specification, admission, fixtures, runtime, goals, and session order;
- the product binary hash and version match the manifest;
- all eight fixtures materialize as deterministic two-commit repositories; and
- preflight records zero model calls.

Commit the clean product and fixture correction before freezing the v2 protocol manifest and preflight.

## Blind scoring

Freeze every raw session before adjudication. Randomize lane order per case, freeze judgments before revealing the lane map, and apply the same coarse rules as v1:

- positive passes only when a finding states the introduced root and concrete impact;
- counterexample passes only with no actionable introduced finding;
- malformed or incomplete execution fails;
- a newly discovered genuine counterexample defect blocks that case rather than counting as a false positive.

## Expansion gate

Authorize the 40-case comparison only if all conditions hold:

1. all 16 sessions complete with retained execution evidence;
2. goal mode passes at least three of four positives and three of four counterexamples;
3. goal mode is not worse than the baseline by kind;
4. no case is blocked during blind adjudication;
5. all frozen hashes match; and
6. the 24-call ceiling is respected with no hidden retry.

A passing result authorizes only the later 40-case comparison. It does not authorize release, push, tag, deployment, or claims of population-level superiority.
