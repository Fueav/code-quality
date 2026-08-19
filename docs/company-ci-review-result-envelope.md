# 公司 CI 结果 envelope 与受限恢复合同

`quality-review` 只发布一份不可变的 schema-v10 PASS/BLOCK/ERROR 结果。公司 CI 的缓存命中与发布时效性必须用 [review-result-envelope-v3 schema](../schemas/review-result-envelope-v3.schema.json) 包装，不得修改原始 JSON。已发布的 envelope-v1/v2 和它们分别引用的 schema-v8/v9 继续逐字节冻结。

四个值分属三个维度：

- `review_result.review_scope`：`FULL` 或 `INCREMENTAL`，表示 Provider 实际审查的范围。
- `result_source`：`EXECUTED` 或 `REUSED`，表示本次是否产生了新的有效结果。恢复后发布仍是 `EXECUTED`。
- `lifecycle_status`：`CURRENT` 或 `SUPERSEDED`，表示发布时该结果是否仍对应 PR 当前 base/head。

可执行示例见 [company-ci-review-result-envelope-v3.example.json](company-ci-review-result-envelope-v3.example.json)。示例内嵌一份经过恢复、完整记录 1 次 Native 和 2 次 Restricted attempt 的 schema-v10 结果。

## 外围 Runner policy 与持久化

Service/Runner 可以固定禁用 apps、plugins、web search 或网络，并要求 Provider 必须成功读取本地代码。这些限制不属于 CLI 的 `contract_digest`，因此集成层必须维护独立的 `runner_policy_version`：

- 缓存命名空间是 `(review_key, runner_policy_version)`，二者都一致才允许 `REUSED`；
- 可恢复 session 索引是 `(repository, review_key, contract_digest, runner_policy_version)`；
- session root 和所属 Git object store 必须在 Jenkins workspace 之外持久化，保持 owner-only 权限、明确保留期和可审计删除；
- policy 不同、未知或 attestation 失败时不得复用或恢复；如需继续，只能由人工明确发起强制 FULL；
- 本地代码读取门禁失败时返回外围 `ERROR/HOLD`，不得伪造 PASS、BLOCK 或 CLI review-result；
- `runner_policy_version` 不得写入原始 schema-v10 结果或 envelope-v3。

## 断点恢复流程

1. 以 `(repository, review_key, contract_digest, runner_policy_version)` 查找可验证 session。
2. 只有 checkpoint 状态为 `NATIVE_FROZEN`、`RESTRICTED_RUNNING` 或 `RESTRICTED_RETRYABLE` 时，GitHub rerun 或明确的内部 retry 才可调用 `quality-review resume-restricted --session <absolute-session-dir>`。
3. Service 先以数据库 CAS/分布式锁取得业务级所有权；CLI 再以 session lock 和全局 Provider lease 防止本机并发。
4. CLI 从 checkpoint 验证 Native 结论、contract、policy/schema/prompt、全部 digest 和 target commit，在同一 commit 的 detached checkout 上只运行 Restricted。Service 不从 raw log 推断状态。
5. checkpoint 缺失、不可验证或 target object 不存在时，返回人工可见的 `ERROR/HOLD`；不得自动 clone 并重跑 FULL。人工明确发起的新 FULL 是独立操作。
6. `PUBLISHED` 与 `MANUAL_REQUIRED` 的再次恢复都幂等且 Provider 调用数为 0；第二次 Restricted 失败必须停在 `MANUAL_REQUIRED`。
7. 发布 required check 前重新读取 PR 当前 base/head，以 compare-and-swap 与计划时冻结值比较，再用 envelope-v3 包装结果。

伪代码：

```text
key = (repository, review_key, contract_digest, verified_runner_policy_version())
session = verified_session_store.get(key)

if retry_requested:
  if !session or session.state not in {NATIVE_FROZEN, RESTRICTED_RUNNING, RESTRICTED_RETRYABLE}:
    return ERROR_HOLD
  with distributed_compare_and_swap(session):
    result_or_status = quality-review resume-restricted --session session.absolute_path
else:
  result_or_status = execute_new_review_transaction()

if result_or_status.state == PUBLISHED:
  observed = read_current_pr_base_and_head()
  lifecycle = observed == frozen_base_and_head ? CURRENT : SUPERSEDED
  publish({schema_version: 3, result_source: EXECUTED,
           lifecycle_status: lifecycle, review_result: result_or_status.result})
```

GitHub Check 只显示 `Native Review reused`、当前阶段、Restricted attempt 数、各阶段耗时和安全的 retryable/manual reason。不显示被过滤的 candidate 正文、raw Provider log、token、凭据或宿主绝对路径。

`REUSED` 时不产生新的 Provider 调用；`SUPERSEDED` 不是失败或通过结论，而是“这份结果已经不能代表当前 PR”。Restricted stage retry 不创建新的 FULL/INCREMENTAL review round。

INCREMENTAL `plan` 或运行返回 `FULL_REQUIRED` / exit 4 时，外围只能在正常 review-round 语义下重新读取 base/head，移除 `--previous-result`，再以同一合同发起新 FULL；这不是 checkpoint 丢失时的自动回退。

若上一份结果已经是 INCREMENTAL，CLI 返回 `MANUAL_REQUIRED` / exit 5。外围必须停止自动链并把剩余 blocker 交给人，不能按 `FULL_REQUIRED` 路径自动回退成 FULL。
