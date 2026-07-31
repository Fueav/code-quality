# v0.4.1 Codex Quality Hardening RED Evidence

Source baseline: `v0.4.0` / `799536a05bb4163e3ceac2f7907246a7f2ba5b66`

Command:

```sh
go test ./internal/intake ./internal/codexreview ./cmd/quality-review .
```

Observed failures before implementation:

- `TestExplicitBaselineDerivesReasonWhenOnlyRangeIsSupplied`: the old CLI required `--diff-reason` with base and target.
- `TestExplicitReasonRequiresRange`: the old error did not distinguish a reason without a range.
- `TestNativeReviewInvocationUsesOneCustomTarget`: native invocation did not enable JSONL events.
- `TestAdaptFindingsAcceptsCanonicalEquivalentCheckoutRoots`: a candidate under a canonical-equivalent root was dropped as outside the checkout.
- `TestRunCodexZeroFindingsUsesOneNativeReviewCall`: the CLI summary had no metrics path.
- `TestPluginSkillUsesThinNativeReviewPath`: the Skill did not preserve a user-supplied base/target pair.
- `TestMakefileReleaseGateCoversShippedComponents`: the root Python qualification suite was absent from the release gate.

No unrelated focused-test failure was observed.

## Evaluation follow-up

The first frozen real-history evaluation exposed an additional observability defect. Codex CLI 0.145.0 emitted an all-zero `turn.completed.usage` object for every nonempty review, and v0.4.1 initially marked those counters as available. Before the follow-up implementation, this command failed as expected:

```sh
go test ./internal/codexreview -run TestReadCodexUsageRejectsAllZeroCounters -count=1
```

`TestReadCodexUsageRejectsAllZeroCounters` observed non-nil zero counters and no error. The corrected contract records this upstream all-zero response as explicitly unavailable, without changing review semantics.
