# Code Quality v0.4.1 Full Codex Review Specification

Status: approved and authorized for release by the owner on 2026-07-31

## Scope

This Codex-focused patch release replaces the capability-restricting review wrapper with one full Codex Agent review, freezes its raw evidence, and applies only deterministic post-classification. Claude Code compatibility work remains deferred. The product stays report-only and makes exactly one semantic model call.

## Discovery contract

- The default provider is ordinary `codex exec`, not `codex exec review`.
- The default model is `gpt-5.6-sol` with `max` reasoning. Explicit CLI overrides remain available.
- Codex inherits the invoking user's normal authentication, `CODEX_HOME`, configuration, rules, Skills, and native tools.
- The isolated checkout has a session-owned, read-only marker outside the Git root while Codex is running. Before honoring any requested repository, the shipped Skill and CLI resolve the canonical active Git root and inspect only its parent marker; they also check the requested repository. This catches a checkout entered through a symlink without treating a repository-owned file as the session marker. It prevents accidental cross-repository self-recursion without changing the Codex environment policy, other Skills, rules, configuration, or tools. The executor removes the marker after the child exits.
- The isolated committed checkout runs with `workspace-write` and shell network access enabled through `sandbox_workspace_write.network_access=true`.
- The model receives the complete target checkout and Git context available from that checkout.
- The only default review instruction is: `Review the changes introduced by <target> relative to <base> for actionable defects.`
- A user-supplied `--goal` may be appended as user context, but the product adds no rubric, risk directions, output schema, defect hint, evidence packet, verifier, retry, or second model call.
- The invocation must not pass `--ignore-user-config`, `--ignore-rules`, `--ephemeral`, `read-only`, or the `review` subcommand.

## Raw evidence freeze

- After the direct Codex process exits, the executor drains its stdout and stderr pipes to EOF with a bounded fail-closed wait so descendant writers cannot mutate evidence after hashing. Before any parser or classifier runs, the product validates the final message, JSONL stdout, and stderr as regular non-symlink files. The bounded final message is retained byte-for-byte for classification, while potentially large JSONL and stderr artifacts are streamed to their hashes without a total-size parser limit.
- For each present artifact it copies one verified regular-file descriptor into a newly created exclusive inode, bounds only the captured final message, and rehashes the source descriptor before installation. It syncs the snapshot, changes it to `0400`, syncs the final metadata, atomically replaces the raw pathname with that snapshot, and retains the snapshot descriptor through manifest publication.
- The published schema fixes exactly one ordered entry for the final message, JSONL stdout, and stderr; every present artifact requires its digest.
- Present raw artifacts become read-only before the manifest is published, old writable descriptors no longer reference their published inodes, and every pathname, size, digest, and mode is revalidated through the retained descriptor immediately before publication. The manifest itself is set to `0400` and synced through its temporary descriptor; that descriptor remains open while its temporary pathname and installed hard link are each bound back to the verified inode, size, mode, and digest.
- Classification consumes the exact frozen final-message bytes already held in memory; it must not rewrite or replace the raw artifact.
- Usage metrics are decoded from the retained frozen JSONL descriptor, never by reopening its writable parent directory by pathname.
- The session summary exposes the freeze manifest path, and the session remains retained after checkout cleanup.

## Thin deterministic classification

- The classifier accepts the previously observed native bullet format and ordinary CommonMark `-`, `*`, `+`, `N.`, and `N)` Agent finding markers, including title-only or whole-finding bold emphasis, balanced or backslash-escaped punctuation in Markdown link destinations, and the observed form where the finding title itself is the location link. CommonMark's zero-to-three-space top-level indentation is accepted; deeper indentation than the current finding remains body content. Fenced code never becomes a candidate, while an indented fence belonging to an active finding remains in that finding's body; a closing fence may use up to three additional spaces relative to its opening fence's list-container indentation, and fenced text never resumes a body after trailing assessment begins. If a candidate precedes the selected native heading or grammars are otherwise mixed in a way that cannot be parsed losslessly, classification fails closed instead of returning a partial finding set.
- Each recognized candidate is either retained inside the trusted changed-file scope or recorded as an indexed adapter exclusion.
- Top-level explicit no-finding text, either standalone or immediately below the first top-level recognized findings heading after optional introductory text, may become `PASS`. Zero-to-three-space CommonMark indentation remains top-level, while four-space code examples are never result sentinels or container headings.
- Any priority candidate before that sentinel makes the output contradictory rather than allowing a later section to erase it.
- Explicit no-finding text followed by any nonblank tail is not accepted as `PASS`.
- Top-level text after a structured finding is not appended to its body, a trailing no-finding sentinel is contradictory in either accepted grammar, and indented priority text or nested bullets remain body text.
- Empty, ambiguous, contradictory, or unrecognized non-finding prose becomes `INCOMPLETE`, never `PASS`.
- If candidates exist but none map to trusted changed files, the result is `INCOMPLETE`.
- Classification performs no model call and does not edit the frozen source.

## Existing hardening retained

- Canonically equivalent macOS paths map to the same isolated checkout, including different casing on a case-insensitive volume. Existing entries recover their filesystem spelling, while a uniquely case-equivalent missing or deleted path maps back to the trusted changed-path spelling only when a read-only probe on the checkout's own filesystem proves it is case-insensitive; the probe never crosses a mount-device boundary. Existing and dangling symlinks are resolved component-by-component in filesystem traversal order before containment; a parent traversal after a missing component and traversal through a non-directory component are rejected, so chained targets cannot be cleaned into a changed file or back into the checkout. A changed in-repository symlink keeps its logical path, while an unchanged alias to a changed or deleted target falls back to the canonical changed path. Candidate paths containing an unresolved `..` component are rejected before cleaning or symlink resolution.
- `--base` and `--target` are supplied together. `--diff-reason` is optional for an explicit range and defaults to `explicit_commit_range`.
- Each run retains duration and token metrics by streaming complete JSONL output while preserving a per-event size bound. Missing or all-zero usage remains explicitly unavailable in both runtime behavior and the published metrics schema.
- `make release-check` covers Go, root qualification, live, mining, vet, formatting, and diff checks without model calls.

## Non-goals

- No Claude Code execution-path work.
- No prompt framework, review-direction selector, verifier, rereview, retry, or automatic source exclusion.
- No claim that three public historical probes establish population-level review quality.
- No CI blocking or source-repository modification.

## Acceptance evidence

1. RED tests prove the old invocation suppresses normal Codex context, raw evidence has no pre-classification freeze, ordinary Agent output is not losslessly classified, and ambiguous prose can become `PASS`.
2. Focused tests and `make release-check VERSION=v0.4.1 VERIFY_COMPARE_REF=v0.4.0` pass from the clean release worktree.
3. A deterministic fake Codex smoke proves one call, full invocation controls, frozen hashes, metrics, classification, retained reports, and checkout cleanup.
4. A real candidate-binary smoke uses the release contract and preserves raw evidence before adjudication.
5. A fresh full-capability Codex review of the release diff has no unresolved actionable finding.
6. Release assets are built from the tagged commit, checksummed, published, downloaded, and version-verified.
