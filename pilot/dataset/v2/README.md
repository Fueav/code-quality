# Code-quality historical dataset v2

Frozen on 2026-07-28 from the existing 13-target historical set plus three completed repository-mining runs. The owner stopped the fourth and fifth repositories after the first three were sufficient for the preregistered scale conclusion; six completed `social-trading-ai` traces are excluded from `targets.json` and reported only as sunk cost.

## Frozen result

| Gate | Result | Threshold | Verdict |
| --- | ---: | ---: | --- |
| Total targets (`n`) | 103 | ≥30 | PASS |
| `missing_safeguard` targets | 41 | ≥10 | PASS |
| Difficulty 3+ targets | 52 | ≥15 | PASS |

This file and `targets.json` are the frozen v2 dataset. Any later missing-defect, sampling, merge, or other mechanism-effectiveness claim must cite the dataset version and its evaluated `n`; percentages without their denominator are not an acceptable power claim.

## Construction method

1. `select_repos.py` enumerated direct Git repositories under `/Users/chris/AiProject`, retained repositories with at least 30 total commits and five fix-like commits, excluded the two previously mined repositories, code-quality and its linked worktrees, and ranked by fix-like commit count.
2. `prefilter.sh` retained fix-like commits touching a source-extension allowlist outside pure docs, CI, tests, fixtures, examples, samples, and configuration templates.
3. Each candidate received one isolated, ephemeral, read-only `codex exec` using versioned `trace-prompt.md` and `mining-result.schema.json` v1.0.0. Concurrency never exceeded four.
4. `aggregate.py` admitted only in-scope, material, statically detectable results with a locally resolvable introducing commit changing at most 3,000 text lines, then deduplicated by full introducing commit.
5. The prior 13 targets were migrated from `reports/2026-07-28-route-b-historical-mining-eval/evidence/targets.json`. Each gained a `defect_class` and one-line `defect_class_basis` from its frozen defect description. `build_dataset.py` makes this mapping explicit and reproducible.

For deduplicated new targets, every accepted fix remains in `defects`. The highest-difficulty result supplies the target-level class and basis. Dataset uniqueness is `(repo, introducing_commit)`.

## Mechanical repository ranking and stop decision

| Rank | Repository | Total commits | Fix-like commits | Source candidates | Disposition |
| ---: | --- | ---: | ---: | ---: | --- |
| 1 | `relayer` | 2,555 | 1,038 | 1,010 measured | Stopped before trace; excluded |
| 2 | `social-trading-ai` | 938 | 458 | 446 | Stopped after 6 traces; excluded |
| 3 | `moss_agent_trade` | 865 | 266 | 249 | Completed and included |
| 4 | `ai-first-go-template` | 63 | 17 | 14 | Completed and included |
| 5 | `marketplace-ballot-agent` | 92 | 11 | 5 | Completed and included |

Runs were started from the smallest candidate sets to validate the productized pipeline before incurring the largest cost. After the three completed repositories produced 90 new targets and the combined dataset passed every size gate, the owner explicitly stopped the remaining two repositories. No sampling or post-hoc candidate selection from those partial repositories enters v2.

## Completed mining statistics

| Repository | Candidates / traces | Accepted results | Too large | Out of scope | Not static | Deduplicated | Output targets |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `marketplace-ballot-agent` | 5 / 5 | 3 | 0 | 1 | 1 | 0 | 3 |
| `ai-first-go-template` | 14 / 14 | 12 | 0 | 1 | 1 | 3 | 9 |
| `moss_agent_trade` | 249 / 249 | 127 | 70 | 31 | 21 | 49 | 78 |
| **Total new** | **268 / 268** | **142** | **70** | **33** | **23** | **52** | **90** |

The frozen composition is 8 `agent_marketplace`, 5 `general-agent-ai`, 9 `ai-first-go-template`, 3 `marketplace-ballot-agent`, and 78 `moss_agent_trade` targets.

## Cost

Durations below are summed per-trace wall seconds, not parallel-run elapsed time. Token counts come from `turn.completed` events.

| Repository | Status | Input | Cached input | Output | Reasoning output | Trace wall seconds |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `marketplace-ballot-agent` | Included, 5/5 | 526,926 | 348,160 | 9,919 | 2,212 | 308 |
| `ai-first-go-template` | Included, 14/14 | 1,848,897 | 1,276,416 | 25,357 | 6,054 | 865 |
| `moss_agent_trade` | Included, 249/249 | 46,182,827 | 35,688,448 | 492,647 | 102,269 | 15,948 |
| **Included total** | **268/268** | **48,558,650** | **37,313,024** | **527,923** | **110,535** | **17,121** |
| `social-trading-ai` | Stopped, excluded, 6/446 | 782,663 | 579,584 | 10,944 | 2,129 | 323 |
| `relayer` | Stopped, excluded, 0/1,010 | 0 | 0 | 0 | 0 | 0 |

The observed cost shows that raw fix-count ranking can select fork-sized histories: expanding beyond these three completed repositories should require an explicit candidate budget or a preregistered sampling design.

## Verification

Run:

```bash
make mining-test
pilot/mining/validate_dataset.py pilot/dataset/v2/targets.json
```

The validator checks target uniqueness, required labels and bases, difficulty ranges, and all three frozen-size gates.
