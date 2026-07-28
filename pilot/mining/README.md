# Historical defect mining

This directory turns fix-history mining into a reproducible, read-only pipeline. Prompt and output schema are versioned as `trace-prompt.md` and `mining-result.schema.json` (`1.0.0`).

## Commands

```bash
pilot/mining/select_repos.py /Users/chris/AiProject --limit 5
pilot/mining/prefilter.sh /path/to/repo
pilot/mining/trace.sh /path/to/repo <fix_sha>
MINING_RUN_DIR=/absolute/run/path pilot/mining/mine_repo.sh /path/to/repo
pilot/mining/validate_dataset.py pilot/dataset/v2/targets.json
make mining-test
```

`prefilter.sh` keeps fix-like commits only when their diff touches a source-extension allowlist outside docs, CI, tests, fixtures, examples, samples, and configuration templates. `trace.sh` performs exactly one ephemeral `codex exec` in a read-only sandbox and a fresh `CODEX_HOME` containing only a temporary authentication-file copy. It returns one schema-constrained result on stdout and removes the copied authentication file on exit.

`mine_repo.sh` clones the committed source history into its run directory, removes the remote, runs at most four traces concurrently, and writes candidates, per-trace events/costs, successful JSON results, filtered targets, rejection statistics, and a cost summary. Set `MINING_WORKERS` from 1 through 4. A fixed `MINING_RUN_DIR` is resumable: valid result files are skipped and failed or interrupted candidates are retried.

`aggregate.py` admits only in-scope, material, statically detectable results whose introducing commit resolves locally and changes at most 3,000 text lines. Results are grouped by full introducing commit. All fix-level evidence remains under `defects`; the highest-difficulty record supplies the target-level `defect_class` and its one-line basis.

Generated runs live under the ignored `pilot/mining/runs/` directory. No product or builtin review lane is part of this workflow.
