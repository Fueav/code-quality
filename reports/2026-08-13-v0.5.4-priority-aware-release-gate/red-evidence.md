# v0.5.4 RED evidence

Baseline: `origin/main` at `6954cca5363e95d3a126af1c67820e9e8d65d6f8`, with runtime behavior inherited from `v0.5.3`.

Before the implementation change, the new priority-boundary tests were run with:

```sh
go test ./quality ./internal/nativereview
```

The run failed for the intended contract gaps:

- `NativeReleaseSummary` had no `AdvisoryIssues` field.
- P2/P3 findings were counted as blockers and could not coexist with `PASS`.
- the native review prompt still requested every actionable defect without pointing to priority definitions or excluding non-defect review noise.

These failures establish that the priority-aware release decision was not already present before the implementation.
