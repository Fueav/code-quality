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
