Explicit `fromBlock` or `toBlock` criteria are no longer propagated to the range filter, causing incorrect log ranges.

Review comment:

- [P1] Preserve supplied block bounds in GetLogs — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web3-log-range-scan/eth/filters/api.go:337-341
  When either bound is supplied, this code leaves that bound at its zero-value rather than copying `crit.FromBlock`/`crit.ToBlock`. For example, `eth_getLogs` with `fromBlock: 100` and no `toBlock` scans from block 0, while explicit `fromBlock` and `toBlock` values produce a `0..0` range. This breaks all queries with an explicit range bound; retain the defaults for nil values and assign `Int64()` in the non-nil cases.