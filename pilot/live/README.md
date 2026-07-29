# Live Review And Git-Forward Adjudication

This directory implements Route A: unattended daily review followed by weekly adjudication against later Git history. It does not install hooks, change CI, modify the review engine or policy, or ask maintainers to label findings.

## The tradeoff

Automatic Git-forward adjudication is weaker than human labeling. A later commit that independently fixes the reported root cause strongly confirms the finding, but code that remains unchanged cannot distinguish noise from a real unfixed defect. After 30 days without a nearby candidate, the system therefore emits the deliberately weak label `stale_probable_noise`, not a factual false-positive verdict.

The most valuable label is `confirmed_by_later_fix`: the product reported a defect at commit N and a later developer fixed the same problem at N+k. If separate evidence shows that the developer had not seen the report, set `independent_confirmation` in the adjudication record; that is the strongest evidence that the tool found the problem first.

## Architecture

`live_watch.sh` runs once per day and maintains an observed-HEAD watermark plus a pending review queue for every configured repository. It discovers new commits across merged history, ignores merge commits and commits without source changes, rejects changes over 3,000 added/deleted lines, and processes at most ten eligible commits per invocation. Filtered commits advance the watermark. A failed review is recorded once and remains pending for the next invocation.

Every product lane uses a binary built from the local `main` ref's current HEAD, never a pinned release tag. The build is cached by main commit under the external data directory. Each review uses a fresh detached shared clone, removes refs and remotes, copies target-reachable objects locally, and removes the shared-object alternate. Each `codex exec` receives a new `CODEX_HOME` containing only a temporary copy of `~/.codex/auth.json`, plus `--ignore-user-config --ephemeral`; the entire runtime directory and copied authentication file are deleted on exit. The prompt handles both `REVIEW_INVALID` repair and `REREVIEW_REQUIRED` counterexample review.

`live_adjudicate.py` runs weekly. For each non-terminal finding it scans later non-merge commits for diff hunks in the same file within ±10 lines. Each unseen candidate is paired once with the finding and sent through an isolated `codex exec -s read-only --output-schema` decision:

- `fixes` → `confirmed_by_later_fix` (terminal)
- `touches_only` → `superseded` (terminal and not counted as noise)
- `unclear` → remains `open`
- no candidate for at least 30 days → `stale_probable_noise` (terminal, weak noise mapping)

Terminal findings are never re-adjudicated. `summary.md` reports label shares, zero-finding rate, D1–D4 splits, and repository splits. For compatibility with the existing live-report vocabulary, `confirmed_by_later_fix` maps to adopted and `stale_probable_noise` maps to noise.

## Install

The defaults are daily at 02:17 and Mondays at 03:43 in the machine's cron timezone:

```sh
make live-install
```

Override either five-field schedule or the external data root at installation:

```sh
make live-install \
  LIVE_WATCH_CRON='11 1 * * *' \
  LIVE_ADJUDICATE_CRON='29 3 * * 1' \
  LIVE_DATA_ROOT="$HOME/AiProject/code-quality-live"
```

Installation is idempotent and changes only the marked `# BEGIN code-quality-live` crontab block. It copies the two runtime components into the external `bin/` directory, so later branch switches in this checkout do not remove the scheduled executable. It creates `config.json` only when absent, initially monitoring:

- `~/AiProject/agent_marketplace`
- `~/AiProject/general-agent-ai`
- `~/AiProject/code-quality` at local `main`

Edit `config.json` to change repositories or refs. The first watch of a repository initializes its watermark at the configured ref without reviewing history.

Remove only the cron entries while retaining all evidence:

```sh
make live-uninstall
```

Run either component manually:

```sh
pilot/live/live_watch.sh --once
python3 pilot/live/live_adjudicate.py
make live-test
```

## External data layout

All mutable data stays outside this repository, by default under `~/AiProject/code-quality-live/`:

```text
config.json
index.jsonl
state/<repo>.json
reviews/<repo>/<sha>/review-result.json
adjudications/<repo>/<sha>.json
summary.json
summary.md
snapshots/<ISO-year>-W<week>.md
bin/quality-review-main-<sha>
bin/live_watch.sh
bin/live_adjudicate.py
logs/live-watch.log
logs/live-adjudicate.log
logs/runs/<repo>/<sha>/codex.stderr.log
```

`index.jsonl` contains one line per completed or failed lane with repository, SHA, UTC time, duration, status, and either result counts or a failure reason. Review failures are not retried within the same invocation.
