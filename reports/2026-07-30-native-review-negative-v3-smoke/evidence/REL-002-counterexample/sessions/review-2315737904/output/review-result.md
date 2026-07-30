# Code Quality Native Review

**Result:** `INCOMPLETE`  
**Rollout:** `report_only`

## Scope

- Repository: `REL-002-counterexample-v3-repo`
- Base: `bb5824f4f10b904035e62bf212521b1112350f1c`
- Target: `ccbfe0f8579c19f6ac31bb181ef70b96a33dc16c`
- Goal: Implement the approved two-second downstream timeout while preserving caller cancellation, earlier deadlines, and request-scoped values.

## Non-binding directions

- `reliability-lifecycle`: Trace errors, timeouts, retries, cancellation, concurrency, and resource lifecycle across the changed path.

## Findings

No actionable finding remained.

## Execution

- Mode: `native_review`
- Model calls: 1
- Verifier: `not_needed`

## Adjudication

- native review output is invalid: native review contains neither an explicit no-findings result nor a review comment section
