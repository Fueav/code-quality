Removing the unique secondary sort key makes runner pagination unstable when primary sort values tie.

Review comment:

- [P1] Restore a unique pagination tie-breaker — /private/tmp/code-quality-corrected-fixtures.SJ13Cf/web2-list-pagination/models/actions/runner.go:256-256
  When a paginated runner listing contains equal `last_online` values (common for runners registered or heartbeating in the same second), this non-unique ordering lets the database return tied rows in different orders for successive offset queries. Consequently API and web page navigation can duplicate runners or omit others; the same issue also affects the name-based sort branches. Append `id` as a secondary ordering key.