# code-quality v0.5.6 生产接入合同修正规格

状态：Owner 已批准，并明确要求公司侧生产审查统一使用 `reasoning-effort=max`。

基线：`v0.5.5` / `8380e9d4e7cb452395b49b479ab15fd8adf802e5`。

## 1. 目标

修正 v0.5.5 官方接入示例中 `plan`、`doctor` 与原生运行命令使用不同 Provider 合同参数的问题，并明确外围 Runner 安全策略与 CLI review contract、增量复用之间的边界。

本次是接入合同补丁，不改变 FULL/INCREMENTAL、P0/P1 阻断、P2/P3 advisory、schema-v8 详细结果、schema-v3 简报或 envelope-v1 的既有语义。

## 2. 唯一合同参数

每次生产审查必须先解析一次以下参数，随后原样传给 `plan`、`doctor` 和 `run-codex` / `run-claude`：

- Provider host 与 model；
- `reasoning-effort=max`；
- `execution-profile=production-ci`；
- base/head 范围、review scope、previous result、goal 与 diff reason（存在时）。

官方 reusable workflow 保留既有 model 与 reasoning-effort 输入以兼容调用方，但默认 reasoning effort 升为 `max`；同一次运行中不允许三个阶段各自使用默认值。README、Jenkins 示例和官方 Skill 的公司侧路径全部使用 `max`。

改变 model、reasoning effort 或 execution profile 会改变 `contract_digest` 与 `review_key`。任何阶段参数不一致都必须在 Provider 调用前暴露，不能把 plan 或 doctor 的身份当作另一个合同下的运行结果。

## 3. 增量回退与结果时效

CLI 继续只返回 `FULL_REQUIRED`，不在内部自动扩大范围。公司 Service/Runner 在 INCREMENTAL `plan` 或运行返回 exit 4 后，重新读取当前 base/head，移除 `--previous-result`，再以相同合同参数执行一次 `plan -> doctor -> run FULL`。

发布前的 `CURRENT / SUPERSEDED` compare-and-swap 仍由外围完成，不进入 CLI 结果。

## 4. 外围 Runner 安全策略

外围可以固定禁用 apps、plugins、web search 或网络，并增加“必须成功读取本地代码”的 fail-close 门禁，但不得改写 CLI prompt、schema、原始结果或 Provider 参数。

这些限制不属于 v0.5.5/v0.5.6 的 `NativeReviewContract`，因此外围必须维护独立的 `runner_policy_version`：

- 可信缓存以 `(review_key, runner_policy_version)` 隔离；
- policy 不同或缺少可信 attestation 时，不得 REUSED，也不得把旧结果传给 INCREMENTAL，必须执行 FULL；
- 本地代码读取门禁失败时发布外围 ERROR/HOLD，不伪造 PASS、BLOCK 或 review-result；
- `runner_policy_version` 留在可信存储或外围 attestation，不加入严格的 schema-v8 结果或 envelope-v1。

## 5. 验收与发布

1. 合同测试证明 reusable workflow、Jenkins 示例、README 和 Skill 统一传递参数，默认/公司示例均为 `max`。
2. 合同测试证明 runner policy 保持在不可变 CLI 结果与 envelope-v1 之外，policy 变化强制 FULL。
3. 版本面同步到 `0.5.6`，旧 schema 文件与历史规格保持不可变。
4. `make release-check VERSION=v0.5.6 VERIFY_COMPARE_REF=v0.5.5`、workflow 语法/actionlint 和四平台 `make dist` 全部通过。
5. 从干净 release commit 原子推送 `main` 与 annotated `v0.5.6`，创建 GitHub Release，并校验公开资产、checksum、installer 和两个 bootstrap 路径。
