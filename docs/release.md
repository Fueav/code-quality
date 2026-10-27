# Release and upgrade

Distribution has one entrypoint: `make dist VERSION=vX.Y.Z VERIFY_COMPARE_REF=<approved-baseline>`.
It requires a clean checkout and builds from the frozen Git commit. Before verification and again afterward, it checks tracked file bytes, types and executable modes against committed blobs; hidden index flags and clean-filter drift cannot qualify different working source. The full Harness release verification invokes the native release gate once. The artifact manifest binds the source commit/tree, comparison commit, version, verification evidence, model qualification, toolchain and asset checksums.

## Final approval

Implementation authorization is not evidence that an owner has read the final code. After tests and independent review, present the exact final SHA, baseline, risks and CI results to the owner. Only after that specific approval may the release operator set:

```sh
export AI_BOUNDARY_APPROVED=1
export AI_BOUNDARY_APPROVAL_EVIDENCE='owner-request:<reference-to-final-SHA-confirmation>'
export RELEASE_APPROVED_HEAD_SHA='<approved-final-full-SHA>'
export RELEASE_APPROVED_COMPARE_SHA='<approved-baseline-full-SHA>'
# Also set the three frozen evaluation inputs described below.
make dist VERSION=vX.Y.Z VERIFY_COMPARE_REF="$RELEASE_APPROVED_COMPARE_SHA"
```

An actual GitHub approval can use the corresponding `github:` evidence. Never invent an approval, use a bot approval as a human review, or reuse approval after the source SHA changes. `candidate` CI proves its checks only; it does not claim human review or release readiness. The repository's release dispatch likewise requires a real owner to confirm the specified final source and baseline. Branch protection is managed by repository owners separately.

Candidate, human approval and explicit release dispatch use separate workflows with unconditional jobs, so a skipped job from another event cannot stand in for successful evidence. PR approval runs the trusted base's helper and binds actual human owner/maintainer review to the current head and matching candidate evidence. On the initial rollout, the base has no helper: approval intentionally reports HOLD and never executes an unapproved head helper as a fallback. Bootstrap requires the owner to review the exact final source and baseline and provide specific `owner-request:` confirmation. Once installed in trusted main, subsequent PR approval events can use the trusted helper. A single owner may approve a final release through that confirmation or the explicit reviewed-source dispatch; no second reviewer or server policy is silently imposed.

If Harness is not installed, use the exact version from `harness/harness.lock` through the existing explicit `make install-tools` entrypoint or an already verified `HARNESSCTL_BIN`. Do not skip a gate because a dependency is missing. Tests and frozen-fixture checks do not establish model recall or latency; retain the qualification scope and any unavailable real-model evidence in release notes.

## Frozen model qualification

Formal distribution requires all three inputs before full verification starts:

```sh
export RELEASE_EVAL_SUITE='/absolute/path/to/reviewed-suite.json'
export RELEASE_EVAL_EVIDENCE='/absolute/path/to/frozen-model-evidence/manifest.json'
export RELEASE_EVAL_CONTRACT='contract-v1:sha256:<64-lowercase-hex-digits>'
```

The third value is a digest string. The owner must review the frozen suite labels and its explicit `review_config` (`host`, `model`, `reasoning_effort`), the bundle provenance and the expected contract. See [model evidence schema](../pilot/live/README.md). These inputs are qualification data, never instructions to run a Provider. The current host CLI is built once from the frozen source, then its read-only `plan` derives the FULL, `production-ci` contract at the approved base/head. The contract must match the explicit digest before the frozen verifier checks every model record with this built CLI. Missing inputs, structural fixtures, old contracts, incomplete runs and invalid evidence HOLD; a failed expected gate BLOCKs. Only PASS permits the remaining platform builds and replacement of `dist/`. Candidate CI does not require or claim this qualification.

The release dispatch accepts suite/evidence paths committed in the reviewed source SHA and the expected digest. Local release may use absolute external frozen bundle paths when the owner has reviewed those inputs. The result is saved as `.artifacts/release-qualification.json` and included in both `dist/release-qualification.json` and the release manifest with suite/evidence hashes. This establishes only the frozen suite's expected gate outcomes; it does not establish exact incident-finding recall or a general speed improvement. Delivery tests use explicitly structural fake qualification responses and cannot serve as a real release bundle.

## Publish the verified assets

Verify `dist/release-manifest.json` names the approved source and version. Create the tag at that source SHA and publish the exact `dist/` assets, including `checksums.txt`, `release-manifest.json`, `release-evidence.json` and `release-qualification.json`. Do not rebuild or change the tag after approval. If a required check fails, an artifact changes, or the source moves, stop publication and obtain valid evidence for the final candidate.

## Recoverable upgrades

Use the fixed release bootstrap for the selected host. Python 3 and the host CLI must already be available. CLI file replacement is atomic on its filesystem; CLI and host-plugin changes form a recoverable transaction, not a single atomic operation across both stores.

The bootstrap retains only this product's previous CLI/plugin state and emits `QUALITY_REVIEW_ROLLBACK=<absolute-path>`. Preserve that path and receipt until the new installation is verified. A failed upgrade attempts offline restoration and reports incomplete restoration explicitly. The rollback refuses to overwrite an installation that has since changed to another version; resolve that condition manually rather than forcing it.

After upgrade, verify the actual command path, exact CLI version and product plugin descriptor version. A safe smoke consists of version/help and local validation of known frozen result fixtures; it does not start a paid Provider review. Retain the release URL, manifest checksum, installed paths, version checks and rollback command for each machine. Installation on one machine does not prove installation on another.
