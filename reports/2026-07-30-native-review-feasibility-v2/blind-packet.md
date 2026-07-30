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
    "body": "For `annual-plan`, this passes the analytics value of 100 cents to the irreversible charger instead of the authoritative 10,000 cents, undercharging every such order by 99%. The added source metadata explicitly marks analytics as reporting-only and potentially 24 hours stale, so checkout must continue using `AuthoritativePrice`.",
    "end_line": 24,
    "path": "price.go",
    "priority": 1,
    "start_line": 24,
    "title": "Keep checkout on the authoritative pricing source"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `ChargeOrder` processes `annual-plan`, this now sends 100 cents to the irreversible external charger instead of the authoritative 10,000 cents. The added production metadata explicitly marks analytics pricing as non-authoritative and potentially 24 hours stale, so using `AnalyticsPrice` here causes incorrect customer charges.",
    "end_line": 24,
    "path": "price.go",
    "priority": 1,
    "start_line": 24,
    "title": "Use the authoritative price for checkout charges"
  }
]
```

## S02

Kind: `counterexample`

Expected root: No actionable defect: a matching cheap coherence version returns the cached decision without the full authority lookup, while misses and mismatches synchronously use the authority.

### Output 1

Operational status: `COMPLETE`

```json
[]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "If `Cache.Get` is delayed and the user's membership changes after the version read, the captured old version can still match the cached snapshot, allowing a revoked user even though the authority changed before the cache returned. Read the cache before `Authority.Version`, or recheck the version after the cache lookup, so cache latency cannot separate validation from use.",
    "end_line": 20,
    "path": "cache.go",
    "priority": 1,
    "start_line": 19,
    "title": "Validate the version after reading the cache"
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
    "body": "When `WriteDebit` returns `ErrLedgerUnavailable`, the account has already been debited and no rollback occurs, leaving the balance changed without a corresponding ledger entry. This violates the failure contract and causes the existing failure-path check in `main` to exit with status 2.",
    "end_line": 27,
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
    "body": "When `ledger.Unavailable` is true, `CommitDebit` reduces the balance before `WriteDebit` returns `ErrLedgerUnavailable`, so `Transfer` reports failure while leaving the account debited. This violates the documented failure contract and the invariant checked in `main`; stage or commit the balance change only after the ledger write succeeds.",
    "end_line": 26,
    "path": "transfer.go",
    "priority": 1,
    "start_line": 26,
    "title": "Commit the debit only after the ledger write succeeds"
  }
]
```

## S04

Kind: `counterexample`

Expected root: No actionable defect: order and outbox commit under one lock without copying history, bounded reconciliation drains persisted events, and event-keyed delivery is idempotent.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "When an existing `Database` has an initialized `orders` map but a nil `outbox` map\u2014a valid setup before this change\u2014`Save` commits the order and then panics while assigning to `database.outbox`. This leaves the order without its event and violates the new atomic-persistence guarantee; initialize or validate both maps before performing either write.",
    "end_line": 41,
    "path": "place.go",
    "priority": 1,
    "start_line": 40,
    "title": "Initialize the outbox before writing the order"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When this receives a zero-valued `Database`, or a legacy database with only `orders` initialized, assigning to a nil map panics; in the latter case line 40 has already persisted the order before the outbox write fails, violating the atomic-outbox guarantee. Since the maps are unexported and there is no database constructor, initialize both maps under the lock before either assignment.",
    "end_line": 41,
    "path": "place.go",
    "priority": 1,
    "start_line": 40,
    "title": "Initialize both maps before committing the order"
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
    "body": "When `valid` is false, this return occurs before `connection.Close()` is deferred, so every invalid request permanently increments `pool.inUse`. After `capacity` invalid requests, subsequent calls fail with `ErrExhausted`\u2014including the third iteration in `main`; register the defer immediately after a successful `Open()`.",
    "end_line": 44,
    "path": "pool.go",
    "priority": 1,
    "start_line": 43,
    "title": "Register cleanup before rejecting invalid requests"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When `valid` is false, this return occurs before `connection.Close` is deferred, leaving `pool.inUse` incremented. Repeated invalid requests therefore leak pool capacity until `Open` returns `ErrExhausted`; register the defer immediately after a successful `Open`.",
    "end_line": 46,
    "path": "pool.go",
    "priority": 1,
    "start_line": 43,
    "title": "Close connections before returning invalid requests"
  }
]
```

## S06

Kind: `counterexample`

Expected root: No actionable defect: the framework path retains a deferred close and its test proves the response body is closed.

### Output 1

Operational status: `COMPLETE`

```json
[]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "When an external package tries to use the newly exported `Framework` API, it cannot supply a transport because the field is unexported and no constructor exists. The only value callers can create is `Framework{}`, whose `Fetch` method panics at `framework.transport.Fetch()`; expose a constructor or field, or keep this type unexported if it is only an implementation detail.",
    "end_line": 13,
    "path": "fetch.go",
    "priority": 2,
    "start_line": 13,
    "title": "Provide a usable way to initialize Framework"
  }
]
```

## S07

Kind: `positive`

Expected root: The target removes the authenticated-tenant predicate from a public update, allowing a repeated tenant-local ID to select another tenant's resource.

### Output 1

Operational status: `COMPLETE`

```json
[
  {
    "body": "Because resource IDs are tenant-local and can repeat, matching only `row.ID` lets an authenticated tenant modify another tenant's first matching row. The included attacker request consequently changes the victim row and makes `main` exit with status 2; pass the authenticated tenant through and retain the `TenantID` predicate.",
    "end_line": 16,
    "path": "update.go",
    "priority": 1,
    "start_line": 16,
    "title": "Retain tenant scope when selecting the row"
  }
]
```

### Output 2

Operational status: `COMPLETE`

```json
[
  {
    "body": "Because resource IDs are tenant-local and may repeat, matching only `row.ID` lets any authenticated tenant overwrite another tenant's resource when that resource appears first with the requested ID. The production handler now discards the verified tenant identity, so the update must continue matching both the resource ID and authenticated tenant.",
    "end_line": 16,
    "path": "update.go",
    "priority": 1,
    "start_line": 16,
    "title": "Preserve tenant filtering when updating resources"
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
