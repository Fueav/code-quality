The change causes commit totals and pagination to disagree with date-filtered API results.

Review comment:

- [P1] Restore time filters in commit counting — /private/tmp/code-quality-corrected-fixtures.SJ13Cf/web2-query-count-filter/modules/gitrepo/commit.go:35-35
  When the commits API is called with `since` or `until`, this count now includes every commit while the subsequent `CommitsByRange`/`CommitsByFileAndRange` query still applies the date bounds. This makes `X-Total`, pagination links, and `X-HasMore` incorrect; for a date range with no matching path commits it can also bypass the intended 404 check and return an empty 200 response.