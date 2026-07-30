# Native Review vNext Minimal Live Smoke

## Conclusion

The architecture is feasible, but this frozen smoke did **not** qualify the negative-case gate. Native review found the intended positive defect, and the candidate verifier independently preserved it. The supposed counterexample was not a true negative: it bounded the wait to two seconds but still severed the caller context, so both model calls reported a real regression.

The run also found two integration contracts that unit-only feasibility checks had missed: native `codex exec review` returns review-agent text on Codex CLI 0.145.0 even when `--output-schema` is supplied, and a verifier can interpret an unlabelled single candidate as one-based index `1`.

## Frozen execution

| Item | Frozen value |
| --- | --- |
| Baseline checkpoint | `37a3078e7dfaed0d464f0e05ac830849df2e0918` |
| Post-run contract fix | `1dcea57153a62c45484d5cee88cf60d7618d3715` |
| Model | `gpt-5.6-sol` |
| Reasoning | `high` |
| Goal | `Assess the downstream request-context refactor for production safety.` |
| Maximum calls | 4 |
| Actual calls | 4 |
| Corrected-adapter binary | SHA-256 `11edd3d09510af500b85193e2a75e613d8e323e55bb8fdc750ca5c684b671101` |

No call was added after the ceiling was reached.

## Results

| Call | Case | Role | Observed result | Assessment |
| --- | --- | --- | --- | --- |
| 1 | REL-002-positive | Native review | Correct P0 at `client.go:24`; initial wrapper result was `INCOMPLETE` because it attempted JSON decoding | Semantic discovery passed; adapter assumption failed |
| 2 | REL-002-positive | Candidate verifier, reusing call 1 output | `keep=true`; reconciled final result `MANUAL_REVIEW` | Positive gate passed without rerunning discovery |
| 3 | REL-002-counterexample | Native review | P1: `context.Background()` drops caller cancellation, earlier deadlines, and request values | Finding is valid; frozen PASS label is invalid |
| 4 | REL-002-counterexample | Candidate verifier | Semantically kept the same defect, but returned index `1` for the only zero-based candidate | Program correctly failed open; indexing prompt was ambiguous |

The positive final evidence is [review-result-reconciled.json](evidence/REL-002-positive/sessions/review-944322599/output/review-result-reconciled.json). The counterexample evidence is [review-result.json](evidence/REL-002-counterexample/sessions/review-130358682/output/review-result.json). Raw native and verifier outputs remain beside those files.

## What was corrected

- The main call now uses `--output-last-message` only and stores `native-review.txt`. A strict adapter accepts explicit `No findings.` or native `Review comment:` entries; ambiguous output becomes `INCOMPLETE`.
- The unused native-output schema and invented confidence field were removed. The candidate verifier remains the only schema-bound model call.
- Verifier input now includes an explicit zero-based `index` next to each finding, and the prompt requires copying that exact value. Invalid decisions still fail open.
- RED→GREEN tests cover real native finding text, explicit no-findings text, multiple findings, ambiguous text, absence of the ineffective main `--output-schema`, and explicit verifier indexes.

The final index correction was not live-rerun because doing so would have violated the frozen four-call ceiling. Its behavior is covered mechanically; live proof remains pending.

## Why the negative fixture was invalid

The target changed `client.Do(ctx)` to `client.Do(context.WithTimeout(context.Background(), 2s))`. A shorter timeout bounds the maximum wait, but it does not preserve the incoming context. Compared with the base commit, caller cancellation, a deadline shorter than two seconds, tracing/authentication values, and other request-scoped values are all lost. Therefore a zero-finding expectation cannot be used to measure false-positive control for this change.

## Next evidence step

Replace only the invalid negative target with a genuine bounded derivation, `context.WithTimeout(ctx, 2s)`, freeze its hashes and expected `PASS`, then run one new negative smoke with a fresh call ceiling. Until that passes, this experiment supports native-review feasibility and positive sensitivity, but makes no valid claim about specificity or large-sample A/B quality.
