## v0.5.4

- Make the production release gate priority-aware: P0/P1 findings return `BLOCK`; P2/P3-only reviews retain their findings as advisories and return `PASS`.
- Define the P0-P3 boundary in the Provider output schema and filter style, naming, preference, ordinary maintainability, and unsupported scale speculation from findings.
- Upgrade the detailed native result to schema v7 and `review-summary.json` to schema v2 with separate `blocking_issues` and `advisory_issues` counts.
- Split Markdown output into release-blocking issues and advisories while preserving one frozen Provider call and fail-closed `ERROR` behavior.
- Update the CLI, Codex and Claude Code plugins, GitHub reusable workflow, Jenkins guide, installation docs, and contract tests to v0.5.4.

Compatibility note: summary consumers must no longer treat every entry in `issues` as a blocker. Use `blocking_issues`, `advisory_issues`, or the per-issue priority.
