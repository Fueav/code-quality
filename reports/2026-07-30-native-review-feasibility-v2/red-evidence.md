# Native Review Feasibility v2 RED Evidence

Model calls: `0`

The v2 contract tests were executed against the pre-correction product and fixtures after specification commits `606af0f` and `3632993`.

## Observed failures

| Contract | Command | Pre-correction failure |
| --- | --- | --- |
| Retained native heading replay | `go test ./internal/codexreview -run 'Test(ReadNativeOutputReplaysV1FullReviewComments\|NativeOutputAcceptsObservedCandidateHeadingGrammar\|NativeOutputRejectsEmptyResults\|NativeOutputRejectsNoFindingsFollowedByAnyCandidateHeading)' -count=1` | The exact v1 S04 response failed with `native finding appears without a review comment section`; plural/full headings were unrecognized, and empty or contradictory full sections were accepted. |
| DES-004 cache hit/fallback | `go test ./pilot/fixtures/des-004-counterexample/target -count=1` | Every case observed `version=0 current=1`; the target performed the full authority lookup before the cache. |
| COR-004 bounded outbox | `go test ./pilot/fixtures/cor-004-counterexample/target -count=1` | Build failed because `reconciliationBatchSize` and `nextOutboxBatch` did not exist. |
| Repository-visible cache contract | `python3 -m unittest -v pilot.test_native_review_admission.NativeReviewAdmissionTest.test_known_label_and_visibility_repairs_remain_aligned` | The identical base/target `cache-contract.json` was absent. |

These are expected RED results. They authorize only the implementation described by the frozen v2 specification.
