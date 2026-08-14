## v0.5.6

- Resolve one Provider model, reasoning effort, execution profile, scope, goal, and Git range for each review, then pass the same values to `plan`, `doctor`, and `run-codex` / `run-claude`.
- Make `max` the bundled production-CI default and the fixed value in official company-side README and Jenkins examples.
- Add a plan artifact to the reusable GitHub workflow and Jenkins example so operators can compare the frozen `review_key` and `contract_digest` before Provider execution.
- Document external `runner_policy_version` isolation for additional Service/Runner restrictions. A policy change invalidates REUSED and INCREMENTAL admission and forces FULL without modifying schema-v8 results or envelope-v1.
- Add direct contract coverage proving plan, doctor, and run produce the same identity when given one argument set.

Compatibility note: CLI review semantics and schemas remain v8/v3/envelope-v1. The reusable workflow still accepts explicit model and reasoning-effort inputs, but its default reasoning effort is now `max`. Because tool version is part of the review contract, v0.5.5 results are not eligible as v0.5.6 incremental parents and correctly require a FULL review.
