# Code Quality Native Review vNext Implementation Report

## Outcome

The new default path is `quality-review run-codex`. It starts from the existing deterministic committed-scope intake, creates a clean target checkout, and delegates semantic discovery to native `codex exec review`.

The historical V1.2 rule activation, inactive-dimension explanation, zero-finding rereview, and host-authored model JSON are not used by this path. V1.2 remains available only as an offline evaluation rubric and through explicitly named legacy commands.

## Specification audit

| Contract | Implementation evidence | Verification |
| --- | --- | --- |
| Exact committed scope | `intake.Discover`, trusted `base..target` diff, isolated target checkout | Existing intake/session tests plus native CLI integration test |
| Native review owns discovery | Main process invokes the `review` subcommand with a short custom target | Generated argument contract test and local Codex 0.145.0 parser check |
| No competing native target | Main invocation contains no `--base`, `--commit`, or `--uncommitted` | `TestNativeReviewInvocationUsesOneCustomTarget` |
| One to three non-binding directions | Deterministic change-signal catalog with generic fallback and explicit non-exhaustive wording | `TestSelectDirectionsIsDeterministicAndBounded` |
| Zero findings stop after one call | Verifier branch is entered only for normalized candidates | `TestZeroFindingsSkipsVerifier` and end-to-end CLI test |
| Candidate-only falsification | Second call returns only indexed keep/reject decisions; original findings are filtered deterministically | `TestVerifierCanOnlyFilterExistingCandidates` |
| Verifier failure is fail-open | Process, shape, or decision-contract failure keeps every valid native candidate | Verifier failure and missing-field tests |
| Deterministic output boundary | Strict required fields, absolute-path containment, changed-file mapping, line/priority/confidence validation | Native adapter, result validator, and out-of-checkout test |
| No old runtime prompt artifacts | Native sessions materialize only scope plus the two model schemas | End-to-end CLI test and active-path forbidden-field scan |
| Report-only | Final semantic states are `PASS`, `MANUAL_REVIEW`, or `INCOMPLETE`; CI action remains `publish_report` | `ValidateNativeResult` tests |

## Verification evidence

- RED: the initial `internal/codexreview` contract test failed to compile because the native runner, direction selector, and v2 result types did not exist.
- GREEN: `go test ./...` passed.
- Static checks: `go vet ./...` and `git diff --check` passed.
- Auxiliary suites: all live-watch/live-adjudication and historical-mining Python tests passed.
- Prompt discipline: the shipped Skill became two lines shorter and contains no rereview or rule-activation loop.
- CLI feasibility: the complete read-only native review argument order was accepted by local `codex-cli 0.145.0` using `--help`; no model review was launched.

## Intentionally not performed

No large frozen-sample A/B run, release tag, push, or deployment was performed. This iteration validates the architecture and executable contract without spending review tokens on a broad experiment.
