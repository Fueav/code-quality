# Work And Verification

Daily work follows the requested outcome: understand relevant contracts, implement, verify, and deliver. Routine tasks need no workflow classification, readiness audit, separate plan, or closeout artifact. Persist only product contracts, material decisions, or requested reports.

## Verification

`make verify` selects the repository's development checks from the changed inputs. CI uses a clean candidate with its declared checks; release tags and scheduled health checks use full release verification. Profiles describe verification, not task categories.

New behavior and bug fixes need meaningful tests. Select migration, prompt, performance, and integration checks when their inputs or contracts change. Unknown impact requires broader verification. Keep boundary and secret checks; required checks cannot be waived by a model. During repair, rerun affected checks; after success, repeat only for changed inputs or unresolved evidence. Evidence reuse requires `harnessctl evidence verify` against the exact commit, baseline and current verification inputs.

## Completion

Finish the authorized outcome, including requested integration or deployment, with observable evidence at the affected entrypoint. Report the result, relevant checks, and any unverified or blocked operation. Git integration does not prove deployment. Existing authorization covers routine reversible repair; missing authority or unresolved high-risk semantics stops only the affected action after safe preparation.

Template delivery audits run during explicit bootstrap, upgrade, or diagnosis. A stale delivery record does not prevent daily work. Keep prompt guidance net-line non-positive; project artifacts contain only their own facts and evidence.

Product contracts use approved date-prefixed root `*-spec.md` files. Native test and release-check gates retain the review service contracts; the Harness does not change provider invocation or adjudication policy.
