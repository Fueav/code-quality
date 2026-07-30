# Code Quality Native Review vNext Specification

Status: approved implementation baseline
Scope: the default product review path only; the V1.2 rubric and historical evaluators remain offline assets.

## Product contract

`quality-review run-codex` reviews one committed `base..target` increment in an isolated target checkout and publishes a report-only result. It does not modify the reviewed repository or CI status.

The model-facing flow is deliberately small:

1. Deterministically resolve `base`, `target`, changed files, and a trusted diff.
2. Invoke `codex exec review` once in native review mode with a short custom target containing the exact scope, the optional user goal, and one to three non-binding risk directions. Retain its native text result verbatim.
3. If the native review returns no candidates, finish successfully without another model call.
4. If candidates exist, invoke one fresh read-only `codex exec` pass that may only keep or reject each existing candidate. It may not add, rewrite, merge, or reprioritize findings.
5. Parse the native review contract and normalize paths and lines deterministically, then publish JSON and Markdown.

## Prompt boundary

- Native Codex review owns semantic discovery. The wrapper does not ask the model to activate rules, prove checklist coverage, enumerate inactive dimensions, or perform a zero-finding rereview.
- Risk directions are deterministic hints, not review boundaries. At least one and at most three are supplied; the prompt explicitly permits findings outside them.
- The 20-item V1.2 rubric is not injected into either model call. It remains the versioned offline evaluation rubric and may inform the deterministic direction catalog.
- A user goal supplies change intent or an extra review concern. It cannot change read-only execution or the deterministic adaptation contract.

## Native CLI contract

- The main call uses `codex exec --sandbox read-only --ignore-user-config --ignore-rules --ephemeral review ... -`.
- A custom review prompt is the single native review target. The wrapper must not also pass `--base`, `--commit`, or `--uncommitted`, because current Codex CLI treats those targets as mutually exclusive.
- On Codex CLI 0.145.0, native `review` writes its review-agent text to `--output-last-message`; `--output-schema` does not turn that final message into JSON. The main call therefore retains native text without a schema. Only the candidate verifier uses `--output-schema` plus `--output-last-message`.
- Model selection is optional; reasoning effort defaults to `high` and is overridable.

## Candidate and failure semantics

- The adapter accepts either an explicit first-line `No findings.` result or the native `Review comment:` entries (`[P0]` through `[P3]`, title, absolute path, line range, and indented body). Empty, contradictory, or unrecognized text is malformed and cannot produce `PASS`.
- A reportable candidate must have non-empty title/body, priority 0-3, an absolute path inside the isolated checkout that maps to a changed file, and a positive ordered line range. Confidence is absent because native review does not emit it.
- Invalid individual candidates are excluded and recorded. If the native response contained candidates but none can be normalized, the result is `INCOMPLETE`, not `PASS`.
- Candidate-verifier input carries an explicit zero-based index for every candidate. The verifier must copy that exact index; missing, duplicate, or out-of-range decisions fail open.
- A failed or malformed candidate verifier is fail-open: every valid native candidate is kept and the verifier status records `failed_open`.
- A failed main native call or malformed main response produces an `INCOMPLETE` report.
- `PASS` means no valid candidate remained; findings produce `MANUAL_REVIEW`. The rollout remains report-only.

## Acceptance tests

- Native invocation is read-only, ephemeral, uses the `review` subcommand, and never combines the custom target with native scope flags.
- Direction selection is deterministic, bounded to one through three, and explicitly non-exhaustive in the prompt.
- Zero candidates cause exactly one model call.
- Existing candidates cause at most one verifier call; verifier failure preserves them.
- Native finding text, explicit `No findings.`, multiple findings, and ambiguous text have deterministic parser tests.
- Paths outside the checkout or outside changed files cannot enter the final report.
- The default plugin path calls `run-codex` and contains no rereview or 20-rule execution loop.
- `gofmt`, `go vet ./...`, `go test ./...`, and `git diff --check` pass.
