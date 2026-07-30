# Code Quality Native Review vNext Specification

Status: approved locked architecture
Scope: the default product review path; historical V1.2 workflows and synthetic evaluations remain offline assets only.

## Product contract

`quality-review run-codex` reviews one committed `base..target` increment in an isolated target checkout and publishes a report-only result. It never modifies the reviewed repository or CI status.

The runtime flow has one semantic model step:

1. Deterministically resolve `base`, `target`, changed files, and a trusted diff.
2. Invoke native `codex exec review` exactly once with a short custom target that fixes the committed scope.
3. Include a review goal only when the user explicitly supplied one. The goal is an optional focus, not a coverage boundary or a quality claim.
4. Retain the native text verbatim, adapt its findings deterministically to trusted changed files, and publish JSON and Markdown.

The default path has no automatic risk-direction selection, runtime rubric, checklist, candidate verifier, rereview, retry, or second model call.

## Prompt boundary

- Native Codex review owns semantic discovery and judgment.
- The wrapper supplies only the exact committed scope, the optional user goal, and the read-only instruction.
- A missing goal stays missing; the wrapper must not invent a default goal.
- A supplied goal must not suppress findings outside that focus.
- The 20-item V1.2 rubric remains a versioned offline evaluation standard and is never injected into the runtime prompt.

## Native CLI contract

- The call uses `codex exec --sandbox read-only --ignore-user-config --ignore-rules --ephemeral review ... -`.
- The custom target is the single native review target. The wrapper must not also pass `--base`, `--commit`, or `--uncommitted`.
- The native final text is retained through `--output-last-message`; no output schema is imposed on semantic discovery.
- Model selection is optional; reasoning effort defaults to `high` and is overridable.
- Successful execution always records exactly one model call, whether findings exist or not.

## Adaptation and failure semantics

- A successful nonempty native response without a candidate section means zero findings; explicit first-line `No findings.` remains accepted.
- A present candidate section must use an observed native heading and native `[P0]` through `[P3]` entries.
- A reportable finding must have a non-empty title and body, priority 0-3, and an absolute location that maps inside the isolated checkout to a changed file.
- Invalid individual candidates are excluded and recorded. If candidates were present but none map to trusted scope, the result is `INCOMPLETE` rather than `PASS`.
- A failed native call or malformed native response produces `INCOMPLETE`.
- Zero valid findings produce `PASS`; one or more valid findings produce `MANUAL_REVIEW` unchanged by any second model judgment.
- The result schema is v3 and contains no direction or verifier fields.

## Evaluation boundary

- Deterministic product conformance covers scope resolution, isolation, invocation, adaptation, publication, and the one-call invariant.
- Historical synthetic positives and counterexamples are regression and calibration fixtures only. They cannot prove product superiority or block the product architecture.
- A future quality A/B must use an independently qualified frozen benchmark. Newly discovered plausible defects make a sample `ambiguous` or `excluded`; they do not automatically count as model false positives.
- Any treatment lane differs from the official native baseline only by an explicitly supplied goal. A goal-mode win requires more accepted true findings without more accepted false positives under a decision rule frozen before execution.

## Acceptance tests

- Native invocation is read-only, ephemeral, uses `review`, and never combines the custom target with native scope flags.
- The prompt contains exact scope and no automatic review directions; it contains the user goal only when supplied.
- Zero and nonzero findings both cause exactly one model call.
- Native output grammar and trusted path/line adaptation remain deterministic.
- Native sessions contain no verifier schema or verifier output path.
- The default Skill contains no verifier, rereview, automatic direction, or V1.2 execution loop.
- `gofmt`, `go vet ./...`, `go test ./...`, and `git diff --check` pass.
