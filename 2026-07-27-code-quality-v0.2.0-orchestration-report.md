# Code Quality v0.2.0 编排改动实施报告

## 1. 多次采样设计评估

当前产品是借用宿主现有会话完成模型审查的薄封装：CLI 固定输入、校验输出并裁决，但不持有模型凭证，也不能自行创建独立模型上下文。因此，单一宿主会话中的多轮输出不能被表述为独立采样。

| 方案 | 隔离强度 | 使用体验成本 | 实现复杂度 | 是否符合薄封装定位 |
| --- | --- | --- | --- | --- |
| `prepare` 生成 N 个 review slot，由 Skill 要求当前模型连续写入 | 弱。第二轮起可见先前结论，只能算迭代复查 | 低；一次会话内完成，但耗时和 token 近似线性增加 | 低到中；增加 slot、状态与合并即可 | 部分成立。仍借用宿主模型，但不能宣称独立样本 |
| 多次 `prepare` 生成独立 session，`finalize` 合并多个 session | 强度取决于调用方是否使用不同宿主会话；若不同会话则强 | 中到高；当前需要使用者或外部编排器启动多个会话并传回 session | 中；需增加 session 组、来源校验、确定性合并与失败策略 | 成立。产品只管理输入/工件/裁决，不管理模型后端 |
| 使用宿主原生子任务/子 Agent，每个任务读取同一冻结输入 | 中到强；宿主若保证新上下文则接近独立，否则只是会话分支 | 低到中；宿主支持时可在一次入口完成 | 中到高；Claude Code/Codex 能力与返回契约不同，需要宿主适配层 | 条件成立，但会削弱跨宿主一致性，不能作为核心契约的唯一实现 |
| CLI 直接调用模型 API 或另起 `codex exec`/其他模型进程 | 强 | 表面低，实际增加鉴权、成本与运行环境配置 | 高；需管理模型、重试、配额、日志与安全边界 | 不成立；越过“不自己管理模型后端”的产品边界 |

推荐未来完整实现采用“多独立 session + 显式合并裁决”：先把 session 组与合并契约做成宿主无关能力，再由支持独立上下文的宿主适配器或外部编排器提供 N 个样本。引擎必须记录每个样本的来源和完整性，只有能够证明来自不同上下文时才称为独立采样；否则降级标记为同会话迭代复查。

本轮仅实现零发现后的单次同会话复审。这里上下文延续是刻意设计：第一次 PASS 会成为第二轮反证式检查的触发信息，但状态机最多允许两轮，避免无限循环。

## 2. 旧评测口径审计

### `internal/eval/`

- `internal/eval/eval.go`：`Expected`、`validateExpected`、`runCase` 以预设 verdict/发现数判定 deterministic case 是否通过，并汇总 `PassedCases`/`FailedCases`。这是确定性裁决与 schema 回归门禁，不是真实模型的核心问题命中率；保留为工程附属门禁。
- `internal/eval/replay.go`：`PositiveCasesDetected`、`matchesSmokeExpectation` 与 `ReportOnlySmokeComplete` 以 positive fixture 是否产生任意发现作为 smoke 条件。它已不要求精确 rule 或 S/T/E，但仍是预设 fixture 期待驱动的准入门禁；保留 replay 能力，不作为产品相对基线质量的主指标。
- `internal/eval/eval_test.go`、`internal/eval/replay_test.go`：锁定上述 deterministic/smoke 行为，属于配套契约测试。

### `pilot/`

- `pilot/historical_pilot.py`：真实的旧主指标实现。`core_issue_found`、`severe_issue_discovery`、`severe_core_issue_pairs` 与 `caught_up_to_builtin_review` 把命中冻结核心问题及 normal 误报率组合成自动相对结论。
- `pilot/historical_pilot_review.py`：要求维护者为 severe 样本填写两条 lane 的 `core_issue_found`，是旧指标的人工输入入口。
- `pilot/historical_pilot_review_packet.py`：把 ground truth、label note 与两份报告排成“是否命中已知核心问题”的判定队列。
- `pilot/historical_pilot_initialize.py`、`pilot/historical_pilot_verify.py`：冻结、隔离并校验 ground truth 映射；它们本身不评分，但为旧指标提供可信标签与防泄漏证据。
- `pilot/historical-pilot-seed.json`：8 个历史样本的 ground truth/label note 数据源；保留历史数据。
- `pilot/web3-dataset-spec.md`：把 `caught_up_to_builtin_review` 及核心问题命中条件写成数据集目标与通过规则，是旧口径的规范入口。
- `pilot/README.md`：把 severe discovery、paired core-issue hits 与 `caught_up_to_builtin_review` 描述为历史 pilot 汇总及进入真实试点的依据。
- `pilot/test_historical_pilot.py`：锁定 ground-truth 隔离、核心问题判定和 `caught_up_to_builtin_review` 汇总。

`qualification_*` 工具主要验证 60 个合成任务的证据完整性、粗粒度 positive smoke、执行指标与本地 Codex 约束；它们不是历史核心问题命中率计算器，但其中 smoke completion 仍属于预设 fixture 门禁，继续作为附属工程信号。

## 3. 实施改动

### 行为结果

- 零发现 finalize 现在返回 `REREVIEW_REQUIRED`、`review_required=true`、`completed_review_rounds=1`、`maximum_review_rounds=2` 与 `next_review_path`，不提前发布最终报告，也保留临时 worktree 供复审。
- 第二轮结果与第一轮按 `rule_id + 排序后代码位置` 合并去重；不同根因若碰撞 finding ID 会被确定性重命名。第二轮之后必定终结，不会产生第三个 slot。
- 完成结果通过 `completed_review_rounds=2`、`retry_count=1` 与裁决原因区分两轮零发现；一轮有发现则记录 `retry_count=0`。
- 新增 `quality-review compare --product ... --baseline ...`。输入方用显式 `comparison_key` 标记同一问题，引擎只做确定性三分区，不猜语义、不评价真伪；输出每个分区的完整位置/描述和空白人工判定行。
- historical pilot 继续计算旧命中率，但移动到 `supplemental_ground_truth_metrics`，删除自动 `caught_up_to_builtin_review` 结论；`primary_evaluation.metric` 改为 `finding_comparison`，且 `automatic_pass=null`。
- 新增真实试点逐报告标注模板与汇总脚本，支持 `adopted`、`noise`、`confirmed_unseen`，汇总三类占比、D1–D4、零发现比例及单轮/复审零发现拆分；没有新增 CI 接入。
- `SkillVersion` 与 Claude/Codex plugin descriptor、Claude marketplace、README 三条固定版本安装命令均同步为 `0.2.0`。`.agents/plugins/marketplace.json` 经人工核对是本地 path 引用，不含也不支持独立版本字段；其版本来自 `plugins/code-quality/.codex-plugin/plugin.json`。

### 逐文件增删行数

| 文件 | + | - | 改动 |
| --- | ---: | ---: | --- |
| `.claude-plugin/marketplace.json` | 1 | 1 | Claude marketplace 版本升级 |
| `README.md` | 8 | 4 | 安装版本、零发现复审与对照评测说明 |
| `cmd/quality-review/main.go` | 33 | 1 | 新增 `compare` CLI |
| `cmd/quality-review/main_test.go` | 116 | 0 | 四条复审路径与 compare CLI 测试 |
| `internal/eval/comparison.go` | 144 | 0 | finding set 校验、三分区与人工模板 |
| `internal/eval/comparison_test.go` | 75 | 0 | 分区、保真与无歧义输入测试 |
| `internal/session/finalize.go` | 160 | 30 | 两轮状态机、合并去重、显式返回字段 |
| `internal/session/session.go` | 2 | 0 | 固定 rereview 路径 |
| `internal/session/session_test.go` | 25 | 0 | 合并去重与 ID 冲突测试 |
| `pilot/README.md` | 12 | 2 | 新主指标及真实试点用法 |
| `pilot/historical_pilot.py` | 10 | 11 | 命中率降级为附属指标，移除自动通过 |
| `pilot/live_report_annotation_template.json` | 26 | 0 | 逐 finding 三选一标注模板 |
| `pilot/live_report_summary.py` | 159 | 0 | 多报告汇总与输入校验 |
| `pilot/test_historical_pilot.py` | 5 | 2 | 新主/附属指标契约测试 |
| `pilot/test_live_report_summary.py` | 78 | 0 | 真实试点汇总测试 |
| `pilot/web3-dataset-spec.md` | 3 | 9 | 历史数据集评价口径同步 |
| `plugins/code-quality/.claude-plugin/plugin.json` | 1 | 1 | Claude plugin 版本升级 |
| `plugins/code-quality/.codex-plugin/plugin.json` | 1 | 1 | Codex plugin 版本升级 |
| `plugins/code-quality/skills/code-quality/SKILL.md` | 2 | 2 | 仅增加收到复审状态后的动作；净行数 0 |
| `quality/adjudicate.go` | 2 | 0 | 两轮零发现的可验证裁决原因 |
| `quality/types.go` | 1 | 1 | `SkillVersion=0.2.0` |
| `2026-07-27-code-quality-v0.2.0-orchestration-report.md` | 118 | 0 | 设计、审计、证据与风险报告 |

未修改 `.agents/plugins/marketplace.json`、`plugin_contract_test.go`、`policy/v1.1/*.md` 或 `.code-quality/`。工作区原有未跟踪 `reports/` 也未修改。

## 4. 验证

RED 证据：先加入契约测试后，`go test ./cmd/quality-review ./internal/eval` 以 exit 1 失败，错误分别为 `session.Finalized` 缺少复审状态字段，以及 `FindingSet` / `ComparisonFinding` / `CompareFindings` 未定义。实现后定向测试转绿。

最终 `gofmt` 门禁：

```text
$ gofmt -l internal cmd quality *.go
<no output>
```

最终 `go test` 门禁 exit 0。有测试包的实际输出如下；其余 `pilot/fixtures/**` 包均输出 `[no test files]`：

```text
$ go test ./...
ok  github.com/Fueav/code-quality  0.737s
ok  github.com/Fueav/code-quality/cmd/quality-review  2.746s
ok  github.com/Fueav/code-quality/internal/eval  (cached)
ok  github.com/Fueav/code-quality/internal/intake  (cached)
ok  github.com/Fueav/code-quality/internal/session  1.609s
ok  github.com/Fueav/code-quality/pilot/fixtures/cor-003-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/cor-004-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/cor-005-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/rel-004-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/rel-005-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/sec-002-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/sec-003-counterexample/base  (cached)
ok  github.com/Fueav/code-quality/pilot/fixtures/sec-003-counterexample/target  (cached)
ok  github.com/Fueav/code-quality/quality  (cached)
```

补充验证：`python3 -m unittest discover -s pilot -p 'test_*.py'` 实际结果为 `Ran 17 tests ... OK`。

## 5. 风险与待拍板项

- `comparison_key` 刻意由评测准备者提供，避免引擎用文本相似度误配；代价是 key 标注质量会直接影响三分区。若要进一步降低人工成本，需要你拍板是否接受一个“自动建议配对、人工确认”的辅助层，但它不应成为权威判定。
- 当前复审仍在同一宿主会话，不能声称独立采样；这是本轮明确接受的限制。未来完整实现建议采用多独立 session + 显式合并裁决，需要你决定何时进入该架构阶段。
- historical pilot 的输出字段移除了 `comparison.caught_up_to_builtin_review`，属于 0.2.0 有意的消费端契约变更；若有仓库外脚本依赖旧字段，需要同步迁移到 `supplemental_ground_truth_metrics` 与新的 compare 输出。
- 本轮未 push、未 tag、未发 release，也未自动接入 CI。
