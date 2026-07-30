# Native Review Evaluation Admission Audit

Status: **PASS**

Spec commit: `b470869`

Frozen evaluation source commit: `e275e7f`

Model calls: `0`

## Decision

The legacy 60-case suite is not one homogeneous automatic accuracy benchmark for the simplified native-review product.

- 40 cases are qualified for automatic comparison: 20 positive and 20 counterexample.
- 20 `insufficient` cases are retained for human calibration only.
- 0 cases remain blocked after repair.

The automatic contract is intentionally coarse: a positive passes when review finds at least one actionable defect introduced by the target; a counterexample passes when review reports no actionable introduced defect. Extra valid findings are allowed. Legacy rule ID, S/T/E fields, and exact finding count are diagnostic only.

The machine authority is `evals/native-review-admission.json`. It freezes both `evals/cases.json` and the complete fixture tree by SHA-256.

## Why the old measurement was misleading

The audit found three evaluation effects that can make a simpler, lower-noise review look worse even when its judgment is reasonable:

1. One third of the suite was unresolved by construction. All 20 `insufficient` cases were nevertheless labeled as required findings, so a reviewer that correctly refused to speculate was counted wrong.
2. Exact rule/count/S/T/E matching treated valid review variation as failure. In particular, a positive may contain more than one valid root or may express the same defect without the legacy taxonomy.
3. Several fixtures did not isolate the intended signal. Seven counterexamples changed an unrelated public API or behavior, nine metadata entries disagreed with checked-in evidence, and two safe-path claims lacked a complete repository-visible proof.

This audit repairs the measuring instrument. It does not by itself prove that the product prompt is better; that requires a later bounded comparison on the admitted 40 cases.

## Per-rule conclusions

`Q` means qualified for automatic scoring. `H` means human-only because a decisive fact is intentionally unresolved.

| Rule | Positive | Counterexample | Insufficient |
| --- | --- | --- | --- |
| DES-001 | Q — visible cardinality and schedule prove infeasible nested work | Q — fixed 2×2 domain and equivalence test preserve behavior | H — scale and schedule budget unknown |
| DES-002 | Q — 20M-row full scan replaces a five-minute checkpoint path | Q — CPU-local scan rejects inputs above 20 | H — frequency and row count unknown |
| DES-003 | Q — 600k remote calls replace one batch call | Q — both sides reject more than three regions | H — caller cardinality and rate limit unknown |
| DES-004 | Q — checkout switches from authority to divergent analytics price | Q — version mismatch and cache miss fall back to authority | H — source authority undocumented |
| DES-005 | Q — 500k synchronous writes exceed the two-second contract | Q — request type hard-bounds CPU-local work to five items | H — item limits and latency objective unknown |
| COR-001 | Q — changed refund branch contradicts approved v2 contract | Q — added v2 path matches every documented branch | H — repository requirements conflict |
| COR-002 | Q — overdraft violates the non-negative balance contract | Q — UNIQUE plus `ErrDuplicate` contract removes check/insert race safely | H — overdraft policy missing |
| COR-003 | Q — cents are multiplied before irreversible provider debit | Q — bankers rounding is explicitly contracted and boundary-tested | H — downstream unit contract missing |
| COR-004 | Q — debit commits before a stable ledger failure with no recovery | Q — atomic outbox, reconciler, and idempotent delivery close the path | H — transaction and compensation guarantees unknown |
| COR-005 | Q — redelivery can repeat an irreversible payout | Q — message ID is a provider-proven idempotency key | H — provider idempotency semantics unknown |
| REL-001 | Q — 100k blocked goroutines exceed the process budget | Q — 20-item input and four-worker cap bound concurrency | H — input and downstream release bounds unknown |
| REL-002 | Q — `Background` drops deadline and cancellation under load | Q — derived timeout preserves caller cancellation and earlier deadline | H — caller lifecycle and downstream SLO unknown |
| REL-003 | Q — concurrent read-modify-write loses updates | Q — one owner goroutine serializes all writes and closes safely | H — ownership and concurrency model unknown |
| REL-004 | Q — normal invalid requests leak a finite DB pool | Q — framework path closes every response and is tested | H — resource ownership contract unknown |
| REL-005 | Q — stable failure causes zero-delay infinite retry | Q — read-only probe has bounded backoff and pages on exhaustion | H — supervision and retry policy unknown |
| SEC-001 | Q — public update drops tenant ownership | Q — signed editor identity and tenant-scoped lookup are both preserved | H — gateway trust boundary undocumented |
| SEC-002 | Q — public input reaches a command shell | Q — placeholder query keeps attacker input as a bound value | H — template escaping and input source unknown |
| SEC-003 | Q — active reusable credential is committed and logged | Q — test-only value is non-routable and excluded from production | H — credential validity and build reachability unknown |
| CHG-001 | Q — identity key changes with no migration or dual read | Q — versioned dual write/read and validated backfill preserve access | H — persisted identity compatibility unknown |
| CHG-002 | Q — application deploy precedes the required schema expand | Q — expand/migrate/contract order and rollback are explicit | H — rollout order and compatibility window unknown |

## Repairs made during admission

- Preserved the existing public API and unrelated behavior in DES-001, DES-003, DES-004, COR-002, REL-001, SEC-001, and SEC-002 counterexamples.
- Aligned repository-visible scale, schedule, deadline, authority, and safety evidence with case metadata.
- Added identical base/target contracts where stable evidence is required, including the DES-005 request contract and COR-002 store contract.
- Added behavioral tests for DES-001 permission equivalence, COR-002 conflict mapping, and COR-004 outbox reconciliation/idempotency.
- Added a deterministic admission validator that checks complete classification, frozen hashes, fixture reviewability, scoring semantics, and removed/changed exported Go functions in counterexamples.

## Verification

The contract test was first observed RED because `native_review_admission` did not exist. After implementation:

- `python3 -m unittest -v pilot.test_native_review_admission`: 3 passed.
- `python3 pilot/native_review_admission.py`: admission ready; 40 qualified, 20 human-only, 0 blocked; hashes match.
- `python3 -m unittest discover -s pilot -p 'test_*.py' -v`: 21 passed.
- `make live-test`: 8 passed.
- `make mining-test`: 2 passed.
- `go test ./...`: passed, including all fixture packages.
- `go vet ./... && go build ./...`: passed.
- focused COR-002/COR-004 fixture tests: passed.
- `gofmt` check and `git diff --check`: passed.

## Residual uncertainty and next gate

These are synthetic fixtures. Human semantic admission removes known label and evidence defects but does not establish real-world representativeness or native-review superiority.

The next authorized step should therefore be a small frozen feasibility comparison on the 40 qualified cases, not another 60-case replay. The 20 human-only cases may be inspected qualitatively but must not affect the automatic score.
