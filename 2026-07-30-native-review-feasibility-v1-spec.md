# Native Review Goal-mode Feasibility v1

Status: execution contract
Baseline source: `aa8bdf838b550281565af509b48631710751ea70`
Admission authority: `evals/native-review-admission.json`

## Question

On a small balanced set of semantically admitted fixtures, does the thin goal-mode product preserve or improve native review quality without reintroducing the legacy 20-rule reasoning workflow?

This is a feasibility gate, not a population estimate.

## Frozen sample

Use four matched positive/counterexample pairs, one from each V1 dimension:

- D1: `DES-004-positive`, `DES-004-counterexample`
- D2: `COR-004-positive`, `COR-004-counterexample`
- D3: `REL-004-positive`, `REL-004-counterexample`
- D4: `SEC-001-positive`, `SEC-001-counterexample`

Every case must remain qualified by the admission authority. Models receive only an opaque repository, its committed base/target history, and the lane input. They do not receive case IDs, kinds, expected roots, private facts, this specification, or prior outputs.

## Lanes

Both lanes use standalone Codex CLI `0.145.0`, `gpt-5.6-sol`, `high` reasoning, a fresh isolated clone, a unique `CODEX_HOME` containing only authentication, `--ignore-user-config`, `--ignore-rules`, `--ephemeral`, and read-only execution.

- Lane A — official native baseline: one `codex exec review --commit HEAD` call with no custom review prompt.
- Lane B — thin goal mode: current frozen `quality-review run-codex` with a separately frozen, non-label-leaking change goal. The product may use one native discovery call and, only when candidates exist, one candidate-only verifier call.

Lane A has a ceiling of 8 model calls. Lane B has a ceiling of 16. The experiment ceiling is 24. There are no retries hidden from the evidence record.

## Scoring

Freeze all raw outputs before adjudication. Within each case, hide lane identity and shuffle the two normalized outputs before human judgment.

- Positive passes when at least one retained finding identifies the admitted introduced root cause and a concrete impact. Additional valid findings are allowed.
- Counterexample passes when no actionable introduced defect is reported.
- A malformed or incomplete run fails its lane for that case.
- If a counterexample output reveals a genuine defect missed by admission, block the benchmark case and do not count it as a model false positive.
- Rule ID, exact finding count, severity taxonomy, S/T/E, prose similarity, duration, and token use are not accuracy gates.

## Expansion gate

Expand to the 40 admitted automatic cases only when all conditions hold:

1. all 16 sessions finish and preserve their raw inputs, outputs, exit status, model, reasoning effort, and call count;
2. Lane B passes at least three of four positives;
3. Lane B passes at least three of four counterexamples;
4. Lane B is not worse than Lane A on positive passes or counterexample passes;
5. no benchmark case becomes blocked during adjudication; and
6. source, protocol, admission, fixtures, model, binary, and session order hashes match the frozen manifest.

A passing gate authorizes only the 40-case comparison. It does not prove statistical superiority or authorize a release, push, tag, or deployment.

## Workflow

Use the repository's spec-first Harness route: freeze this contract and a machine manifest; add RED-to-GREEN deterministic preflight tests; commit the clean preflight; execute the fixed session order; freeze raw evidence; adjudicate without lane identity; then publish a specification audit and residual risks.
