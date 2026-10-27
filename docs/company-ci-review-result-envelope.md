# 公司 CI 结果 envelope 与受限恢复合同

`quality-review` 只发布一份不可变的 schema-v10 PASS/BLOCK/ERROR 结果。v0.6.0 的 `internal/reviewrunner` 供现有 `run-codex` / `run-claude` 入口调用，负责同请求去重、可信终局复用、受限恢复和一次合法增量。公司 CI 的缓存命中与发布时效性必须用 [review-result-envelope-v3 schema](../schemas/review-result-envelope-v3.schema.json) 包装，不得修改原始 JSON。已发布的 envelope-v1/v2 和它们分别引用的 schema-v8/v9 继续逐字节冻结。

四个值分属三个维度：

- `review_result.review_scope`：`FULL` 或 `INCREMENTAL`，表示 Provider 实际审查的范围。
- `result_source`：`EXECUTED` 或 `REUSED`，表示本次是否产生了新的有效结果。恢复后发布仍是 `EXECUTED`。
- `lifecycle_status`：`CURRENT` 或 `SUPERSEDED`，表示发布时该结果是否仍对应 PR 当前 base/head。

可执行示例见 [company-ci-review-result-envelope-v3.example.json](company-ci-review-result-envelope-v3.example.json)。示例内嵌一份经过恢复、完整记录 1 次 Native 和 2 次 Restricted attempt 的 schema-v10 结果。

## 外围 Runner policy 与持久化

Service/Runner 可以固定禁用 apps、plugins、web search 或网络，并要求 Provider 必须成功读取本地代码。这些限制不属于 CLI 的 `contract_digest`，因此集成层必须维护独立的 `runner_policy_version`：

- 缓存命名空间是 `(review_key, runner_policy_version, execution_fingerprint)`，三者一致且完整证据通过校验才允许 `REUSED`；`review_key` 已绑定 repository、base/head、scope、父结果和 contract，不能用 SHA 单独替代；
- `execution_fingerprint` 是受保护适配器对有效 Provider CLI 版本、配置、插件、工具权限和运行环境的可信声明；CLI 不能替调用方证明这份声明。配置或 Provider 升级必须换指纹；未知 personal 上下文禁止跨运行复用；
- 可恢复 session 索引还绑定 `repository` 与 `contract_digest`；
- session root 和所属 Git object store 必须在 Jenkins workspace 之外持久化，保持 owner-only 权限、明确保留期和可审计删除；
- policy 不同、未知或 attestation 失败时不得复用或恢复；如需继续，只能由人工明确发起强制 FULL；
- 本地代码读取门禁失败时返回外围 `ERROR/HOLD`，不得伪造 PASS、BLOCK 或 CLI review-result；
- `runner_policy_version` 和 `execution_fingerprint` 不得写入原始 schema-v10 结果或 envelope-v3。

受保护适配器传入 `--execution-profile production-ci --state-root <absolute-private-root> --runner-policy-version <verified-version> --execution-fingerprint <verified-digest>`。`state-root` 必须在实际 Git 根目录之外，目录为 owner-only `0700`；不能同时传 `--output-root`。同一个 change 必须固定路由到同一状态存储。CLI 的文件锁只保证同一文件系统上的进程串行，不是跨机器分布式锁；多机调度仍由 Service 持有业务级 CAS/租约。

共享流程只有一条：精确请求已有可信终局则验证后复用；已有冻结 Native 则只恢复 Restricted；已有可信 FULL 父结果且 base/contract/目标不变、head 严格追加时执行一次 INCREMENTAL；第一次请求才执行 FULL。第三轮返回 `MANUAL_REQUIRED`。policy/指纹变化、rebase、base 变化或父证据不兼容返回 `FULL_REQUIRED`，不会自动再开 FULL。

状态在启动 Native 之前登记，包含耐久 started 标记与请求索引。已知请求丢失 checkpoint，或 lineage 索引丢失但保留启动标记/运行目录，均 `ERROR/HOLD`，不会把未知结果当新任务。持久卷整体丢失无法由该卷上的程序识别，运维必须把它视为恢复事故，不能自动创建空卷并重放旧任务；需恢复状态或由人工明确批准新 lineage。保留期删除必须同时管理调度系统的请求登记。

不传 `--state-root` 时保留原单次事务行为。启用协调时 stdout 保留原 NativeRunSummary 顶层字段，仅附加 `runner: {result_source, lifecycle_status}`；`review-result.json` 和 `review-summary.json` 原文不变。`ERROR` 复用仍退出非零；`SUPERSEDED` 仅是外围时效状态，不能发布当前 required check 的成功。

总预算统一为 `--total-timeout 65m --finalize-reserve 5m`，包含默认 Native 45 分钟与 Restricted 15 分钟；CI job 留 70 分钟。启动前拒绝矛盾预算；`finalize-reserve` 是预算准入余量，队列和准备也消耗总 deadline，不能保证结束时仍有完整 5 分钟。这是避免 CI 提前杀死合法执行，不代表审查速度提升。等待锁时 stderr 输出一次安全等待诊断。

## 断点恢复流程

1. 以完整 identity、`runner_policy_version` 和 `execution_fingerprint` 查找可验证 session。普通 rerun 继续调用相同 `run-*`，共享协调器决定恢复，无需外围猜测状态。
2. 只有 checkpoint 状态为 `NATIVE_FROZEN`、`RESTRICTED_RUNNING` 或 `RESTRICTED_RETRYABLE` 时，GitHub rerun 或明确的内部 retry 才可调用 `quality-review resume-restricted --session <absolute-session-dir>`。
3. Service 先以数据库 CAS/分布式锁取得业务级所有权；CLI 再以 session lock 和全局 Provider lease 防止本机并发。
4. CLI 从 checkpoint 验证 Native 结论、contract、policy/schema/prompt、全部 digest 和 target commit，在同一 commit 的 detached checkout 上只运行 Restricted。Service 不从 raw log 推断状态。
5. checkpoint 缺失、不可验证或 target object 不存在时，返回人工可见的 `ERROR/HOLD`；不得自动 clone 并重跑 FULL。人工明确发起的新 FULL 是独立操作。
6. `PUBLISHED` 与 `MANUAL_REQUIRED` 的再次恢复都幂等且 Provider 调用数为 0；第二次 Restricted 失败必须停在 `MANUAL_REQUIRED`。
7. 发布 required check 前从远端权威源重新读取 PR 当前 base/head，以 compare-and-swap 与计划时冻结值比较，再用 envelope-v3 包装结果。本地 refs 重读只证明本地观察未变，冻结 GitHub event 不代表远端实时状态。GitHub 模板通过当前 PR API 读取，Jenkins 模板通过受信只读 SCM 凭据执行 `git ls-remote`。读取失败保持 HOLD。

## 进度事件与五分钟播报

Service 调用 `run-codex`、`run-claude` 或 `resume-restricted` 时传入 `--progress-format jsonl`，逐行读取 stderr。只有能按 `review-progress-event-v1` 解码的对象进入状态库；其他 stderr 行保留为诊断日志，不从 raw Provider transcript 推断阶段。

Service 以自己的 job/invocation identity 加 CLI `sequence` 去重并持久化最新事件。CLI 负责 `PLAN`、`RECOVERY`、`NATIVE`、`NATIVE_FREEZE`、`RESTRICTED`、`RESTRICTED_FREEZE` 和 `FINALIZE` 的事实；Service 负责每五分钟定时更新同一张卡片、进程重启恢复和外部消息幂等。播报只能展示当前阶段、review scope、attempt、已运行时间和最近活动时间，不展示 finding、prompt、Provider 原文、token、凭据或宿主路径，也不虚构百分比和 ETA。

progress writer 或消息发送失败不得改变 CLI 的 PASS/BLOCK、checkpoint 或退出码；Service 将通知故障作为独立运维状态处理。

伪代码：

```text
with distributed_change_ownership(repository, change):
  result_or_status = quality-review run-{host} ... --state-root private_store \
    --runner-policy-version verified_policy --execution-fingerprint verified_environment

if result_or_status.state == PUBLISHED:
  observed = read_current_pr_base_and_head()
  lifecycle = observed == frozen_base_and_head ? CURRENT : SUPERSEDED
  publish({schema_version: 3, result_source: result_or_status.runner.result_source,
           lifecycle_status: lifecycle, review_result: result_or_status.result})
```

GitHub Check 只显示 `Native Review reused`、当前阶段、Restricted attempt 数、各阶段耗时和安全的 retryable/manual reason。不显示被过滤的 candidate 正文、raw Provider log、token、凭据或宿主绝对路径。

`REUSED` 时不产生新的 Provider 调用；`SUPERSEDED` 不是失败或通过结论，而是“这份结果已经不能代表当前 PR”。Restricted stage retry 不创建新的 FULL/INCREMENTAL review round。

INCREMENTAL `plan` 或运行返回 `FULL_REQUIRED` / exit 4 只表示前次证据不能用于本次增量审查。项目契约决定是否还需要审查；必需审查须完成可信审查或保持未完成，不能静默跳过或标绿。人工批准新 FULL 后，重新读取 base/head，用明确独立的 lineage 发起；不得由自动化删除旧状态、换空 state-root 或移除父结果来重置轮数。

若上一份结果已经是 INCREMENTAL，CLI 返回 `MANUAL_REQUIRED` / exit 5。外围必须停止自动链并把剩余 blocker 交给人，不能按 `FULL_REQUIRED` 路径自动回退成 FULL。

## 精确归档

适配器把本次 stdout 保存为 `run.json`，再调用 `quality-review package-evidence --run <absolute-run.json> --evidence-root <state-root> --destination <private-artifact-directory>`。该运维命令只归档结构化输出指定的精确 session，拒绝 root、越界和 symlink，保留完整证据而不归档整个共享根。相同不可变快照只压缩一次，缓存与导出包都校验摘要；缓存损坏会失败，不默默重建。输出 `package.json` 记录 `evidence_dir`、`archive_path`、`archive_sha256`、`summary_path` 和 `reused`。

输出目录包含 `run.json`、`evidence.tar.gz`、`package.json`，以及本次真实存在的 `review-summary.md/json`。中断 session 没有摘要时不伪造结果，重复使用导出目录会移除上一份摘要。无可验证 session 时归档失败并保持 HOLD；stderr/调度日志仍保留失败原因。
