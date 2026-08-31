# Test dependencies

`harness/dependencies.json` declares PostgreSQL/Redis test requirements and budgets; the exact engine in `harness/harness.lock` owns execution and cleanup. `make test` and native Harness test gates use this contract. No cleanup daemon is installed.

| Entry | Behavior |
| --- | --- |
| `scripts/with_test_resources.sh -- COMMAND ...` | Share compatible services across this repository's worktrees; allocate new data for this command |
| `scripts/with_test_resources.sh --mode fresh -- COMMAND ...` | Use dedicated services and remove their containers, network and owned volumes afterwards |
| `scripts/harnessctl.sh resources status` | Show active runs, pending cleanup and storage usage without printing credentials |
| `scripts/harnessctl.sh resources gc` | Retry dead-run cleanup and evict expired/excess idle environments |
| `scripts/harnessctl.sh resources gc --all-idle` | Also remove all unused owned service environments and their volumes |

Each command receives its own `TEST_DATABASE_DSN` / `DATABASE_DSN`, `TEST_REDIS_ADDR`, `TEST_REDIS_DB`, `TEST_REDIS_USERNAME`, `TEST_REDIS_PASSWORD`, and application `REDIS_*` / `CACHE_REDIS_*` settings. Tests must consume the complete connection contract. Never hard-code DB 0 or a shared application database. PostgreSQL migrations run in each command's new database. Application builds and ports remain worktree-specific.

The service key combines the common Git repository, local Docker daemon, actual image IDs and service configuration, not the worktree name or application commit. The engine pulls a declared image only if it is absent locally. Global database/server mutations, Redis global state, and persistence/recovery tests require dedicated environments. Redis logical DBs share a process and are not an untrusted-code security boundary.

Normal success, failure and INT/TERM remove task databases/roles and Redis data after stopping task writers. Cleanup failure makes verification fail and blocks new allocations until reconciled. Process identity and ownership records survive outside worktrees; a later invocation cleans confirmed dead runs after kill -9 or reboot. An old cleanup cannot acquire a newly allocated run's identity. Unknown ownership is preserved and reported.

The initial configurable limits are four concurrent runs, two service environments, one idle environment, one hour idle eligibility, 8 GiB project test storage and 5 GiB free-space reserve. These are admission/monitoring budgets, not storage hard quotas or measured performance guarantees. WAL, service log budgets, VM free space and host free space are included in monitoring. Pressure rejects new work and can reclaim unused test environments; aggregate usage does not justify killing other active tasks. Idle expiry is evaluated on the next invocation; no background timer is promised. No business or manually created developer volume is pruned.

Debug data is removed by default. `--retain-on-failure SECONDS` explicitly keeps a failed command's data with stopped writers; count, duration and storage limits still apply. Use the owned service's administrative connection for inspection, then let GC expire it. The engine never prints generated credentials. Docker Desktop host allocation can lag file deletion inside its VM; `status` separates actual Docker.raw allocation from data usage where available.

Template upgrades must manually resolve `harness/dependencies.json` and the project's test entrypoint. Set `enabled: false` only for service-free projects; adapt legacy clients before sharing, preserve native gates and fresh release validation, and prove real database/Redis tests ran rather than skipped. `docker-compose.dev.yml` remains a separate persistent developer environment. Before adopting its named mounts in an existing environment, preserve the original volumes and explicitly map or migrate their data: Compose can otherwise attach a new empty volume. Never use `down -v` or prune for this cutover. [Compose volume ownership](https://docs.docker.com/reference/compose-file/volumes/) applies independently of disposable task cleanup.
