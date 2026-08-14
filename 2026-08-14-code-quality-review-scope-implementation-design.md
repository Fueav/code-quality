# code-quality v0.5.5 review-scope implementation design

Status: approved direction, implementation authority is the frozen v0.5.5 specification.

## 1. Selected architecture

The implementation combines two strong candidates from the architecture review:

1. **Frozen Review Plan Module** is the primary refactor.
2. **Native Outcome deepening** is the required wire-contract change.
3. **Provider Contract consolidation** is deliberately minimal and exposes only stable invocation facts.

The existing `internal/intake` package is replaced, not wrapped, by a deeper `internal/reviewplan` module. This keeps Git selection, incremental admission, and identity construction behind one interface instead of distributing them across the CLI, onboarding, session, and native runner.

## 2. Deep modules and interfaces

### 2.1 Review Plan

Main interface:

```go
decision, err := reviewplan.Build(ctx, reviewplan.Input{...})
```

`Build` absorbs:

- explicit commit, explicit ref, GitHub PR, GitLab MR, and local discovery;
- repository/ref normalization and commit resolution;
- base tip, current head, merge-base, full changed files, and delta changed files;
- previous-result safe loading and validation;
- incremental eligibility and `FULL_REQUIRED` reasons;
- deterministic contract digest and review key inputs;
- dirty-worktree observation.

`Decision` exposes two requests:

- `Request`: the full PR scope retained in the detailed result;
- `ProviderRequest`: the exact range materialized as `trusted.diff` and described to Codex or Claude.

The interface returns normal plan statuses instead of using errors for expected incremental fallback:

- `READY`: the provider may run once;
- `FULL_REQUIRED`: the provider must not run and the caller receives machine-readable reasons;
- `error`: malformed arguments, unreadable Git state, or another inability to construct a trustworthy plan.

There is intentionally no Git adapter interface. Production and tests both use real local Git repositories, which is the cheapest faithful substitute for the behavior being designed.

### 2.2 Native Outcome

`quality.NativeReviewResult` advances to schema v8 and becomes self-validating with respect to:

- `review_key` and `contract_digest`;
- `FULL`/`INCREMENTAL` lineage;
- stable finding IDs;
- exact previous-blocker resolution coverage;
- the separation of `new_findings`, unresolved previous findings, and current `findings`;
- P0/P1 release-gate consistency.

FULL provider output remains `{ "findings": [...] }`. Incremental provider output is a separate schema with:

```json
{
  "previous_finding_resolutions": [
    {
      "finding_id": "finding-v1:sha256:...",
      "status": "RESOLVED",
      "reason": "...",
      "current_finding": null
    }
  ],
  "new_findings": []
}
```

The incremental output schema uses only the Structured Outputs subset accepted by both native CLIs. Cross-field and lineage rules remain deterministic Go validation after the frozen provider bytes are captured.

### 2.3 Provider seam

The existing `Provider` seam remains the Codex/Claude adapter boundary. A small contract builder freezes:

- provider host;
- resolved model and reasoning effort;
- execution profile;
- provider-output schema digest;
- prompt-contract and rubric versions;
- tool and detailed-result schema versions.

No cache, verifier agent, Git selection, or incremental policy moves into provider adapters.

## 3. Execution flow

```text
CLI flags / CI context
        |
        v
reviewplan.Build
  | READY                          | FULL_REQUIRED
  | freezes identity               | provider_invocations = 0
  v                                v
current-head detached checkout     machine plan JSON, exit 4
provider-range trusted.diff
        |
        v
one Codex/Claude invocation
        |
        v
freeze exact output + transcript
        |
        v
quality.ClassifyFrozenNativeReview
        |
        v
schema-v8 result + schema-v3 summary
```

Codex Exec is run with its ordinary `--cd`/working-directory behavior. Base and head refs are never forwarded to Codex. The CLI resolves refs to commits, creates a detached checkout at `current_head`, writes the selected SHA diff, and tells the provider which commit range it is reviewing.

## 4. CLI contract

`doctor`, `run-codex`, and `run-claude` accept:

```text
--base-ref <destination-ref>
--head-ref <source-ref>
--review-scope <full|incremental>
--previous-result <review-result.json>
```

Rules:

- `--base-ref` and `--head-ref` are a pair.
- Ref flags and legacy `--base`/`--target` cannot be mixed.
- Legacy exact commits are FULL-only.
- INCREMENTAL requires `--previous-result` and either explicit refs or GitHub/GitLab change context.
- FULL rejects `--previous-result`, so the parent result cannot silently affect a full key.

A new read-only command computes the same plan and identity without authentication or provider execution:

```text
quality-review plan --host <codex|claude-code> [the same scope and provider flags]
```

`plan` always reports `provider_invocations=0`. A ready plan exits 0; `FULL_REQUIRED` exits 4; malformed or untrustworthy input exits 2 or 1 according to the existing CLI boundary.

## 5. Canonical identities

Canonical JSON is produced from structs with fixed field order and sorted slices, never from maps.

- Contract digest: `contract-v1:sha256:<hex>`.
- Review key: `review-v1:sha256:<hex>`.
- Finding identity: `finding-v1:sha256:<hex>`.

The result retains the canonical base/head refs, base tip, merge-base, full request, contract facts, goal, parent key, previous/current heads, and delta files. `quality.ValidateNativeResult` recomputes every digest and rejects tampering.

Remote URLs are normalized to repository paths and common branch spellings are canonicalized so `origin/production`, `refs/remotes/origin/production`, and PR-context `production` describe the same branch identity when they resolve to the same commit.

## 6. Incremental classification

The plan passes only the previous result's current P0/P1 findings to the provider. The classifier requires exactly one resolution per previous blocker:

- `RESOLVED` requires `current_finding=null`;
- `UNRESOLVED` requires a current finding and retains the prior stable ID;
- duplicate, unknown, missing, or contradictory resolutions turn the review into `ERROR`;
- new finding locations must be in delta changed files;
- unresolved prior finding locations must remain in the full PR changed-file set.

Final current findings are sorted by stable ID and equal:

```text
UNRESOLVED previous blockers + new findings
```

They determine PASS/BLOCK using the unchanged v0.5.4 threshold: only P0/P1 block.

## 7. Seams and locality

| Concern | Module | Local reason to change |
| --- | --- | --- |
| Git/ref discovery and incremental eligibility | `internal/reviewplan` | scope rules or CI-context support changes |
| Wire types, hashing, classification, validation | `quality` | result contract or release-gate semantics change |
| Provider CLI arguments/transcripts | `internal/nativereview/*_provider.go` | Codex/Claude protocol changes |
| Detached checkout and trusted diff | `internal/session` | evidence isolation changes |
| CLI flags and exit behavior | `cmd/quality-review` | user-facing command contract changes |

This division gives leverage without introducing speculative abstractions: each policy has one owner, while both providers reuse the same plan and outcome modules.

## 8. Rejected alternatives and deletion tests

Rejected:

- adding incremental conditionals to `internal/intake` and CLI handlers;
- a Git adapter/fake object graph;
- putting cache reuse or `SUPERSEDED` state into the CLI result;
- passing branch arguments to Codex Exec;
- silently widening failed incremental admission to FULL;
- asking a second agent to verify the first provider.

Deletion tests for the implementation:

1. `internal/intake` no longer exists and no import references it.
2. There is one review-plan builder used by plan, doctor, Codex, and Claude paths.
3. There is one canonical identity implementation used by creation and validation.
4. `result_source` and `lifecycle_status` appear only in integration documentation/fixtures, not raw CLI result types.
5. No Git interface or cache interface is introduced.
6. Provider adapters contain no FULL/INCREMENTAL eligibility rules.

## 9. Contract-first verification

RED tests are written before implementation for:

- Deploy-to-Production explicit refs and merge-base changed files;
- deterministic keys and one-input-at-a-time key changes;
- resolved, unresolved, new, duplicate, unknown, and omitted resolution cases;
- base-tip advance, rebase, contract change, provider change, and empty delta returning `FULL_REQUIRED` before invocation;
- legacy exact-commit and GitHub/GitLab/local compatibility;
- schema v8, summary v3, CLI help/plan/doctor, both native providers, docs, and plugin descriptors.

The PR 15 simulation is the concrete pre-implementation proof: a detached checkout at `8e138c68a16f`, provider delta `17700ad41ed4..8e138c68a16f`, and the previous P1 produced `RESOLVED` with no new findings, matching the production full-review `PASS` at the same head.
