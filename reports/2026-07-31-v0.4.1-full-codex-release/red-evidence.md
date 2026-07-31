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
