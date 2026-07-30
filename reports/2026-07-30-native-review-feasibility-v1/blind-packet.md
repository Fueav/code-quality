# Blind Goal-mode Feasibility Adjudication Packet

Judge each output only against the frozen expected root. Lane identity is sealed separately.
For positives, pass only when at least one finding states the introduced root and concrete impact. For counterexamples, pass only when there is no actionable introduced finding. Use blocked only for a genuine defect that invalidates admission.

## S01

Kind: `positive`

Expected root: Checkout selects the reporting-only analytics price instead of the authoritative price and sends the divergent value to the charger.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `ChargeOrder` processes `annual-plan`, this now sends the analytics price (100 cents) to the irreversible external charger instead of the authoritative price (10,000 cents). The added configuration explicitly marks analytics as non-authoritative and potentially 24 hours stale, so checkout must continue using `AuthoritativePrice` to avoid incorrect charges.",
    "end_line": 24,
    "path": "price.go",
    "priority": 1,
    "start_line": 24,
    "title": "Keep analytics prices out of checkout charges"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "For `annual-plan`, this changes the irreversible production charge from the authoritative 10,000 cents to the analytics value of 100 cents. The added configuration explicitly marks analytics as non-authoritative reporting-only and permits 24 hours of lag, so `ChargeOrder` will undercharge customers or use stale prices; checkout should continue using `AuthoritativePrice`.",
    "end_line": 24,
    "path": "price.go",
    "priority": 1,
    "start_line": 24,
    "title": "Keep checkout on the authoritative price source"
  }
]
```

## S02

Kind: `counterexample`

Expected root: No actionable defect: only a version-matched cached decision is used, and misses or mismatches synchronously fall back to the authority.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `Current` performs the remote or storage lookup this cache is intended to optimize, it is completed before `cache.Get`, so every cache hit still incurs the full latency and backend load and remains blocked by authority outages. Cache freshness must be established without completing the expensive `Current` call before returning the cached decision.",
    "end_line": 17,
    "path": "cache.go",
    "priority": 1,
    "start_line": 17,
    "title": "Avoid the authoritative lookup on cache hits"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When the cache contains `Allowed: true` with the same numeric version that `Current` returns with `Allowed: false`, this branch grants access despite the authoritative denial. This can occur after version reuse/reset or an incoherent cache, and no interface contract guarantees globally unique versions; use the current authority decision or fail closed when the values disagree.",
    "end_line": 19,
    "path": "cache.go",
    "priority": 1,
    "start_line": 18,
    "title": "Refuse cache entries that contradict the authority"
  }
]
```

## S03

Kind: `positive`

Expected root: The debit commits before a stable ledger-write failure and no rollback, compensation, or reconciliation restores the balance.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `ledger.Unavailable` is true, this mutates the account before `WriteDebit` returns `ErrLedgerUnavailable`, so a failed transfer leaves the account debited despite no ledger entry. This violates the atomic failure contract and causes the existing failure check in `main` to exit with status 2; defer or roll back the account mutation unless the ledger write succeeds.",
    "end_line": 26,
    "path": "transfer.go",
    "priority": 1,
    "start_line": 26,
    "title": "Commit the debit only after the ledger write succeeds"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `ledger.WriteDebit` returns an error, such as when `ledger.Unavailable` is true, `CommitDebit` has already reduced the account balance. `Transfer` therefore reports failure while leaving a debit behind, violating the transaction contract and the existing check in `main`; commit the debit only after the ledger write succeeds.",
    "end_line": 27,
    "path": "transfer.go",
    "priority": 1,
    "start_line": 26,
    "title": "Preserve the balance when the ledger write fails"
  }
]
```

## S04

Kind: `counterexample`

Expected root: No actionable defect: order and outbox commit together, persisted events are reconciled, and event-keyed delivery is idempotent.

### Output 1

Operational status: `INCOMPLETE`

```json
[]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "As the database grows, every placement copies all previously stored orders (and all pending events), making insertion of N orders \u0398(N\u00b2) and generating substantial allocation and GC pressure. Since `database.mu` already excludes all internal readers and writers, updating both maps while holding that lock can preserve the intended atomic visibility without copying the complete history.",
    "end_line": 50,
    "path": "place.go",
    "priority": 2,
    "start_line": 49,
    "title": "Avoid cloning the full order history on every save"
  }
]
```

## S05

Kind: `positive`

Expected root: The changed invalid-input return occurs after connection acquisition but before release, leaking one connection per normal invalid request.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `valid` is false, this return occurs before `connection.Close()` is deferred, so every rejected request permanently increments `pool.inUse`. After `capacity` invalid requests, all subsequent calls fail with `ErrExhausted`; register the defer immediately after a successful `Open()` so this return path also releases the connection.",
    "end_line": 46,
    "path": "pool.go",
    "priority": 1,
    "start_line": 43,
    "title": "Close the connection before rejecting invalid requests"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `valid` is false, `Handle` returns before this defer is registered, leaving the opened connection counted in `pool.inUse`. After `capacity` invalid requests, all subsequent calls fail with `ErrExhausted`; keep the close defer immediately after a successful `Open`.",
    "end_line": 46,
    "path": "pool.go",
    "priority": 1,
    "start_line": 46,
    "title": "Close the connection before returning invalid requests"
  }
]
```

## S06

Kind: `counterexample`

Expected root: No actionable defect: the framework path retains a deferred close and its test proves the response body is closed.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "Because `transport` is unexported and no constructor or setter is provided, callers outside package `fetch` cannot create a usable `Framework`; the only constructible zero value panics when `Fetch` dereferences its nil transport. The new test misses this because it invokes the legacy package-level `Fetch` function rather than `Framework.Fetch`. Export the dependency field or add a constructor.",
    "end_line": 13,
    "path": "fetch.go",
    "priority": 2,
    "start_line": 13,
    "title": "Make Framework constructible by external callers"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[]
```

## S07

Kind: `positive`

Expected root: The target removes the authenticated-tenant predicate from a public update, allowing a repeated tenant-local ID to select another tenant's resource.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "Because resource IDs can repeat across tenants, matching only `row.ID` lets any authenticated tenant update whichever tenant's matching row appears first. In the included example, the attacker request updates the victim's `shared-id` row, violating the stated tenant isolation; the authenticated tenant must remain part of the repository predicate.",
    "end_line": 16,
    "path": "update.go",
    "priority": 1,
    "start_line": 16,
    "title": "Retain tenant filtering for tenant-local resource IDs"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When two tenants use the same resource ID, any authenticated tenant can now update the first matching row regardless of ownership because `authenticatedTenant` is discarded. The route contract explicitly permits tenant-local IDs to repeat and exposes this write operation to every authenticated tenant, so this enables cross-tenant modification; retain `TenantID == authenticatedTenant` in the lookup.",
    "end_line": 16,
    "path": "update.go",
    "priority": 1,
    "start_line": 16,
    "title": "Keep tenant scope in the update predicate"
  }
]
```

## S08

Kind: `counterexample`

Expected root: No actionable defect: signed gateway identity, the editor-role check, and tenant-scoped lookup are all enforced.

### Output 1

Operational status: `COMPLETE`

```json
[]
```

### Output 2

Operational status: `COMPLETE`

```json
[]
```
