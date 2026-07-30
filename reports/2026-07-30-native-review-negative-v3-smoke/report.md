# Native Review Contract-backed Negative v3 Smoke

## Conclusion

The model correctly understood the visible timeout contract and found no actionable defect, but the adapter returned `INCOMPLETE`. The model wrote a natural-language positive assessment instead of the adapter's required first-line `No findings.` marker.

This isolates the remaining failure to the terminal output protocol. It is no longer a sample-label or review-context defect, and it is not evidence of weak semantic review on this case.

## Frozen result

| Item | Value |
| --- | --- |
| Source commit | `c0ae4489df865249121f2b525ceaa2e11e438efb` |
| Freeze commit | `594d3b21dbf3c5ee4e7ff4f780810c885c178525` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Maximum / actual calls | 2 / 1 |
| Model assessment | No actionable defect |
| Adapter result | `INCOMPLETE` |
| Verifier | `not_needed` |

The final result is [review-result.json](evidence/REL-002-counterexample/sessions/review-2315737904/output/review-result.json), and the byte-preserved model response is [native-review.txt](evidence/REL-002-counterexample/sessions/review-2315737904/output/native-review.txt).

## Evidence assessment

The model inspected the exact committed diff and the identical `timeout-contract.json` present in both revisions. Its retained conclusion says that the derived context preserves caller cancellation, earlier deadlines, request-scoped values, and releases timer resources. It emitted no `Review comment:` candidate.

The strict adapter did not infer PASS from free-form prose. That conservative behavior prevented a silent false negative, but the invocation never asked the model to emit the explicit zero-finding marker that the parser requires.

## Next bounded check

Keep the strict parser unchanged. Add one terminal-format sentence to the native review prompt: when there are no actionable defects, the first nonblank line must be exactly `No findings.` Freeze the changed binary and repeat the same contract-backed case with a fresh two-call ceiling.
