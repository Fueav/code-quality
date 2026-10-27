# v0.6.0 — reliable review execution

This candidate keeps the existing Native → Restricted review flow and severity policy. Invalid evidence references and unverifiable target checkouts now stop publication with ERROR/HOLD, including an empty Native finding set. Restricted starts from a fresh committed target and cannot succeed if its integrity check is interrupted.

Trusted production runners share one coordinator through the existing `run-*` commands. It serializes the same change, verifies retained results before zero-Provider reuse, resumes only the bounded Restricted stage, and permits one legal incremental review. Missing known state, changed execution configuration, or a second incremental round requires intervention. Exact-session packaging replaces recursive file searches and repeated archive creation. Personal sessions with unbound configuration do not gain cross-run caching.

The live evaluation path now uses the production CLI and native schema v10. Failure, retry, timing, and token observations remain visible. Offline fixture tests establish implementation behavior; they do not establish current model recall or real-world speed. The prompt contract changes to version 4 while the result schema remains version 10. Model defaults are unchanged.

Distribution binds an approved source/base pair to one full release verification and an exact-source build. Candidate CI, human approval, and approved release packaging use separate workflows. First installation of the approval runtime requires explicit owner review because the prior trusted branch does not contain that runtime. No repository protection settings are changed.

CLI installation stages and verifies the new binary before atomic file replacement. Bootstrap retains the previous CLI/plugin combination and an executable offline rollback, rejects concurrent installation drift, and verifies the selected host's plugin version. This is a recoverable transaction across the two stores, not a simultaneous atomic switch.

## Upgrade requirements and limits

- Use the fixed-version bootstrap with an existing supported host CLI and Python 3.8 or later. Keep its `QUALITY_REVIEW_ROLLBACK` path until the new CLI/plugin pair is verified.
- Coordinated CI requires a private persistent state root, runner policy version and a trusted effective configuration fingerprint. Existing caller workflows must grant read access to pull requests for publication-time freshness checks.
- Checkout filters or initialized submodules that cannot reproduce exact committed blobs are rejected. File locks serialize a shared local filesystem; distributed runners still need shared scheduling ownership.
- There is no measured v0.6.0 real-model latency or recall result yet. Publication requires a genuine frozen evaluation bundle for the current contract and specific owner approval of the final SHA. These are pending for this candidate.
