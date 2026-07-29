# Code Quality Review

**Result:** `MANUAL_REVIEW`<br>
**Rollout:** `report_only`<br>
**Policy:** `1.1.1`

## Baseline

- Repository: `web2-list-pagination`
- Target branch: `main`
- Base commit: `dd137ef93923b4c7683d3bc5fdccc4ffb2d12c61`
- Target commit: `b2efd341bd8594ce46563ad23b335e3263899e4c`
- Diff reason: Review committed increment HEAD relative to HEAD^ as requested

## Findings

### CQ-001 · COR-003

- Verdict: `MANUAL_REVIEW`
- Impact: The runner list API accepts page/limit and the settings UI uses page-based LIMIT/OFFSET queries. With the default/online/offline ordering, any runners sharing last_online (and with the alphabetical modes, sharing name) now have no unique secondary key. SQL may return those tied rows in a different order on consecutive page requests, so a user paging through a sufficiently large runner list can receive a runner twice while another runner is skipped. This affects all scopes that call the shared runner listing endpoint and the web settings list.
- Location: `models/actions/runner.go:256`
- Minimal fix: Restore a unique, deterministic secondary key for each non-ID sort, e.g. append `id ASC` to the last_online and name orderings (including the default), so each page boundary has a stable total order.

## Coverage And Uncertainty

- Uninspected scope: none
- Missing context: none
- Inspected context: `models/actions/runner.go` (Reviewed the changed runner ordering implementation and sortable fields.); `models/db/list.go` (Verified that FindAndCount applies LIMIT/OFFSET and ToOrders to the runner query.); `routers/api/v1/shared/runners.go` (Verified the shared runner API list endpoint uses FindAndCount with request pagination.); `routers/api/v1/utils/page.go` (Verified page and limit request parameters become database list options.); `routers/web/shared/actions/runners.go` (Verified the web runner settings page uses page-based FindAndCount queries.)

## Execution

- Host / Skill: `codex / 0.1.1`
- Agents: 1 (0 verifier)
- Tokens: unavailable input / unavailable output
- Duration: unavailable
- Retries: unavailable

## Adjudication Reasons

- CQ-001 requires manual review
