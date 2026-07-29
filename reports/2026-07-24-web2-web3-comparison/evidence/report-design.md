# Report design and chart contract

## Audience and reading path

- Audience: code-quality maintainers deciding whether v0.1.1 is ready for broader adoption.
- Reading path: answer first, paired quality comparison, case evidence, operational readiness, limitations, recommendations.
- Delivery: one self-contained HTML report backed by the canonical `artifact.json` snapshot.

## Primary chart contract

- Question: does either review lane produce more complete, actionable reports across the six defect replays?
- Takeaway: both lanes hit every core defect; code-quality is more uniform on fix completeness, while the built-in lane has broader component coverage in the chain-reorg case.
- Chart family: grouped bar comparison.
- Grain: one row per case and review lane, 12 rows total.
- Encodings: case on x, 0–8 quality score on y, lane by color.
- Layout: single-root, content-width HTML chart.
- Source: `case_scores_sql` over `run_scores` in `scoring.sqlite`.

Latency remains a metric card because two lane medians do not justify a separate chart. The six-row comparison stays a table because its main value is exact case-to-case mapping and qualitative differences.

