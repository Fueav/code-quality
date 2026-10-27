# v0.6.0 — reliable review execution

This candidate keeps the existing Native → Restricted review flow and severity policy. Invalid evidence references and unverifiable target checkouts now stop publication with ERROR/HOLD, including an empty Native finding set. Restricted starts from a fresh committed target and cannot succeed if its integrity check is interrupted.

Trusted production runners share one coordinator through the existing `run-*` commands. It serializes the same change, verifies retained results before zero-Provider reuse, resumes only the bounded Restricted stage, and permits one legal incremental review. Missing known state, changed execution configuration, or a second incremental round requires intervention. Exact-session packaging replaces recursive file searches and repeated archive creation. Personal sessions with unbound configuration do not gain cross-run caching.

The live evaluation path now uses the production CLI and native schema v10. Failure, retry, timing, and token observations remain visible. Offline fixture tests establish implementation behavior; they do not establish current model recall or real-world speed. The prompt contract changes to version 4 while the result schema remains version 10. Model defaults are unchanged.

Distribution binds an approved source/base pair to one full release verification and an exact-source build. Candidate CI, human approval, and approved release packaging use separate workflows. First installation of the approval runtime requires explicit owner review because the prior trusted branch does not contain that runtime. No repository protection settings are changed.

CLI installation stages and verifies the new binary before atomic file replacement. Bootstrap retains the previous CLI/plugin combination and an executable offline rollback, rejects concurrent installation drift, and verifies the selected host's plugin version. This is a recoverable transaction across the two stores, not a simultaneous atomic switch.

Native timeout without a final message now retains partial evidence as a non-retryable terminal error. Request failures expose an available session status with a nonzero exit; CI never presents a retained PASS as a successful failed request. Older Claude hosts are supported only after their available commands and the single user-scoped marketplace are verified before uninstall, including rejection of settings aliases.

## Limited model evidence

The owner requested publication without further real-model evaluation on 2026-10-27. Four frozen cases ran against candidate `6e4b72521dce9fad3c03055354bf34a652b56847` with the unchanged `gpt-5.6-sol/max` review contract. Five Provider processes started; none were retried. Three cases completed with their expected gates: the synthetic money-conversion defect was BLOCK (105.797 s), and rounding and tenant-authorization controls were PASS (57.030 s and 43.126 s). The historical authorization-trailer leakage case timed out after 605.395 s without a final result. Its failure remains in the evidence and completion denominator.

This is limited qualification, not a passing four-case evaluation or a general latency/recall result. The subsequent timeout, installation and reporting repairs have deterministic regression coverage; the prior successful model records are identified by their original source SHA and unchanged contract. There is no comparable before/after speed measurement. The release manifest records the explicit scope limitation; incorrect completed outcomes, invalid evidence and source/approval mismatches still block distribution.

## Upgrade requirements and limits

- Use the fixed-version bootstrap with an existing supported host CLI and Python 3.8 or later. Keep its `QUALITY_REVIEW_ROLLBACK` path until the new CLI/plugin pair is verified.
- Coordinated CI requires a private persistent state root, runner policy version and a trusted effective configuration fingerprint. Existing caller workflows must grant read access to pull requests for publication-time freshness checks.
- Checkout filters or initialized submodules that cannot reproduce exact committed blobs are rejected. File locks serialize a shared local filesystem; distributed runners still need shared scheduling ownership.
- Formal publication retains the exact-source approval and verification checks. Strict model qualification remains the default; an explicitly authorized limited release records incomplete cases instead of claiming they passed.
