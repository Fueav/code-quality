The patch swaps the added, deleted, and updated counters, causing inaccurate release synchronization statistics.

Review comment:

- [P2] Report release-sync counts in correct categories — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web2-release-statistics/modules/repository/repo.go:236-236
  When a sync inserts, deletes, or updates tags, this assigns each count to the wrong statistic: `added` receives deletions, `deleted` receives updates, and `updated` receives insertions. The following trace log therefore reports misleading release-sync results, which breaks operational diagnostics and any log-derived release statistics.