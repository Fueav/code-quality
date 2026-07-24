# V1 Report-Only Pilot

V1 is a code-quality safety net. It uses the existing 20 bottom-line rules to help a maintainer discover severe problems introduced by a committed change, then publishes evidence and a suggested fix. The maintainer makes the final decision; V1 never changes CI success or blocks a merge.

The report-only pilot has two evidence layers:

1. one blind smoke run for each of the 60 synthetic cases;
2. one blind review of at least 30 human-labeled historical changes from three real projects.

Repeated severe-case runs and exact rule/S/T/E stability are reserved for a future automatic-blocking evaluation.

## Deterministic core

Run from the source checkout:

```bash
quality-review eval --cases evals/cases.json > eval-result.json
```

The deterministic suite verifies:

- 20 rules, each with one positive, counterexample, and insufficient-evidence case;
- Schema, adjudication, downgrade, deduplication, and rendering behavior;
- all rules remain `report_only`.

This proves the ordinary program is internally consistent. It does not measure model usefulness.

## Blind 60-case report-only smoke

The smoke profile uses one local Codex session per case. Every task is opaque and fixed to `gpt-5.6-terra` with `high` reasoning effort. The review Agent receives only the frozen Skill, materialized repository, trusted diff, and its session output root.

The coarse smoke contract is:

- positive: final result is `MANUAL_REVIEW` and at least one finding exists;
- counterexample: final result is `PASS` or `MANUAL_REVIEW`;
- insufficient evidence: final result is `PASS` or `MANUAL_REVIEW`;
- no `INCOMPLETE`, duplicate root, invalid record, or Agent-limit violation;
- exact rule ID and S/T/E equality are recorded but do not gate report-only smoke;
- human confirmation is optional and does not gate smoke completion;
- tokens and duration are required for every run.

Before freezing:

```bash
python3 pilot/qualification_inventory.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest pilot/test_qualification_tools.py
go test ./...
go vet ./...
```

Initialize from a clean commit:

```bash
python3 pilot/qualification_initialize.py \
  --output .code-quality/report-only-smoke-v1
```

Verify the frozen source, binary, Skill, cases, repositories, and opaque schedule:

```bash
python3 pilot/qualification_verify.py \
  --workspace .code-quality/report-only-smoke-v1
```

Run one opaque task:

```bash
python3 pilot/qualification_run.py \
  --workspace .code-quality/report-only-smoke-v1 \
  --run-id <opaque-run-id>
```

Or run the remaining tasks with one to four local workers:

```bash
python3 pilot/qualification_batch.py \
  --workspace .code-quality/report-only-smoke-v1 \
  --workers 4
```

Finally verify every digest, transcript, report, metric, and replay record:

```bash
python3 pilot/qualification_verify.py \
  --workspace .code-quality/report-only-smoke-v1 \
  --write-summary
```

The result is complete only when `report_only_smoke_complete=true`. Evidence is written to `smoke-summary.json` and `evidence-summary.json`. A failure is retained as a failed smoke result; it does not automatically trigger another full matrix.

## Optional synthetic-case audit

Human review is useful for diagnosing a failed case or sampling report quality, but it is not a 60-record admission gate:

```bash
python3 pilot/qualification_review_packet.py \
  --workspace .code-quality/report-only-smoke-v1
```

The operator manifest and review queue contain case identities and must never be shown to a review Agent.

## Real-project historical pilot

After synthetic smoke, select three active projects:

- one involving funds, on-chain behavior, or complex data;
- one ordinary backend service;
- one project with substantial history or weaker test coverage.

Prepare at least 30 immutable, human-labeled changes:

- at least 15 with a confirmed severe issue;
- at least 15 confirmed normal changes;
- one blind Skill review and one isolated ordinary builtin-lane review per change, both using the same model, reasoning effort, base, and target;
- one maintainer decision on whether each review found the core issue and whether each finding-bearing report was actionable.

Summarize:

- Skill versus built-in severe-issue discovery rate and paired core-issue hits;
- Skill versus built-in `MANUAL_REVIEW` finding rates, including the rate on normal changes;
- per-lane report actionability;
- completion and failure rates;
- P50/P95 duration, tokens, and cost;
- missing context and human-overturn reasons.

Freeze the project/change labels in one manifest. Every change must be exactly one first-parent commit with full base and target SHAs. A severe label must name the known core issue and its historical evidence; commit titles alone are not ground truth.

For a concrete Web3 project-selection, defect-taxonomy, and labeling guide, see [`web3-dataset-spec.md`](web3-dataset-spec.md).

Initialize from a clean code-quality commit:

```bash
python3 pilot/historical_pilot_initialize.py \
  --manifest .code-quality/historical-pilot-labels.json \
  --output .code-quality/historical-pilot-v1
```

The initializer builds the frozen CLI, copies the Skill, and shallow-materializes one opaque repository per change containing only the base and target commits. Later fixes and the private labels are unavailable to the review Agent.

Verify the blind schedule before any model call, then run each change through two isolated local Terra/high Codex lanes. The Skill lane uses the frozen code-quality workflow. The builtin lane runs `codex exec` with a generic review prompt and requires machine-readable JSON, without loading the Skill or reading its session. It does not invoke Codex's official `review` subcommand, whose natural-language output is not the pilot's parseable baseline contract:

```bash
python3 pilot/historical_pilot_verify.py \
  --workspace .code-quality/historical-pilot-v1

python3 pilot/qualification_batch.py \
  --workspace .code-quality/historical-pilot-v1 \
  --workers 4
```

Generate the private maintainer queue after all observations are collected:

```bash
python3 pilot/historical_pilot_review_packet.py \
  --workspace .code-quality/historical-pilot-v1
```

The queue supplies one `historical_pilot_review.py` command per paired observation. The maintainer records whether each lane found the known core issue, whether each `MANUAL_REVIEW` report is actionable, and optional per-lane estimated cost. Judgments are immutable and never shown to an Agent.

Finally verify every report, deterministic render, transcript, metric, digest, and maintainer record and write the summary:

```bash
python3 pilot/historical_pilot_verify.py \
  --workspace .code-quality/historical-pilot-v1 \
  --write-summary
```

The summary reports evidence completeness, both lanes' metrics, paired misses, and `caught_up_to_builtin_review`. That conclusion is true only when evidence is complete, the Skill misses no severe issue found by built-in review, its severe discovery is not lower, and its normal-change `MANUAL_REVIEW` rate is not higher. It never enables blocking or makes the maintainers' pilot decision.

These results support the maintainers' decision to start a 2–4 week live report-only trial. V1 does not define an automatic-blocking threshold.

## Optional Harness evidence

`prepare` accepts Harness evidence only when the summary, manifest, target commit, file sizes, and SHA-256 digests agree. Missing, stale, or corrupt evidence never prevents an ordinary Git review; it is recorded as unavailable proof.

## CI publication

The host session supplies validated `review-result.json` and deterministic `review-result.md`. CI runs `quality-review validate` and uploads both files through its trusted artifact channel.

`MANUAL_REVIEW` and `INCOMPLETE` remain report-only semantic values with `ci_action: publish_report`; neither fails CI.

Reference templates:

- `pilot/github-actions-report-only.yml`
- `pilot/gitlab-report-only.yml`
