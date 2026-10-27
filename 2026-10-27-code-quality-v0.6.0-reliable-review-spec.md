# v0.6.0: reliable review execution and delivery

The owner approved implementing the reviewed reliability and simplification plan, publishing the validated release, and upgrading the MacBook and Mac mini on 2026-10-27. This contract supersedes conflicting older instructions only in the areas below. Existing production-floor severity, independent adjudication, and automatic-lineage limits remain unchanged.

## One review lifecycle

The existing host commands remain the entrypoints. A shared coordinator may select verified result reuse, restricted-stage recovery, a single legal incremental review, or the first full review. It does not change the immutable native result schema or invent a successful result from an incomplete run. No additional Provider, global model change, or unrestricted automatic full-review fallback is introduced.

- A supplied evidence reference that cannot be verified is a protocol failure (`ERROR/HOLD`), not a supported rejection. A structurally valid `INSUFFICIENT/E1` assessment remains subject to the existing production floor.
- Native discovery and restricted adjudication use the exact committed target. Native checkout drift prevents trustworthy publication, including an otherwise empty finding set. First execution and recovery enforce the same target-view invariant.
- A frozen input index identifies full PR scope, Provider scope, changed files and the trusted diff. It does not treat Native reasoning as independent evidence or restrict adjudication to excerpts.
- Stage metrics preserve their existing meaning and gain optional process/freeze timings. Missing measurements are unknown, never zero. Historical model comparisons do not establish current latency or justify changing defaults.

## Trusted coordination

Coordination state is private, outside reviewed repositories, and serialized per change. Reuse requires complete, verified immutable evidence plus the review identity, runner policy and effective execution fingerprint. A caller cannot make an unknown personal context cacheable merely by reusing a head SHA. Host/model/configuration changes must invalidate the effective execution fingerprint. A current-head callback must re-read its authoritative source; frozen CI event data is not a fresh PR observation.

Repeated active requests do not invoke another Provider. A published PASS or BLOCK may be reused without changing its result. ERROR, unknown outcomes, corrupt/missing known state and unverifiable checkpoints cannot become green or silently start a new full review. Recovery invokes no Native process and keeps the existing two-attempt Restricted limit. A trustworthy FULL parent permits at most one strictly append-only INCREMENTAL with unchanged base, goal and contract; a further automatic round returns MANUAL_REQUIRED. FULL_REQUIRED remains incomplete until an authorized new full review.

The coordinator and CI share one total execution budget, reserving time for evidence and cleanup. Publication rechecks base/head and request ownership; superseded evidence is retained but cannot update a newer request's gate. Artifacts are selected from the exact structured result paths and archived once per immutable session, never by choosing the first file below a shared root.

## Evaluation and release

Live evaluation calls the production run entrypoint directly using the existing host login. It no longer copies authentication or delegates legacy prepare/finalize orchestration to another model. Current native findings are consumed without losing identity or locations; unknown schemas are rejected. Errors/timeouts remain in completion statistics and cannot count as successful clean reviews. Quality and timing use the same frozen run records, grouped by contract/model/effort/scope. Offline regression evidence is identified as offline; no unexecuted or fabricated model-efficacy evaluation may qualify a release.

`make dist` becomes the single distribution entrypoint: reject dirty state, freeze the source SHA and explicit comparison baseline, verify the complete release contract once with genuine owner authorization bound to those SHAs, validate sealed evidence, then build from the frozen source. The manifest records version, source SHA, baseline and asset digests. Candidate checks retain affected-test selection; candidate success does not substitute for full release verification. CI human approval must come from an actual eligible human review on the relevant SHA; an agent must not fabricate it.

The fixed-version bootstrap preflights its host, stages and validates replacement files, retains the previous CLI/plugin combination and an executable rollback, then switches. Any failure restores the previous product-specific installation where present. It must not overwrite unrelated plugins or user settings. Same-version verified installations are idempotent. CI continues to use a preinstalled CLI.

## Required regression evidence

Fault injection covers invalid citations; mutated target files, including hidden index flags; corrupt/absent coordination state; duplicate/concurrent requests; identity and policy invalidation; superseded publication; incremental/attempt caps; unknown evaluation schemas; dirty or moved release SHAs; missing release approval; and failed plugin updates with rollback. Tests use fake Providers and isolated fixtures unless a real run is separately authorized. Performance reports distinguish measured local work, historical records and unmeasured real-model behavior.
