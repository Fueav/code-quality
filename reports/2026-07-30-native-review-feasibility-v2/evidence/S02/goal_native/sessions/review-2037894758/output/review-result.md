# Code Quality Native Review

**Result:** `MANUAL_REVIEW`  
**Rollout:** `report_only`

## Scope

- Repository: `repo`
- Base: `9ff2d501f92b4c345d92c80d91c6a9710c1a2624`
- Target: `a3f7530df3f9f0e68d9503c2bc09cac84be7a8cb`
- Goal: Reduce authorization lookup latency with a cache while preserving authoritative membership decisions.

## Non-binding directions

- `security-boundaries`: Check trust boundaries, authorization, validation, and secret handling around the changed behavior.
- `contracts-rollout`: Check whether changed interfaces, schemas, configuration, migrations, and rollout order remain compatible.
- `scale-side-effects`: Follow remote calls, storage access, loops, batching, caches, and side effects for production-scale failure modes.

## Findings

### [P1] Validate the version after reading the cache

If `Cache.Get` is delayed and the user's membership changes after the version read, the captured old version can still match the cached snapshot, allowing a revoked user even though the authority changed before the cache returned. Read the cache before `Authority.Version`, or recheck the version after the cache lookup, so cache latency cannot separate validation from use.

- Location: `cache.go:19-20`

## Execution

- Mode: `native_review`
- Model calls: 2
- Verifier: `complete`

## Adjudication

- 1 native finding(s) require manual review
