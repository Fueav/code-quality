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
| Deterministic output boundary | Strict native-text parsing, absolute-path containment, changed-file mapping, and line/priority validation | Native adapter, result validator, ambiguous-text rejection, and out-of-checkout test |
| No old runtime prompt artifacts | Native sessions materialize only scope plus the candidate-verifier schema | End-to-end CLI test and active-path forbidden-field scan |
| Report-only | Final semantic states are `PASS`, `MANUAL_REVIEW`, or `INCOMPLETE`; CI action remains `publish_report` | `ValidateNativeResult` tests |

## Verification evidence

- RED: the initial `internal/codexreview` contract test failed to compile because the native runner, direction selector, and v2 result types did not exist.
- GREEN: `go test ./...` passed.
- Static checks: `go vet ./...` and `git diff --check` passed.
- Auxiliary suites: all live-watch/live-adjudication and historical-mining Python tests passed.
- Prompt discipline: the shipped Skill became two lines shorter and contains no rereview or rule-activation loop.
- CLI feasibility: a bounded four-call live smoke used local `codex-cli 0.145.0`, `gpt-5.6-sol`, and `high` reasoning. It proved that native review returns review-agent text rather than schema JSON, so the adapter now consumes that real contract. See `reports/2026-07-30-native-review-live-smoke/report.md`.

## Intentionally not performed

No large frozen-sample A/B run, release tag, push, or deployment was performed. The minimal live smoke exhausted its predeclared four-call ceiling and found that the proposed negative fixture was not actually negative; no extra run was added after that finding.
