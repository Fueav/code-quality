# 公司 CI 结果 envelope 合同

`quality-review` 每次只产生一份不可变的 schema-v8 `EXECUTED` 结果。公司 CI 的缓存命中与发布时效性属于外部状态，必须用 [review-result-envelope-v1 schema](../schemas/review-result-envelope-v1.schema.json) 包装原始结果，不能修改原始 JSON。

四个值分属三个维度：

- `review_result.review_scope`：`FULL` 或 `INCREMENTAL`，表示 Provider 实际审查的范围。
- `result_source`：`EXECUTED` 或 `REUSED`，表示本次是否真正调用了 Provider。
- `lifecycle_status`：`CURRENT` 或 `SUPERSEDED`，表示发布时该结果是否仍对应 PR 当前 base/head。

可执行示例见 [company-ci-review-result-envelope-v1.example.json](company-ci-review-result-envelope-v1.example.json)。示例内嵌的是一份完整、可独立校验的 schema-v8 结果。

## 外围 Runner policy

Service/Runner 可以固定禁用 apps、plugins、web search 或网络，并要求 Provider 必须成功读取本地代码。这些限制不属于 CLI 的 `contract_digest`，因此集成层必须维护独立的 `runner_policy_version`，并在可信存储或签名 attestation 中校验它：

- 缓存命名空间是 `(review_key, runner_policy_version)`，二者都一致才允许 `REUSED`；
- policy 不同、未知或 attestation 失败时，不得复用结果，也不得把旧结果传给 INCREMENTAL，必须强制 FULL；
- 本地代码读取门禁失败时返回外围 `ERROR/HOLD`，不得伪造 PASS、BLOCK 或 CLI review-result；
- `runner_policy_version` 不得写入原始 schema-v8 结果或 envelope-v1，因为两者都是严格、不可变的已发布合同。

## 推荐流程

1. 用 `quality-review plan` 计算当前 `review_key`，此步骤 `provider_invocations=0`。
2. 可信存储中存在同 `review_key` 和 `runner_policy_version`、通过 schema-v8、内容、签名或 attestation 校验的原始结果时，返回 `result_source=REUSED`；否则执行一次 CLI 并返回 `EXECUTED`。
3. 发布 required check 前重新读取 PR 当前 base/head，以 compare-and-swap 方式与计划时冻结值比较。
4. 值未变化才标记 `CURRENT` 并发布结论；发生新 push、目标分支前进或 force-push 时标记 `SUPERSEDED`，旧结果不得发布为当前结论。

伪代码：

```text
policy = verified_runner_policy_version()
plan = quality-review plan(...same_contract_args)
result = verified_store.get(plan.review_key, policy)
source = result ? REUSED : EXECUTED
if !result:
  doctor = quality-review doctor(...same_contract_args)
  result = quality-review run-(codex|claude)(...same_contract_args)

observed = read_current_pr_base_and_head()
lifecycle = observed == plan.frozen_base_and_head ? CURRENT : SUPERSEDED
publish({schema_version: 1, result_source: source,
         lifecycle_status: lifecycle, review_result: result})
```

`REUSED` 时不产生新的 Provider 调用；`SUPERSEDED` 不是失败或通过结论，而是“这份结果已经不能代表当前 PR”。Harness 的修复、提交和复查轮次同样不属于这个 envelope 或 CLI。

INCREMENTAL `plan` 或运行返回 `FULL_REQUIRED` / exit 4 时，外围重新读取当前 base/head，移除 `--previous-result`，再用同一 model、reasoning effort、execution profile、goal 与范围参数执行一次 `plan -> doctor -> run FULL`。CLI 不自动扩大范围。
