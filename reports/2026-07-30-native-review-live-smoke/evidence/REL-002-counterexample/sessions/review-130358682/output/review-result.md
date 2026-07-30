# Code Quality Native Review

**Result:** `MANUAL_REVIEW`  
**Rollout:** `report_only`

## Scope

- Repository: `REL-002-counterexample-repo`
- Base: `67574076f1a5c99b7bced9bbd84cf006c76d1734`
- Target: `e8f18407ae499d6f8c480da9208f3a8a8caf3cad`
- Goal: Assess the downstream request-context refactor for production safety.

## Non-binding directions

- `reliability-lifecycle`: Trace errors, timeouts, retries, cancellation, concurrency, and resource lifecycle across the changed path.

## Findings

### [P1] Preserve the caller context when adding the timeout

Whenever the caller cancels the request, has an earlier deadline, or supplies request-scoped values, deriving from `context.Background()` severs that context chain. `client.Do` can therefore continue for up to two seconds after cancellation and loses metadata such as tracing or authentication; derive the timeout from the supplied `ctx` instead.

- Location: `client.go:17-18`

## Execution

- Mode: `native_review`
- Model calls: 2
- Verifier: `failed_open`
- Verifier note: candidate verifier output is invalid: decision index 1 is out of range

## Adjudication

- 1 native finding(s) require manual review
