# Native Review Explicit-marker Negative v4 Smoke

## Conclusion

The explicit `No findings.` instruction reached native review, but native review ignored it and again returned a concise positive assessment with no `Review comment:` section. The strict adapter therefore returned `INCOMPLETE` despite a correct semantic assessment.

Together with v3, this is repeat evidence that the actual native zero-candidate contract is successful nonempty prose without a review-comment section. Continuing to force a private marker through the prompt would add ineffective product ceremony.

## Frozen result

| Item | Value |
| --- | --- |
| Source commit | `c16e8847bd2da1590d8dd62f9bfeeea872f4a5da` |
| Freeze commit | `ac9a92b1e87902d05f7db965706196c74c00a421` |
| Model / reasoning | `gpt-5.6-sol` / `high` |
| Maximum / actual calls | 2 / 1 |
| Terminal instruction present | yes |
| Model assessment | No actionable defect |
| Adapter result | `INCOMPLETE` |

The final result is [review-result.json](evidence/REL-002-counterexample/sessions/review-65031574/output/review-result.json), and the byte-preserved response is [native-review.txt](evidence/REL-002-counterexample/sessions/review-65031574/output/native-review.txt).

## Evidence assessment

The retained CLI transcript contains the exact terminal-format instruction. The response nevertheless contains only a positive assessment and no candidate section. This matches the independent v3 response and differs sharply from the retained positive-defect runs, which include an explicit `Review comment:` heading and structured `[P0]` through `[P3]` entries.

The wrapper should therefore adapt the native review contract rather than invent a competing one: a successful nonempty response with no candidate section means zero candidates. Empty output and malformed candidate headers remain invalid; candidate sections remain strictly parsed and scope-checked.

## Next bounded check

Remove the ineffective format sentence, add regression coverage using the observed zero-candidate prose, and preserve rejection of orphan `[P*]` headers. After deterministic replay and full gates pass, run at most one final freshly frozen end-to-end smoke if needed.
