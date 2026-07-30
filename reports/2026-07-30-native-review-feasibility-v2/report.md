# Native Review Goal-mode Feasibility v2

Status: **CLOSED — DO NOT RERUN OR EXPAND**

## Decision

The corrected eight-case gate did not pass. Goal mode and the official native baseline were exactly tied after blind adjudication:

- positives: both 4 pass, 0 fail;
- counterexamples: both 2 pass, 1 fail, and 1 blocked output;
- complete execution: 16/16 for both lanes combined;
- actual/maximum model calls: 22/24, with no retry.

This result does not support adding more runtime review rules or prompt constraints. The thin goal did not improve positive coverage in this sample, and each lane produced one different false positive. One supposedly corrected negative still contained a genuine atomicity bug, so the experiment cannot authorize the 40-case comparison.

The subsequent locked-architecture decision retires this synthetic gate from product-superiority scoring. Its fixtures remain regression and calibration evidence only; the product no longer contains a candidate verifier or automatic risk directions.

Machine outcome: [outcome.json](outcome.json)

## Expansion conditions

| Frozen condition | Result |
| --- | --- |
| All 16 sessions complete | PASS — 16/16 |
| Goal positives at least 3/4 | PASS — 4/4 |
| Goal counterexamples at least 3/4 | **FAIL** — 2 counted passes, 1 fail, 1 blocked |
| Goal not worse than baseline by kind | PASS — exact tie |
| No blocked benchmark cases | **FAIL** — S04 |
| Frozen hashes match | PASS |

## Blind results after lane reveal

The raw evidence was committed before any candidate output was read. Anonymous judgments and the sealed lane map were committed before reveal.

| Case | Kind | Goal mode | Native baseline | Assessment |
| --- | --- | --- | --- | --- |
| S01 / DES-004 | Positive | PASS | PASS | Both identify the reporting-only price reaching the charger and the concrete undercharge. |
| S02 / DES-004 | Counterexample | FAIL | PASS | Goal mode reports a version-read/cache-read timing window. Under the frozen contract, the authorization can linearize at the authoritative version read, so a later overlapping revocation is not a concrete violation. |
| S03 / COR-004 | Positive | PASS | PASS | Both identify the debit committed before the failed ledger write. |
| S04 / COR-004 | Counterexample | BLOCKED | BLOCKED | Both independently find that a legacy database may have `orders` initialized and `outbox` nil; `Save` then writes the order before panicking on the outbox assignment, violating atomicity. |
| S05 / REL-004 | Positive | PASS | PASS | Both identify the return-before-defer connection leak and finite pool exhaustion. |
| S06 / REL-004 | Counterexample | PASS | FAIL | The baseline treats direct construction of exported `Framework` as required, although public `Fetch` remains the usable API and executes the tested framework path. |
| S07 / SEC-001 | Positive | PASS | PASS | Both identify the removed tenant predicate and cross-tenant update. |
| S08 / SEC-001 | Counterexample | PASS | PASS | Neither lane reports an actionable defect. |

The S02 and S06 false positives cancel numerically but differ in direction. The goal focuses more aggressively on the stated authorization intent; the baseline focuses on a newly exported type. With only three adjudicated clean negatives, this is not enough evidence to prefer either lane.

## Corrections proved by v2

### Native output adaptation

The retained v1 S04 response first failed the new replay test with `native finding appears without a review comment section`. The adapter now accepts the bounded observed grammar for singular, plural, and `Full review comments:` headings while keeping malformed, orphan, empty, and contradictory sections invalid.

The exact retained v1 response now deterministically parses two findings. In the v2 live execution every session completed, and S04 goal mode retained one candidate after a complete verifier call with zero adapter drops. The v2 live response happened to use `Review comment:`; support for the original `Full review comments:` form is proved by byte-for-byte replay rather than another model call.

### DES-004 counterexample

The earlier guaranteed full authority lookup is removed. A repository-visible contract defines `Authority.Version` as a cheap coherence read, and behavioral tests prove a matching cache hit avoids `Authority.Current` while misses and mismatches synchronously use it.

The baseline accepted the corrected negative. Goal mode's timing finding was adjudicated as a false positive because the frozen contract permits the decision to linearize at the version read. If the business requires revocation to take effect before every in-flight authorization returns, that stronger semantic must be added explicitly; the current fixture does not establish it.

### COR-004 counterexample

The v1 quadratic full-history cloning defect is removed. Placement now mutates both maps under one lock without replacing them, and reconciliation processes bounded batches outside delivery locks.

The fixture repair was still incomplete: base behavior permits an initialized `orders` map with nil `outbox`, while target `Save` writes `orders` before its nil-map panic. Both lanes found this real defect. S04 therefore remains invalid for automatic negative scoring.

## Execution integrity

| Item | Frozen value |
| --- | --- |
| Product source | `c9bfbc946a0ae95e86f31c5a0819d2b723e9dfee` |
| Product binary | `quality-review c9bfbc9-feasibility-v2` |
| Product SHA-256 | `cb948fc243c12639a0000080b3ddf3d98ee6d953c16805eaeb06fbd4c123a470` |
| Codex CLI | `0.145.0` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Sessions | 16 isolated fresh clones and authentication-only `CODEX_HOME` directories |
| Complete / incomplete | 16 / 0 |
| Maximum / actual calls | 24 / 22 |
| Retries | 0 |
| Protocol manifest SHA-256 | `f9668b7d53ca2cb36cea3e66c46c6799f5e4aa75cddde4713fe9ed3bf3e3a7f3` |
| Evidence freeze commit | `05abfb7` |
| Pre-reveal judgment commit | `411cd43` |

No authentication file, credential-pattern match, or symlink was retained in the evidence tree.

## Specification audit

- The v2 specification was committed before RED tests and implementation.
- RED evidence records the old adapter failure, full authority lookup, unbounded outbox implementation, and missing cache contract with zero model calls.
- The correction commit precedes and exactly matches the frozen product source in the v2 manifest.
- Admission remained ready at 40 automatic and 20 human-only cases with no exported-function regression.
- Preflight materialized all eight repositories, matched the binary and protocol hashes, and recorded zero model calls.
- All 16 planned sessions retained their raw inputs, outputs, exit status, model, reasoning effort, and call count.
- Evidence was frozen before reading candidates; judgments were frozen before lane reveal.
- The machine expansion decision is false because two mandatory conditions are false.

## Closed follow-up

Do not continue the S02/S04 repair loop and do not rerun this matrix. A future quality A/B starts from a separately qualified benchmark built from real historical changes. Plausible newly discovered defects move a sample to `ambiguous` or `excluded` before scoring rather than triggering another fixture patch.

## Residual uncertainty

The sample is synthetic and contains only four positives and, after blocking S04, three adjudicated negatives. The S02 timing judgment depends on ordinary linearizable-read semantics; a stricter revocation contract would change that assessment. Even a later passing eight-case gate would authorize only the 40-case comparison, not prove population-level superiority or authorize release, push, tag, or deployment.
