# v0.4.1 full Codex review RED evidence

Recorded: 2026-07-31

Command:

```text
go test ./internal/codexreview ./cmd/quality-review .
```

Observed failures before implementation:

- `internal/codexreview` did not compile because the session layout had no `NativeFreezePath`.
- `cmd/quality-review` returned an empty `freeze_path` even though metrics were retained.
- The shipped Skill still promised `codex exec review` and did not expose the raw freeze path.

These failures prove that v0.4.0 plus the earlier hardening commits did not implement the owner-approved `full Codex discovery -> immutable raw evidence -> deterministic classification` release contract.

## Candidate review RED

The first real `quality-review v0.4.1` candidate smoke completed one full `gpt-5.6-sol/max` call and froze all three raw artifacts. It correctly returned `MANUAL_REVIEW` with three actionable release findings:

- standalone or localized strict finding lists were rejected by an English-introduction check;
- angle-bracket Markdown destinations used for paths containing spaces were not parsed;
- the freeze manifest was closed but not atomically installed and synced before artifact locking.

Regression tests for the first two parser findings failed before their fixes with `agent finding appears without a findings introduction` and `agent finding has no Markdown code location`. The original candidate smoke remains diagnostic evidence and is not treated as release acceptance.

## Second candidate review RED

The second fresh `gpt-5.6-sol/max` full-Agent smoke completed one model call, retained 4,954,785 input tokens and 42,653 output tokens, froze all three raw artifacts, and classified all three reported candidates without an adapter drop. It found:

- the installed Skill could recursively invoke `quality-review` inside the child discovery run;
- an explicit no-findings sentinel could ignore arbitrary trailing prose and incorrectly become `PASS`;
- the legacy heading alone selected the bare-path grammar and rejected an ordinary Markdown-link finding.

The new contract tests failed before implementation: the recursion marker/helper did not exist, the Skill lacked a child bypass, and the two parser examples reproduced the false pass and false incomplete paths. The second candidate smoke remains diagnostic evidence and is not release acceptance.

## Third candidate review RED

The third fresh full-Agent smoke completed one `gpt-5.6-sol/max` call in 761,211 ms with 3,884,200 input tokens and 35,585 output tokens. The independently recomputed SHA-256 values matched the freeze manifest for all three `0400` raw artifacts, and deterministic classification retained all four candidates with zero adapter drops. The review found:

- tests launched inside the marked discovery child inherited the recursion marker and rejected their own top-level fake smoke;
- a terminal `No findings.` below an ordinary findings heading was not recognized;
- an angle-bracket Markdown destination containing parentheses was not recognized;
- the metrics schema permitted available all-zero usage even though runtime rejected it.

All four cases reproduced before implementation. The marked full `go test ./...` path, headed no-findings parser cases, parenthesized angle-bracket location, and schema constraints now have dedicated regression coverage. This third candidate smoke remains diagnostic evidence and is not release acceptance.

## Fourth candidate review RED

The fourth fresh full-Agent smoke completed one `gpt-5.6-sol/max` call in 996,236 ms with 7,259,799 input tokens and 42,787 output tokens. Independent hashes again matched all three `0400` frozen raw artifacts, and classification retained all three candidates with zero adapter drops. The review found:

- Codex `shell_environment_policy.include_only` can filter the inherited recursion environment variable;
- ordinary Markdown destinations with balanced parentheses, such as Next.js route groups, were rejected;
- the freeze schema did not require each artifact identity exactly once or require a digest when present.

Local Codex 0.145.0 plus the official Codex configuration contract confirmed that environment `set` values are still subject to the final `include_only` filter. The implementation therefore removes the environment injection entirely and uses a session-owned, read-only marker outside the Git root; the Skill recognizes it and the CLI independently blocks recursion. Dedicated marker lifecycle, balanced-link, and schema-invariant tests now cover all three cases. The fourth candidate smoke remains diagnostic evidence and is not release acceptance.

## Fifth candidate review RED

The fifth fresh full-Agent smoke completed one `gpt-5.6-sol/max` call in 577,688 ms with 2,411,895 input tokens and 32,519 output tokens. Independent hashes matched all three `0400` frozen raw artifacts, the recursion marker was absent after process completion, and classification retained all three candidates with zero adapter drops. The review found:

- a recognized findings heading after introductory prose did not enable a terminal no-findings result;
- canonical symlink validation replaced an in-repository changed symlink's logical path before scope membership;
- unindented trailing sections, including a contradictory no-findings sentinel, were appended to the prior finding body.

All three cases reproduced before implementation. The classifier now locates the first recognized findings container after optional introduction, separates indented list-body text from top-level trailing sections, rejects trailing no-findings contradictions, and uses canonical paths only for containment while retaining the logical in-repository path for changed-file matching. The fifth candidate smoke remains diagnostic evidence and is not release acceptance.

## Sixth candidate review RED

The sixth fresh full-Agent smoke completed one `gpt-5.6-sol/max` call in 608,259 ms with 2,840,756 input tokens and 25,801 output tokens. Independent hashes matched all three `0400` frozen raw artifacts and the recursion marker was absent after completion. The tool correctly returned `INCOMPLETE`: its raw output contained three real findings, but a priority marker quoted inside an indented body exposed an unrecognized classification edge. The raw review found:

- a later headed no-findings sentinel could erase an earlier structured candidate;
- an unchanged symlink alias to a changed canonical target could hide the finding;
- native-format trailing no-findings text was not contradictory.

All three reported cases plus the indented priority-reference parser failure reproduced before implementation. The no-findings pre-scan now rejects preceding candidates, logical symlink paths are preferred only when changed and otherwise fall back to a changed canonical target, both grammars reject trailing no-findings contradictions, and indented body text may quote priority markers without becoming a header. The sixth candidate smoke remains diagnostic evidence and is not release acceptance.
