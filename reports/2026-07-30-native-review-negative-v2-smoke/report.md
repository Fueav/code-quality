# Native Review Corrected Negative v2 Smoke

## Conclusion

This two-call smoke did not reach `PASS`, but it proved that the corrected native adapter and verifier-index contract work end to end. The main review produced one candidate, the verifier copied explicit index `0`, completed normally, and independently kept the finding.

The remaining failure is an evaluation-context defect. The private canonical case says that two seconds is an approved downstream budget, while the review Agent sees neither that fact nor a specific change goal. The reviewed repository exposes only a five-second caller deadline. Given that evidence, treating a new unconditional two-second cutoff as a possible regression is reasonable.

## Frozen result

| Item | Value |
| --- | --- |
| Source commit | `ccb81b9c1d3ea47ddcbf2ffb2b40900313ede956` |
| Freeze commit | `17c53252bfaf4021c223d552d73bca3dccca9a58` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Maximum / actual calls | 2 / 2 |
| Final result | `MANUAL_REVIEW` |
| Verifier | `complete`, decision index `0`, `keep=true` |

The final result is [review-result.json](evidence/REL-002-counterexample/sessions/review-1718152687/output/review-result.json). The native review and structured verifier decision remain beside it.

## Finding assessment

The finding is not the earlier context-chain error: `context.WithTimeout(ctx, ...)` correctly preserves cancellation, earlier deadlines, and request values. Instead, both calls objected that operations previously allowed up to the declared five-second deadline are now canceled at two seconds.

The private `evals/cases.json` fact that calls two seconds “approved” cannot serve as evidence for a blind Agent when it is absent from the materialized repository. Therefore this run cannot measure product false-positive control; its expected `PASS` was not reviewable from the supplied context.

## Next bounded check

Place the approved two-second downstream SLA and the caller-context preservation requirement in an identical versioned contract in both base and target. Then pass the concrete change intent through `--goal` and repeat one fresh negative smoke with a new two-call ceiling. This tests the product contract without exposing the private PASS label.
