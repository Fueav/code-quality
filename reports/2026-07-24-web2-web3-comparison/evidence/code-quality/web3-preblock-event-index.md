# Code Quality Review

**Result:** `MANUAL_REVIEW`<br>
**Rollout:** `report_only`<br>
**Policy:** `1.1.1`

## Baseline

- Repository: `web3-preblock-event-index`
- Target branch: `main`
- Base commit: `9ce3b20b593e6e247a6d69ccd6ba87a0a21465ff`
- Target commit: `f4e9ae21bfebba2ec56ab50b8a05f309549f69d2`
- Diff reason: 用户指定：仅审查当前仓库 HEAD 相对 HEAD^ 的已提交增量

## Findings

### CQ-001 · CHG-001

- Verdict: `MANUAL_REVIEW`
- Impact: A configured application whose PreBlocker emits events will now return those events without setting any attribute's Index flag. The FinalizeBlock response appends them unchanged, so neither the configured event keys nor the default 'index all' behavior reaches CometBFT. Pre-block events consequently disappear from event-index queries, breaking existing consumers that search for upgrade or other PreBlocker-produced events.
- Location: `baseapp/baseapp.go:672`
- Minimal fix: Restore sdk.MarkEventsToIndex(events, app.indexEvents) after collecting the PreBlocker events (and add a test that asserts the Index flags for both the default and configured index sets).

## Coverage And Uncertainty

- Uninspected scope: none
- Missing context: none
- Inspected context: `baseapp/baseapp.go` (Reviewed the changed preBlock event path and the documented indexEvents contract.); `baseapp/abci.go` (Verified pre-block events are appended unchanged to ResponseFinalizeBlock.Events.); `baseapp/abci_test.go` (Verified PreBlocker events are emitted and returned by FinalizeBlock.); `types/events.go` (Verified MarkEventsToIndex is the code that sets ABCI attribute Index flags.); `baseapp/options.go` (Verified callers configure event indexing through SetIndexEvents.)

## Execution

- Host / Skill: `codex / 0.1.1`
- Agents: 1 (0 verifier)
- Tokens: unavailable input / unavailable output
- Duration: unavailable
- Retries: unavailable

## Adjudication Reasons

- CQ-001 requires manual review
