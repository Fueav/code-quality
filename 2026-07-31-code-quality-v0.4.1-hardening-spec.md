# Code Quality v0.4.1 Full Codex Review Specification

Status: approved and authorized for release by the owner on 2026-07-31

## Scope

This Codex-focused patch release replaces the capability-restricting review wrapper with one full Codex Agent review, freezes its raw evidence, and applies only deterministic post-classification. Claude Code compatibility work remains deferred. The product stays report-only and makes exactly one semantic model call.

## Discovery contract

- The default provider is ordinary `codex exec`, not `codex exec review`.
- The default model is `gpt-5.6-sol` with `max` reasoning. Explicit CLI overrides remain available.
- Codex inherits the invoking user's normal authentication, `CODEX_HOME`, configuration, rules, Skills, and native tools.
- The isolated checkout has a session-owned, read-only marker outside the Git root while Codex is running. The executor passes that marker's absolute path to the child process tree as `CODE_QUALITY_NATIVE_DISCOVERY_MARKER`; this is the only product-owned environment addition. Before honoring any requested repository, the CLI checks the inherited marker, then the canonical active Git root's parent marker and the requested repository marker as fallbacks. The shipped Skill performs the same preflight. This carries discovery-child identity across `chdir` and symlinked entry without treating a repository-owned file as the session marker, preventing accidental cross-repository self-recursion without changing other Skills, rules, configuration, or tools. The executor removes the marker after the child exits.
- The isolated committed checkout runs with `workspace-write` and shell network access enabled through `sandbox_workspace_write.network_access=true`.
- The model receives the complete target checkout and Git context available from that checkout.
- The only default review instruction is: `Review the changes introduced by <target> relative to <base> for actionable defects.`
- A user-supplied `--goal` may be appended as user context, but the product adds no rubric, risk directions, output schema, defect hint, evidence packet, verifier, retry, or second model call.
- The invocation must not pass `--ignore-user-config`, `--ignore-rules`, `--ephemeral`, `read-only`, or the `review` subcommand.

## Raw evidence freeze

- After the direct Codex process exits, the executor drains its stdout and stderr pipes to EOF with a bounded fail-closed wait so descendant writers cannot mutate evidence after hashing. Before classification runs, the product validates the final message, JSONL stdout, and stderr as regular non-symlink files. The bounded final message is retained byte-for-byte for classification, while potentially large JSONL and stderr artifacts are streamed to their hashes without a total-size parser limit.
- For each present artifact it copies one verified regular-file descriptor into a newly created exclusive inode whose pathname is `0400` from first exposure, while the creator retains its original writable descriptor. It bounds only the captured final message, rehashes the source descriptor, syncs the snapshot, atomically replaces the raw pathname, and retains the snapshot descriptor through manifest publication.
- The published schema fixes exactly one ordered entry for the final message, JSONL stdout, and stderr; every present artifact requires its digest.
- Present raw artifacts are never path-writable during snapshot construction, old source descriptors no longer reference their published inodes, and every pathname, size, digest, and mode is revalidated through the retained descriptor before and after hashing, followed by a final path-only sweep immediately before publication. Paths recorded as absent are also revalidated during both evidence validation passes so late creation fails closed. The manifest temporary inode is likewise created as `0400` and synced through its retained creator descriptor; that descriptor remains open while its temporary pathname and installed hard link are each bound back to the verified inode, size, mode, and digest.
- Classification consumes the exact frozen final-message bytes already held in memory; it must not rewrite or replace the raw artifact.
- Usage metrics are decoded from the retained frozen JSONL descriptor, never by reopening its writable parent directory by pathname.
- The session summary exposes the freeze manifest path, and the session remains retained after checkout cleanup.

## Thin deterministic classification

- The frozen final message is treated as one document. After line-ending normalization and outer whitespace trimming, only the exact case-sensitive documents `No findings.`, `No actionable findings.`, and `No actionable defects found.` become `PASS`.
- A failed native process or a missing or empty final message becomes `INCOMPLETE`.
- Every other nonempty final message becomes `MANUAL_REVIEW`. The result deliberately leaves structured `findings` and `adapter_drops` empty and directs the reader to the frozen `native-review.txt`, which remains the review authority.
- Classification does not parse Markdown, reconstruct finding bodies, interpret code locations, map paths, build a CommonMark AST, perform a model call, or edit the frozen source. This prevents formatting drift from hiding native findings and keeps classification smaller than the native review capability it follows.

## Existing hardening retained

- The recursion marker check resolves the active Git root canonically, including symlinked working-directory entry, before inspecting the session-owned marker outside the checkout.
- `--base` and `--target` are supplied together. `--diff-reason` is optional for an explicit range and defaults to `explicit_commit_range`.
- Each run retains duration and token metrics by streaming complete JSONL output while preserving a per-event size bound. Missing or all-zero usage remains explicitly unavailable in both runtime behavior and the published metrics schema.
- `make release-check` covers Go, root qualification, live, mining, vet, formatting, and diff checks without model calls.

## Non-goals

- No Claude Code execution-path work.
- No prompt framework, review-direction selector, verifier, rereview, retry, or automatic source exclusion.
- No claim that three public historical probes establish population-level review quality.
- No CI blocking or source-repository modification.

## Acceptance evidence

1. RED tests prove the old invocation suppresses normal Codex context, raw evidence has no pre-classification freeze, document-level `MANUAL_REVIEW` was previously invalid, and the report could incorrectly claim no findings for an unparsed nonempty review.
2. Focused tests and `make release-check VERSION=v0.4.1 VERIFY_COMPARE_REF=v0.4.0` pass from the clean release worktree.
3. A deterministic fake Codex smoke proves one call, full invocation controls, frozen hashes, metrics, exact-sentinel classification, retained reports, and checkout cleanup.
4. A real candidate-binary smoke uses the release contract and preserves raw evidence before adjudication.
5. A fresh full-capability Codex review of the release diff has no unresolved actionable finding.
6. Release assets are built from the tagged commit, checksummed, published, downloaded, and version-verified.
