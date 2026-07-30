# Code Quality Native Review vNext Implementation Report

## Outcome

The v0.4.0 architecture is locked to one semantic model call. `quality-review run-codex` deterministically fixes and isolates one committed `base..target` scope, invokes native `codex exec review` once, adapts the retained native text to trusted changed files, and publishes a report-only result.

The wrapper no longer selects risk directions, invents a default goal, runs a candidate verifier, rereviews, retries, or injects the V1.2 rubric. A user-supplied `--goal` is the only optional semantic input and is explicitly presented as a focus rather than a review boundary.

## Contract evidence

| Locked contract | Implementation evidence |
| --- | --- |
| Exactly one native call | Both zero-finding and finding-bearing tests assert `model_calls=1`; the finding path publishes the original valid native candidate directly |
| Optional goal only | Empty input stays empty and is omitted from the prompt and JSON; supplied input is quoted as a non-limiting optional focus |
| No automatic directions | The direction catalog and result fields were deleted; prompt tests reject automatic direction text |
| No verifier path | Verifier code, session paths, schema, logs, result fields, and Skill guidance were deleted |
| Deterministic safety boundary | Native text grammar, checkout containment, changed-file mapping, line validation, and adapter-drop behavior remain covered |
| Report-only semantics | Zero findings produce `PASS`, valid findings produce `MANUAL_REVIEW`, and failed or unusable native output produces `INCOMPLETE` |
| Frozen external contract | Native result and CLI summary use schema v3; plugin/runtime version is v0.4.0 |

## Evaluation decision

The 60 synthetic cases remain offline regression and calibration fixtures, not product-superiority labels. The v1 and v2 feasibility profiles are mechanically retired: their manifests and raw evidence remain auditable, while the runner refuses to mark them ready for another execution.

A future A/B requires a separately qualified benchmark built from real historical changes. A plausible newly discovered defect moves a sample to `ambiguous` or `excluded` before scoring. A goal treatment is preferred only if a predeclared paired comparison finds more accepted true findings without more accepted false positives; a tie leaves native review as the default and goal mode optional.

## Verification

- RED: focused contracts failed on the old invocation signature, required directions and verifier fields, missing v3 schema, old Skill guidance, and the two-call prompt behavior.
- GREEN: all Go package tests passed, including the single-call finding and zero-finding paths.
- Static and build checks passed: `gofmt`, `go vet ./...`, `go build ./cmd/quality-review`, and `git diff --check`.
- Python qualification, live-watch/live-adjudication, and historical-mining suites passed after replacing the obsolete “old protocol is ready” assertion with a retirement assertion.

## Intentionally not performed

No model A/B, token-spending smoke, release tag, push, deployment, or superiority claim was performed. The next quality experiment is blocked on an independently qualified real-change benchmark, not on further synthetic fixture repair.
