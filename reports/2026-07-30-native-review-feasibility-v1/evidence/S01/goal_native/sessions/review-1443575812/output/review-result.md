# Code Quality Native Review

**Result:** `MANUAL_REVIEW`  
**Rollout:** `report_only`

## Scope

- Repository: `repo`
- Base: `3a89a30c7fcd11ed9c8353c619620b144365bea7`
- Target: `827c39694573dad9abc19f271067aae0e82bb645`
- Goal: Use the intended pricing source during checkout while preserving correct charge amounts and source-of-truth behavior.

## Non-binding directions

- `data-business-correctness`: Trace value, state, identity, ordering, and precision changes through their real business effects.
- `security-boundaries`: Check trust boundaries, authorization, validation, and secret handling around the changed behavior.
- `contracts-rollout`: Check whether changed interfaces, schemas, configuration, migrations, and rollout order remain compatible.

## Findings

### [P1] Keep analytics prices out of checkout charges

When `ChargeOrder` processes `annual-plan`, this now sends the analytics price (100 cents) to the irreversible external charger instead of the authoritative price (10,000 cents). The added configuration explicitly marks analytics as non-authoritative and potentially 24 hours stale, so checkout must continue using `AuthoritativePrice` to avoid incorrect charges.

- Location: `price.go:24-24`

## Execution

- Mode: `native_review`
- Model calls: 2
- Verifier: `complete`

## Adjudication

- 1 native finding(s) require manual review
