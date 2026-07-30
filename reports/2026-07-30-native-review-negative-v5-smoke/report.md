# Native Review Zero-candidate Negative v5 Smoke

## Conclusion

The contract-backed negative smoke passed end to end. Native review returned no candidate section, the adapter correctly treated its nonempty assessment as zero candidates, and the final result was `COMPLETE / PASS` with zero findings after one model call. The verifier was not invoked.

This closes the bounded feasibility loop: the earlier poor result was a combination of invalid evaluation context and a wrapper contract that did not match native review's actual rendered output, not evidence that the simplified semantic review path was intrinsically weaker.

## Frozen result

| Item | Value |
| --- | --- |
| Source commit | `2d60862655e41cc0bd07e3d8735db3ca902de626` |
| Freeze commit | `c762b570b9dbedf16e996a964bcd7c76be81f89c` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Maximum / actual calls | 2 / 1 |
| Final result | `COMPLETE / PASS` |
| Findings | 0 |
| Verifier | `not_needed` |

The final result is [review-result.json](evidence/REL-002-counterexample/sessions/review-1781695336/output/review-result.json), and the byte-preserved response is [native-review.txt](evidence/REL-002-counterexample/sessions/review-1781695336/output/native-review.txt).

## Contract evidence

- The frozen base and target both contain the approved two-second timeout contract.
- The target derives the timeout from the caller context rather than `context.Background()`.
- The concrete goal names timeout enforcement plus caller cancellation, earlier deadlines, and request-scoped values.
- The native response independently confirms those properties and emits no `Review comment:` candidate section.
- The prompt contains no private zero-finding marker, checklist coverage demand, or zero-finding rereview.

## Interpretation

The thin product boundary now matches native review: Codex owns semantic discovery; the wrapper fixes scope and supplies intent plus non-binding directions; deterministic code adapts the returned candidate section or zero-candidate assessment. The 20-item quality rubric remains an offline evaluation standard rather than a runtime reasoning cage.

This one qualified negative does not establish population-level precision or recall. It is sufficient to validate the corrected architecture before auditing the remaining frozen samples or spending tokens on a larger A/B run.
