# Historical defect trace v1.0.0

You are mining a historical repository in read-only mode. Analyze exactly the fix commit named below. Do not modify files, create commits, contact external services, or review unrelated changes.

Work in this order:

1. Read the fix diff and nearby code/tests to state the concrete pre-fix behavior, trigger, and production consequence. A subject containing "fix" is not proof of a defect.
2. Use local Git history (`git log`, `git show`, `git blame`, and ancestry checks) to locate the commit that introduced the defective behavior or omitted safeguard. Return the full commit hash and a concise evidence chain that another reviewer can reproduce. If it cannot be located, use an empty `introducing_commit`.
3. Map the defect to exactly one of these 20 review lenses, or `OUT_OF_SCOPE`:
   - DES-001 processing direction; DES-002 incremental processing; DES-003 remote call in loop; DES-004 authoritative data source; DES-005 batch work in synchronous request.
   - COR-001 explicit contract; COR-002 business invariant; COR-003 numeric/boundary calculation; COR-004 transaction boundary; COR-005 idempotency/duplicate side effects.
   - REL-001 unbounded resource growth; REL-002 timeout/cancellation; REL-003 concurrency correctness; REL-004 resource release; REL-005 error recovery.
   - SEC-001 authentication/authorization; SEC-002 untrusted input; SEC-003 sensitive information; CHG-001 compatibility; CHG-002 migration/rolling-release safety.
4. Set `material=true` only for a real production or release-safety consequence, not style, cleanup, test-only, docs-only, or speculative preference.
5. Set `static_detectable=true` only when the introducing diff plus repository-local contracts/context would let a code reviewer identify the defect without runtime telemetry or hindsight from the fix message.
6. Rate discovery difficulty from 1 (local and explicit) to 5 (cross-system, temporal, or highly non-local reasoning).
7. Classify the root cause:
   - `wrong_code`: code attempted the required behavior but implemented it incorrectly.
   - `missing_safeguard`: a required validation, authorization, timeout, rollback, compatibility guard, invariant check, or other protection was absent.

For `OUT_OF_SCOPE`, set `rule_id=OUT_OF_SCOPE`; still classify the closest root-cause form and explain why it is excluded. Keep `defect` concrete and self-contained. `defect_class_basis` must be one sentence.

Return only the JSON object required by the supplied schema.
