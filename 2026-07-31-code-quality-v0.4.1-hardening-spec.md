# Code Quality v0.4.1 Codex Quality Hardening Specification

Status: approved by owner on 2026-07-31

## Scope

This patch release hardens the Codex-only native-review path before any further quality claim. Claude Code compatibility is explicitly deferred. The one-call, report-only architecture and result schema v3 remain unchanged.

## Product contracts

### Trusted finding locations

- A native finding is inside the isolated checkout when its canonical filesystem location is inside the canonical checkout root.
- Equivalent macOS paths such as `/tmp/...` and `/private/tmp/...` must map to the same changed file.
- A path that resolves through a symlink outside the checkout must remain rejected.
- A run with native candidates must not become `PASS`; if every candidate is invalid it remains `INCOMPLETE`.

### Explicit review range

- `--base` and `--target` form the explicit range and must be supplied together.
- `--diff-reason` is optional for an explicit range. When absent, the deterministic reason is `explicit_commit_range`.
- A supplied reason is preserved unchanged.
- Supplying only one endpoint, or a reason without both endpoints, fails closed.
- The Codex Skill passes an explicitly supplied base/target pair and does not silently fall back to the local baseline merely because the user did not provide an internal reason string.

### Release gate

- `make release-check` runs the root Python qualification suite in addition to Go, live-watch/live-adjudication, mining, vet, formatting, and diff checks.
- The gate remains free of model calls.

### Native execution observability

- Native Codex review emits JSONL events to the retained stdout log.
- Each run writes `native-run-metrics.json` schema v1 beside the raw native output.
- Metrics record wall duration, input/output tokens when a valid final `turn.completed` event exists, changed-file count, and trusted-diff bytes.
- Missing usage events are represented explicitly and do not change the semantic review result.
- The CLI summary exposes the metrics path. `review-result.json` remains schema v3.

## Non-goals

- No Claude Code compatibility work.
- No new review directions, rubric injection, verifier, rereview, retry, or automatic file exclusion.
- No change to `PASS`, `MANUAL_REVIEW`, or `INCOMPLETE` semantics.
- No release, tag, push, installation, or marketplace mutation without a later owner request.

## Acceptance evidence

1. RED tests reproduce the macOS-equivalent-root rejection, the base/target-without-reason rejection, the missing root Python release gate, and absent native JSON metrics.
2. Focused tests pass after implementation.
3. `make release-check VERSION=v0.4.1 VERIFY_COMPARE_REF=v0.4.0` passes from a clean candidate checkout.
4. A candidate binary verifies its version and completes a deterministic fake native-review smoke with one model call and retained metrics.

## Capability evaluation gate

After all product acceptance evidence is GREEN, evaluate the candidate on the eight real historical changes frozen in `pilot/historical-pilot-seed.json`: four defect-introducing commits and their four safe fixes.

- Materialize each committed range in an isolated repository with no answer key in the model-visible tree.
- Use one fresh, ephemeral, authentication-only Codex session per case, `gpt-5.6-sol`, reasoning `high`, no goal, and no retry.
- Freeze all eight raw outputs and metrics before reading or applying `ground_truth` and `label_note`.
- Report severe-case hit rate, safe-fix false-positive rate, completion rate, adapter drops, duration, and token use.
- Treat the eight-case result as sample-specific. It cannot establish population-level superiority.
