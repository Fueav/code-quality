# Live review and frozen evaluation evidence

`live_watch.sh` watches committed changes and invokes the shipped `quality-review run-codex --execution-profile production-ci` directly. It uses the runner user's existing login and PATH. There is no outer review agent, credential copy, temporary `CODEX_HOME`, or `prepare/finalize` loop. Optional `model` and `reasoning_effort` config fields override the CLI defaults.

The watcher keeps the existing daily watermark, source-change filter, 3,000-line ceiling, and ten-commit budget. Each sample uses a target-only detached clone with no remotes or shared-object alternate, preserving historical blindness. The product binary is built from local `main` and cached by source commit. The clone and CLI evidence are retained outside the reviewed repository so an interrupted Restricted session can still be resumed.

Only schema-v10 results accepted by the CLI's offline `validate` enter completed samples. `PASS` exits 0; a valid `BLOCK` exits 3 and also counts as completed. `ERROR`, unsupported schemas, inconsistent exits, and validation failures count as failed. `RESTRICTED_RETRYABLE` and `MANUAL_REQUIRED` retain `operator-required.json`; later scheduled runs wait without starting Native again. Once an operator has resumed and published the retained session, the watcher validates and adopts that exact result without another Provider call. The watcher does not automatically expand scope or reset a review lineage.

Before starting the CLI, the watcher exclusively creates `attempt-intent.json` and syncs both the file and parent directory. If the adapter or archive step is interrupted, the next run checks that retained attempt and adopts a valid publication using only local validation. A saved process completion preserves its known exit code and wall duration during archive replay. Missing or undecodable output is `WAITING_OPERATOR`; it cannot silently cause another paid invocation. Retained session pointers remain available even after adoption so a failed watermark write cannot restart Native.

Each attempt retains `stdout.json`, progress/diagnostic `stderr.jsonl`, native evidence, and one `record.json` with the exact result digest, contract, version/model/effort/scope, outcome, total CLI wall time, and per-attempt stage metrics. Failed retained sessions preserve their available stage metrics and contract identity. `index.jsonl` projects these same records for aggregation. Missing duration/token/cache data remains `null`, never zero. Native and Restricted durations include their evidence-freezing work. Wall time includes the CLI transaction; it does not measure CI queue time or preparing the target-only clone. A manually recovered session has unknown total wall time across invocations.

## Install and operate

Install only when scheduling has been authorized:

```sh
make live-install
make live-uninstall
```

Installation updates only the marked code-quality crontab block. Defaults are daily at 02:17 and Mondays at 03:43 in the machine's cron timezone. Override `LIVE_WATCH_CRON`, `LIVE_ADJUDICATE_CRON`, and `LIVE_DATA_ROOT` through Make. All mutable data defaults to `~/AiProject/code-quality-live/`; the installer copies the three runtime files into its `bin/` directory.

```sh
pilot/live/live_watch.sh --once
python3 -B pilot/live/live_adjudicate.py
python3 -B pilot/review_evidence.py summarize --records /absolute/live-root/index.jsonl
```

The summary groups execution sample count, failures, wall/native/restricted P50 and nearest-rank P95, stage process/freezing durations, and tokens by tool version, contract, provider, model, effort, and scope. It counts each session once using its latest cumulative metrics; independent sessions with the same review key retain their separate costs. `logical_reviews` separately counts unique review keys, and failed observations remain visible. Waiting checks do not add execution samples. Unknown metrics include their own denominator. A live summary without trusted severe/clean labels reports effectiveness as `UNKNOWN`.

## Git-forward labels

`live_adjudicate.py` accepts current findings with their stable ID and exact file/range. Current native findings do not contain D1–D4 labels, so the dimension is `unknown`. Old result/state schemas require explicit migration and are excluded from completed denominators.

Weekly adjudication scans later non-merge commits touching the same file within ten lines. A fresh read-only Codex call labels each unseen pair `fixes`, `touches_only`, or `unclear`; it uses the existing login without copying authentication files. `fixes` becomes `confirmed_by_later_fix`, `touches_only` becomes `superseded`, and a 30-day absence of candidates becomes `stale_probable_noise`.

A process lock prevents concurrent weekly runs. Each pair has a persisted intent before invocation and each completed decision is saved immediately. An interrupted or failed pair retains `pending_adjudication` and returns `HOLD`; an operator must inspect and explicitly authorize its handling. The scheduler does not clear it or repeat a possibly paid call automatically.

These labels are observational: code remaining unchanged is not proof of a false positive. Line movement and renames can defeat the simple prefilter. Neither a weak noise label nor this weekly summary qualifies a release. Preserve human-confirmed incidents and clean fixes as frozen benchmark cases instead.

## Offline contract-bound benchmark check

Retain the existing historical corpus and deterministic tests; do not rerun mining or retired synthetic experiments for every change. The historical structure check stays offline:

```sh
python3 -B pilot/mining/validate_dataset.py pilot/dataset/v2/targets.json
```

That verifies dataset structure and size, not model effectiveness. For actual model qualification, a trusted operator supplies a frozen suite with exact `id`, `base`, `target`, and expected `PASS`/`BLOCK` for every case. The suite must contain both severe and clean cases. An evidence manifest has this shape:

```json
{
  "schema_version": 1,
  "kind": "model",
  "contract_digest": "contract-v1:sha256:<64 hex characters>",
  "suite_sha256": "sha256:<hash of suite bytes>",
  "runs": [{"case_id": "case-id", "record": {"path": "case/record.json", "sha256": "sha256:<hash of record bytes>"}}]
}
```

The suite format is `{"schema_version":1,"cases":[{"id":"case-id","base":"<full SHA>","target":"<full SHA>","expected_result":"BLOCK"}, ...]}`. All referenced result and metric artifacts must reside in the frozen evidence bundle. Hashes bind the supplied artifacts; they do not authenticate the person providing labels or prove the provenance of arbitrary user-created files. Keep the suite and evidence under the CI's trusted review/approval process. Test fixtures and mock CLI output are not real-model evidence.

Formal `dist` qualification also requires owner-reviewed suite `review_config` with `host` (`codex` or `claude-code`), `model`, and `reasoning_effort`. The release task uses those trusted settings with the current built CLI's read-only plan to derive the expected contract. Full qualification requires `FULL` scope and `production-ci`. The trusted suite is not copied from a model-produced evidence manifest; test fixtures remain offline tests even when their synthetic fields resemble model evidence.

```sh
python3 -B pilot/review_evidence.py verify \
  --suite /trusted/eval/suite.json --evidence /trusted/eval/evidence.json \
  --expected-contract 'contract-v1:sha256:<current contract digest>' \
  --quality-bin /absolute/path/to/quality-review
```

Verification invokes only local `validate`. Missing evidence, structural-only fixtures, stale contracts, changed hashes, missing cases, `ERROR`, or absent actual stage timings return `HOLD` and exit 1. A known case whose result changed returns `BLOCK`. `PASS` means only that every case in this exact frozen suite matches; it is not population-level superiority. The same output includes quality counts and latency distributions, so another metrics pipeline is unnecessary. Restricted-policy changes can reuse frozen candidate evidence for a separately declared restricted-only experiment; they must not label that experiment as a full-review qualification.

The suite labels the expected `PASS`/`BLOCK` gate, not an exact defect match. Summary quality includes `label_scope: EXPECTED_GATE_ONLY`; `severe_retained` counts severe cases with a blocking gate. It does not prove the original incident was found, because an unrelated blocking finding can produce the same gate. Exact defect recall needs separately trusted finding-level adjudication and remains unknown here. A release job must derive its expected contract using the current built CLI and trusted model/effort settings; accepting the digest asserted by the evidence itself would not establish freshness.

Run offline adapter, fake-CLI, and bundle regressions with `make live-test` and `python3 -B -m unittest discover -s pilot -p test_review_evidence.py`. They never invoke a real Provider.
