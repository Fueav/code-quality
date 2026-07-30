# Native Review Goal-mode Feasibility v1

Status: **DO NOT EXPAND TO 40**

## Decision

The eight-case feasibility gate did not pass, so this experiment does not authorize the 40-case comparison.

This is not evidence that thin goal mode is semantically worse than the official native baseline. Both lanes found all four admitted positive roots. The failed gate instead exposed two counterexamples that are not clean negatives and one real native-output adapter defect in the goal-mode product.

| Frozen condition | Result |
| --- | --- |
| All 16 sessions complete | **FAIL** — 15 complete; goal-mode S04 was `INCOMPLETE` |
| Goal positives at least 3/4 | PASS — 4/4 |
| Goal counterexamples at least 3/4 | **FAIL** — 2 counted passes, 1 fail, 1 blocked |
| Goal not worse than baseline by kind | PASS |
| No blocked benchmark cases | **FAIL** — S02 and S04 |
| Frozen hashes match | PASS |

Machine outcome: [outcome.json](outcome.json)

## Blind results after lane reveal

The raw evidence was committed before adjudication. The anonymous judgments and sealed lane map were then committed before lane reveal.

| Case | Kind | Goal mode | Native baseline | Assessment |
| --- | --- | --- | --- | --- |
| S01 / DES-004 | Positive | PASS | PASS | Both identify the reporting-only price reaching the charger and its concrete charge impact. |
| S02 / DES-004 | Counterexample | BLOCKED | FAIL | Goal mode correctly notices that `authority.Current` runs before the cache, so the change cannot reduce authority-lookup latency. The baseline's same-version contradiction depends on facts outside the frozen contract. This is not a clean negative. |
| S03 / COR-004 | Positive | PASS | PASS | Both identify commit-before-ledger-write and the partial debit left after failure. |
| S04 / COR-004 | Counterexample | FAIL (`INCOMPLETE`) | BLOCKED | Both native reviews found genuine full-store cloning growth. The baseline retained it; goal mode's adapter rejected a valid `Full review comments:` section. This is not a clean negative. |
| S05 / REL-004 | Positive | PASS | PASS | Both identify return-before-defer and finite pool exhaustion. |
| S06 / REL-004 | Counterexample | PASS | FAIL | Goal mode reports no defect. The baseline incorrectly requires external construction of an internal framework path even though public `Fetch` constructs and executes it and the test covers that path. |
| S07 / SEC-001 | Positive | PASS | PASS | Both identify the removed tenant predicate and cross-tenant update. |
| S08 / SEC-001 | Counterexample | PASS | PASS | Neither lane reports an actionable defect. |

The scorer excludes blocked outputs from pass/fail totals:

| Lane | Positives | Counterexamples | Blocked output |
| --- | --- | --- | --- |
| Goal mode | 4 pass, 0 fail | 2 pass, 1 fail | S02 |
| Native baseline | 4 pass, 0 fail | 1 pass, 2 fail | S04 |

On the only two counterexamples that remained clean after adjudication, goal mode passed 2/2 and the baseline passed 1/2. That is directionally favorable but far too small to establish superiority.

## What actually failed in S04

The goal-mode native call did not miss the problem. Its byte-preserved response contains two concrete findings:

- cloning all historical orders and pending events on every placement creates quadratic cumulative work while holding the global mutex;
- cloning an entire delivery backlog can block placements and double backlog memory before progress begins.

The wrapper nevertheless returned `INCOMPLETE` with:

`native review output is invalid: native finding appears without a review comment section`

The parser recognizes only the exact heading `Review comment:`. Codex returned `Full review comments:` followed by valid `[P1]` and `[P2]` entries. This is a deterministic adapter mismatch, not a model-quality failure. The run remains failed under the frozen protocol and was not retried.

- Raw S04 product result: [review-result.json](evidence/S04/goal_native/sessions/review-3670807343/output/review-result.json)
- Raw S04 native response: [native-review.txt](evidence/S04/goal_native/output/native-review.txt)

## Execution integrity

| Item | Frozen value |
| --- | --- |
| Product source | `aa8bdf838b550281565af509b48631710751ea70` |
| Codex CLI | `0.145.0` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Sessions | 16 isolated fresh clones and isolated `CODEX_HOME` directories |
| Maximum / actual model calls | 24 / 21 |
| Retries | 0 |
| Complete / incomplete | 15 / 1 |
| Protocol manifest SHA-256 | `4ba2385a25ebe7bca71f3ca3625c8cdbddf1d36f47f74fc8f8408f0c931b0014` |
| Evidence freeze commit | `21aa5a6` |
| Pre-reveal blind judgment commit | `2cef642` |

No user configuration or repository rules were supplied to either lane. No authentication file, credential material, or symlink was retained in the evidence tree.

## Specification audit

- The sample, goals, lanes, runtime, order, call ceiling, and scoring rules match the frozen specification and manifest.
- All 16 planned records and their raw inputs, outputs, process status, model, reasoning effort, and call counts are retained.
- The 21-call total is below the 24-call ceiling; no hidden retry was used after S04 failed.
- Evidence was frozen before judgment, and judgments were frozen before lane reveal.
- The expansion decision is mechanically false because three required conditions are false.

## Next bounded iteration

Do not spend tokens on the 40-case run yet.

1. Add RED-to-GREEN deterministic replay coverage for the retained `Full review comments:` response and make the adapter accept native review's observed candidate-section variants without weakening finding parsing.
2. Remove S02 and S04 from automatic-negative use until their unrelated defects are repaired. Replace or rematerialize them so the cache actually avoids the expensive path and outbox persistence does not copy unbounded history.
3. Freeze a new product binary, fixture hashes, and eight-case protocol, then rerun this same small paired gate with fresh sessions. Expand only if that corrected gate passes.

The evidence supports keeping the product direction thin: native review already found the important roots. The immediate quality bottlenecks are the evaluation instrument and deterministic output adaptation, not a missing runtime checklist or a need for more prompt constraints.

## Residual uncertainty

This is a synthetic eight-case feasibility check, not a population estimate. Two of four negatives were invalidated during adjudication, leaving only two clean specificity observations. Even after repairing the adapter and fixtures, a passing rerun would authorize the 40-case comparison only; it would not prove real-world superiority or authorize release, push, tag, or deployment.
