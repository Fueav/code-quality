# 公司 CI 结果 envelope 合同

`quality-review` 每次只产生一份不可变的 schema-v8 `EXECUTED` 结果。公司 CI 的缓存命中与发布时效性属于外部状态，必须用 [review-result-envelope-v1 schema](../schemas/review-result-envelope-v1.schema.json) 包装原始结果，不能修改原始 JSON。

四个值分属三个维度：

- `review_result.review_scope`：`FULL` 或 `INCREMENTAL`，表示 Provider 实际审查的范围。
- `result_source`：`EXECUTED` 或 `REUSED`，表示本次是否真正调用了 Provider。
- `lifecycle_status`：`CURRENT` 或 `SUPERSEDED`，表示发布时该结果是否仍对应 PR 当前 base/head。

可执行示例见 [company-ci-review-result-envelope-v1.example.json](company-ci-review-result-envelope-v1.example.json)。示例内嵌的是一份完整、可独立校验的 schema-v8 结果。

## 推荐流程

1. 用 `quality-review plan` 计算当前 `review_key`，此步骤 `provider_invocations=0`。
2. 可信存储中存在同 key、通过 schema-v8、内容、签名或 attestation 校验的原始结果时，返回 `result_source=REUSED`；否则执行一次 CLI 并返回 `EXECUTED`。
3. 发布 required check 前重新读取 PR 当前 base/head，以 compare-and-swap 方式与计划时冻结值比较。
4. 值未变化才标记 `CURRENT` 并发布结论；发生新 push、目标分支前进或 force-push 时标记 `SUPERSEDED`，旧结果不得发布为当前结论。

伪代码：

```text
plan = quality-review plan(...)
result = verified_store.get(plan.review_key)
source = result ? REUSED : EXECUTED
if !result: result = quality-review run-(codex|claude)(...)

observed = read_current_pr_base_and_head()
lifecycle = observed == plan.frozen_base_and_head ? CURRENT : SUPERSEDED
publish({schema_version: 1, result_source: source,
         lifecycle_status: lifecycle, review_result: result})
```

`REUSED` 时不产生新的 Provider 调用；`SUPERSEDED` 不是失败或通过结论，而是“这份结果已经不能代表当前 PR”。Harness 的修复、提交和复查轮次同样不属于这个 envelope 或 CLI。
