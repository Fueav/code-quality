# Code Quality Review

**Result:** `MANUAL_REVIEW`<br>
**Rollout:** `report_only`<br>
**Policy:** `1.1.1`

## Baseline

- Repository: `web2-query-count-filter`
- Target branch: `main`
- Base commit: `b047242d12f8558816c20162c3607584f1452764`
- Target commit: `3776e7708851cfd4218a7a5e0f54c2b75ced4b59`
- Diff reason: User requested review of committed delta HEAD^..HEAD only

## Findings

### CQ-001 · COR-001

- Verdict: `MANUAL_REVIEW`
- Impact: The public GET /repos/{owner}/{repo}/commits API still passes its validated since/until values to both list queries, but this changed count command no longer passes them to git rev-list. Any time-filtered request therefore returns a filtered body with the count, Link pagination, X-Total, and X-PageCount for the unfiltered history. With path set, a path that has commits only outside the requested interval can also bypass the intended zero-match check and return 200 with an empty list instead of the previous 404. This breaks the documented date-filter contract and makes clients paginate against nonexistent results.
- Location: `modules/gitrepo/commit.go:35`
- Minimal fix: Restore adding --since and --until to CommitsCount when the respective option is non-empty, so its rev-list predicate matches the list queries. Add coverage for time-filtered counts, including the path case.

## Coverage And Uncertainty

- Uninspected scope: none
- Missing context: none
- Inspected context: `modules/gitrepo/commit.go` (Reviewed the changed count-command construction and retained time-filter option fields.); `routers/api/v1/repo/commits.go` (Verified API contract, propagation of since/until to the count helper, filtered list operations, and use of the count in response pagination and path zero-match handling.); `modules/git/commit.go` (Verified the non-path list operation forwards time filters.); `modules/git/repo_commit.go` (Verified the path list operation applies --since and --until.); `modules/gitrepo/commit_test.go` (Checked existing count-helper coverage.)

## Execution

- Host / Skill: `codex / 0.1.1`
- Agents: 1 (0 verifier)
- Tokens: unavailable input / unavailable output
- Duration: unavailable
- Retries: unavailable

## Adjudication Reasons

- CQ-001 requires manual review
