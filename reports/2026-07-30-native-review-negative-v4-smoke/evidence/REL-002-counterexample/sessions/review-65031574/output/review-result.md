# Code Quality Native Review

**Result:** `INCOMPLETE`  
**Rollout:** `report_only`

## Scope

- Repository: `REL-002-counterexample-v4-repo`
- Base: `cfeba349e532d7a010eb21924efc32d470ce5f94`
- Target: `7391baaf4c6652c6a2eecd1c55384ca2e0bae62f`
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
