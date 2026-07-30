# Code Quality Native Review

**Result:** `MANUAL_REVIEW`  
**Rollout:** `report_only`

## Scope

- Repository: `repo`
- Base: `597dc030e96acd011c12cba80fb3ed432b0350f2`
- Target: `16236fac88a93a1430db051bad7239354981854f`
- Goal: Move order-event delivery to an outbox while preserving atomic persistence and at-least-once delivery.

## Non-binding directions

- `scale-side-effects`: Follow remote calls, storage access, loops, batching, caches, and side effects for production-scale failure modes.
- `data-business-correctness`: Trace value, state, identity, ordering, and precision changes through their real business effects.
- `reliability-lifecycle`: Trace errors, timeouts, retries, cancellation, concurrency, and resource lifecycle across the changed path.

## Findings

### [P1] Initialize the outbox before writing the order

When an existing `Database` has an initialized `orders` map but a nil `outbox` map—a valid setup before this change—`Save` commits the order and then panics while assigning to `database.outbox`. This leaves the order without its event and violates the new atomic-persistence guarantee; initialize or validate both maps before performing either write.

- Location: `place.go:40-41`

## Execution

- Mode: `native_review`
- Model calls: 2
- Verifier: `complete`

## Adjudication

- 1 native finding(s) require manual review
