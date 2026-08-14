## v0.5.5

- Add explicit `--base-ref` / `--head-ref` direction and the read-only `plan` command so local and CI callers can freeze the same base tip, head, merge-base, changed files, contract digest, and `review_key` before invoking a Provider.
- Add `FULL` and `INCREMENTAL` review scopes. Incremental runs inspect only `previous_head..current_head`, re-evaluate every previous P0/P1, retain unresolved blockers, and report new delta findings separately.
- Fail closed with machine-readable `FULL_REQUIRED`, exit code 4, and zero Provider invocations when lineage, base tip, contract, Provider settings, ancestry, previous-result validation, or non-empty-delta requirements are not satisfied.
- Upgrade detailed results to schema v8 and summaries to schema v3 with deterministic review/finding identities, lineage, resolution evidence, and incremental counts.
- Publish the versioned company-CI envelope contract for external `EXECUTED / REUSED` and `CURRENT / SUPERSEDED` state without adding cache, PR publication, or repair-loop behavior to the CLI.
- Preserve legacy exact `--base / --target`, GitHub PR, GitLab MR, and local `origin/HEAD` discovery behavior, plus the v0.5.4 release floor where only P0/P1 block and P2/P3 remain advisory.

Compatibility note: detailed-result consumers must adopt schema v8 and summary consumers must adopt schema v3. An incremental caller must keep the complete immutable previous `review-result.json`; on `FULL_REQUIRED`, rerun explicitly with `--review-scope full` and without `--previous-result`.
