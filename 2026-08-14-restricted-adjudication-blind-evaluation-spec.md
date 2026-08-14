# Restricted Adjudication Blind Evaluation

Status: owner-authorized isolated experiment contract

Source baseline: `v0.5.6@1e8ae39455d050ccf7a851523d958e3e91fae6db`

## Decision question

After one unchanged native `run-codex` review is frozen, does a second, candidate-only adjudication call using the historical V1.1 production-floor contract reduce erroneous release blocks without reducing retention of independently labeled severe changes?

This experiment is explicitly authorized to run without a delivered Harness checkout. It does not authorize a release, push, tag, deployment, or production gate change.

## Architecture under test

The native review remains the discovery source. The treatment receives only its frozen findings and the same target-reachable repository state. It may inspect evidence for those findings but may not discover, add, split, merge, or repair findings.

The treatment instruction is injected as Codex `developer_instructions`, the highest caller-controlled instruction role exposed by the installed CLI. Repository content, diffs, comments, and raw finding prose are untrusted data and cannot change the adjudication policy.

The model records evidence-bearing facts for each frozen finding:

- validity: supported, contradicted, or insufficient;
- consequence severity: S1, S2, or S3;
- trigger confidence: T1, T2, or T3;
- evidence level: E1, E2, or E3;
- change attribution, concrete trigger, complete causal chain, and non-style status;
- repository-relative evidence references and unresolved uncertainties.

Ordinary code, not the model, computes the effective disposition. A finding blocks only when all V1.1 terms hold:

```text
validity == SUPPORTED
AND severity == S3
AND trigger_confidence == T3
AND evidence_level IN (E2, E3)
AND introduced_or_worsened_by_change == true
AND trigger_condition_is_concrete == true
AND causal_chain_is_complete == true
AND finding_is_not_style_preference == true
```

All other supported findings are advisory or manual review. Contradicted findings are rejected. The raw finding and raw priority remain immutable.

## Frozen benchmark

Use the existing historical-pilot materialization containing 30 independently labeled, one-commit changes from three projects:

- 15 changes labeled `severe` with repository-visible historical evidence;
- 15 accepted remediation changes labeled `normal`;
- one fresh native call per change;
- at most one adjudication call per change, only when native discovery returned findings.

The historical labels, known roots, accepted fixes, sibling outputs, and change identities remain outside every model prompt and target-reachable Git history. Public schedules use fresh opaque sample IDs. Raw native and adjudication evidence is frozen before scoring opens the private label map.

This is a reused historical benchmark, not an untouched confirmatory population. It can qualify the architecture for a live report-only trial but cannot prove population-level superiority.

## Runtime contract

- native lane: the exact `quality-review run-codex` implementation built from the frozen source;
- adjudication lane: `codex exec` with the same `gpt-5.6-sol` model and `max` reasoning;
- adjudication sandbox: read-only, ephemeral, ignored user config and rules, disabled hooks;
- maximum parallel samples: 2;
- native discovery calls remain serialized by the shipped OS-account lease; only one prior sample's adjudication may overlap the next native call;
- maximum calls: 60 total, 30 native plus at most 30 adjudication;
- timeout: 1,200 seconds per call;
- no retry, resume, or same-sample replacement;
- every final response, JSONL transcript, stderr, command contract, duration, token usage when available, and SHA-256 digest is retained.

## Blindness and integrity

Before the first model call:

1. freeze this specification, policy, schema, runner, tests, source commit, binary digest, Codex version, schedule, call ceiling, and model settings;
2. prove every sample repository contains the target and its first parent, has no later accepted fix, has a clean checkout, and has a non-empty diff;
3. freeze an opaque public schedule and a private label map;
4. run deterministic tests and a zero-model preflight.

Each sample uses fresh disposable clones for native discovery and adjudication. Model prompts contain no ground truth, known root, label note, accepted fix, or sibling result. A partial or malformed call is retained as `INCOMPLETE` and is never retried.

## Scoring

At change level:

- baseline predicts block when the raw native result contains any P0 or P1;
- treatment predicts block when deterministic V1.1 adjudication retains any `BLOCK`;
- `severe` is the expected blocking class;
- `normal` is the expected non-blocking class, subject to an ambiguity audit if a genuinely new severe defect is evidenced.

Report both product and statistical effects:

- false-block rate on normal changes;
- severe-change block retention;
- total classification errors;
- baseline-only and treatment-only errors;
- exact paired McNemar/binomial p-value on discordant errors;
- model calls, completion, duration, and tokens.

The treatment is preferred in this pilot only if:

1. it produces fewer false blocks and fewer total errors;
2. it does not reduce severe-change block retention;
3. the two-sided exact paired error test is below `0.05`;
4. every execution and blind-evidence integrity check passes.

A directional improvement that misses the statistical gate is reported as promising but inconclusive. A tie, regression, integrity failure, or loss of severe-change retention keeps the current behavior.

## PR 16 case study

After the blind benchmark is frozen and scored, the four known review rounds from `moss-site/agent_marketplace#16` may be replayed as a labeled case study. They do not enter the 30-change statistical denominator because they motivated the treatment and are not blind holdout data.
