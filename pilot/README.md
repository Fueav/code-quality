# V1 Report-Only Pilot

The pilot validates the report-only V1 release candidate and the current Claude Code/Codex host-session workflow. It does not claim that a remote tag or release artifact exists, embed a model runner, configure a provider, or turn semantic results into CI failures.

## Deterministic qualification

Run from the code-quality source checkout:

```bash
quality-review eval --cases evals/cases.json > eval-result.json
```

Expected V1 evidence:

- 60 cases: one positive, counterexample, and insufficient-evidence case for each of 20 rules;
- all 60 deterministic adjudications pass;
- every severe positive is adjudicated three times with stable output;
- all policy rules remain `report_only`.

This proves schema/adjudication coverage. It does not prove model recall or precision.

## Host-session replay

For each selected case, create a real Git repository with base and target commits matching the fixture. From an authenticated Claude Code or Codex session:

1. Invoke the `code-quality` Skill.
2. Follow the CLI-returned workflow and finish `prepare` / main review / optional batch verifier / `finalize`.
3. Record the validated result:

```bash
quality-review replay record \
  --cases evals/cases.json \
  --case-id DES-003-positive \
  --host claude-code \
  --run-number 1 \
  --result <session>/output/review-result.json \
  --human-status pending \
  > replay-records/DES-003-positive.claude-code.1.json
```

4. After a person reviews the evidence, regenerate the record with `--human-status confirmed`, or use `--human-status overturned --overturn-reason <reason>`.
   When the host exposes usage metrics, pass `--input-tokens`, `--output-tokens`, and `--duration-ms` together; partial metric sets are rejected.
5. Aggregate progress:

```bash
quality-review replay summarize \
  --cases evals/cases.json \
  --records replay-records \
  > replay-summary.json
```

`qualification_complete` becomes true only after all 60 cases are covered, every severe positive has three stable runs, S/T/E and rule IDs match expectations, no duplicate root causes are present, every record respects the two-Agent limit, both Claude Code and Codex have valid replay evidence, and human review confirms every run. Missing token or duration metrics remain `null` and do not count as available.

## Optional Harness evidence

`prepare` scans only `.artifacts/change`, `.artifacts/candidate`, and `.artifacts/release`. It copies evidence only when:

- `summary.json` is passed schema version 1 evidence for that mode;
- `git.head_sha` exactly equals the review target commit;
- `artifact_manifest_sha256` matches the manifest bytes;
- summary and manifest artifact records are identical and sorted;
- every artifact is a regular non-symlink file with matching size and SHA-256.

Missing Harness evidence leaves `sources` empty. Stale or corrupt evidence appears under `rejected` and does not prevent an ordinary repository review.

## Representative pilot evidence

The Claude Code representative pilot covers one DES-003 positive, counterexample, and insufficient-evidence case plus an ordinary compatibility case with optional Harness evidence. It intentionally remains below full qualification.

The positive fixture evolved through three evidence shapes before the retained verifier confirmation:

1. a cardinality constant without a real caller did not prove reachability;
2. a declared schedule interval without an executable scheduler/deadline did not prove production impact;
3. the final fixture closed the path through a real `package main` entry, a five-minute context deadline, 600000 serial calls with a code-defined 10ms minimum success latency, propagated deadline failure, and deterministic nonzero exit.

Only the final confirmed verifier decision is retained in the canonical evidence package. The earlier decisions cannot be independently replayed from the saved artifacts, so they are historical context rather than evidence of verifier refutation behavior. The retained session proves only that the final closed chain passed the batch-verifier contract; it does not prove that all 20 rules are model-qualified.

Canonical local pilot evidence is generated under `.code-quality/pilot-run-v4/` and remains untracked. Its `pilot-summary.json` uses paths relative to that directory and can be checked with `python3 pilot/verify_evidence.py --quality-review <binary>`. Replay records use `human_review.status: pending`. Because its repository and case paths expose the expected case type, this package is a canary only and does not count toward the final blind qualification matrix.

## Blind full qualification

Follow the project-specific [qualification execution plan](../2026-07-22-code-quality-v1-qualification-execution-plan.md). Before freezing a qualification workspace, run:

```bash
python3 pilot/qualification_inventory.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest pilot/test_qualification_tools.py
```

From a clean frozen commit, initialize the untracked blind workspace:

```bash
python3 pilot/qualification_initialize.py \
  --output .code-quality/qualification-v1
```

The initializer builds the pinned CLI, copies the frozen Skill, materializes 60 opaque Git repositories, and emits 100 balanced Claude Code/Codex task files. `operator-manifest.json` contains expected identities and must never be given to a review Agent. A workspace initialized with `--allow-dirty-development` is smoke-test-only and cannot become qualification evidence.

Verify the frozen source, digests, opaque schedule, task prompts, materialized Git trees, and deterministic matrix before any model run:

```bash
python3 pilot/qualification_verify.py \
  --workspace .code-quality/qualification-v1
```

Run one opaque task in a fresh non-persistent host session and collect its result as `pending` human review:

```bash
python3 pilot/qualification_run.py \
  --workspace .code-quality/qualification-v1 \
  --run-id <opaque-run-id>
```

The runner gives the host only its opaque task, frozen Skill, repository, and session output root. It records the host transcript, wall duration, host-reported input/output tokens, finalized result hashes, deterministic Markdown hash, and replay record. A successful model run is still not human-confirmed.

After an independent person inspects the session inputs, model output, optional verifier decision, and expected mapping, promote the same immutable observation without rerunning the model:

```bash
python3 pilot/qualification_collect.py \
  --workspace .code-quality/qualification-v1 \
  --run-id <opaque-run-id> \
  --result <session>/output/review-result.json \
  --transcript <session-root>/operator/<host-output> \
  --input-tokens <count> \
  --output-tokens <count> \
  --duration-ms <count> \
  --human-status confirmed \
  --reviewer <person> \
  --review-note <evidence-checked>
```

Use `--human-status overturned --overturn-reason <reason>` with the same reviewer fields when the observation is wrong. A completed human decision cannot be overwritten. Fix the underlying fixture, policy, or workflow and generate a new frozen qualification workspace instead of rewriting failed evidence.

## CI publication

The host session supplies validated `review-result.json` and deterministic `review-result.md`. CI then runs `quality-review validate` and uploads both files. `BLOCK` and `INCOMPLETE` remain published semantic results with `ci_action: publish_report`; they do not fail CI. A malformed or semantically inconsistent JSON report fails validation. V1 validation is not a signature or provenance system, so the host-session job must hand the report to publication through the CI platform's trusted artifact channel and render Markdown from the validated JSON.

Reference examples:

- `pilot/github-actions-report-only.yml`
- `pilot/gitlab-report-only.yml`

The examples are templates only. V1 does not modify a consuming repository's CI configuration automatically.
