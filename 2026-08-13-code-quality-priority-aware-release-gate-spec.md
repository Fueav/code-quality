# code-quality v0.5.4 优先级感知发布门禁规格

状态：Owner 已批准，按本规格实施并发布 `v0.5.4`。

基线：`v0.5.3` / `d391d73d76b04aeaeb60f3f3e60726101cdb7510`，当前 `main` 另含 Jenkins 文档提交 `6954cca5363e95d3a126af1c67820e9e8d65d6f8`。

## 1. 目标

恢复公司级生产质量下限：原生 Provider 可以报告所有真实、可执行的缺陷，但只有达到 P0/P1 的严重且证据充分问题才阻断发布。P2/P3 保留为可见 advisory，不得单独把 required check 变成失败。

本次不增加第二 Provider、验证 Agent、重试、项目自定义规则或自动修改代码。

## 2. 优先级与判定合同

`schemas/native-review-output.schema.json` 是 Provider 优先级语义的机器真相源：

- P0：有具体证据的紧急阻断问题，可造成灾难性安全事故、不可逆数据或资产损失、或广泛生产中断。
- P1：本次变更新增或恶化、具有可达生产路径或直接违反已批准合同，并造成实质正确性、数据、安全、可靠性或兼容性影响的阻断问题。
- P2：真实但影响有条件、受限、可恢复或不紧迫的问题；应修复但不跨越公司级生产下限。
- P3：低影响真实缺陷或可选改进；纯风格、命名、偏好和没有规模证据的推测不得报告。

确定性分类只依赖冻结的结构化 findings：

- 至少一个 P0/P1：`BLOCK`、`release=HOLD`、退出码非零。
- 只有 P2/P3 或没有 finding：`PASS`、`release=CONTINUE`、退出码为零。
- Provider、结构化输出、冻结证据或结果合同不可信：`ERROR`、`release=HOLD`、退出码非零。

P0/P1 数量写入 `blocking_issues`；P2/P3 数量写入 `advisory_issues`。所有有效 findings 仍按优先级和位置排序并保留在证据与简报中。

## 3. 版本化输出

- 原生详细结果升级到 schema v7；v3-v6 保持不可变。
- `review-summary.json` 升级到 schema v2，字段为 `result`、`release`、`blocking_issues`、`advisory_issues` 和 `issues`。
- `PASS` 可以包含 P2/P3 issues，但不能包含 P0/P1；`BLOCK` 必须至少包含一个 P0/P1。
- Markdown 首屏同时显示 blocking 和 advisory 数量，并把两组问题分节展示。
- `ERROR` 不携带部分 findings，避免把不可信扫描包装成可发布结论。

## 4. CI 与兼容边界

GitHub reusable workflow 和 Jenkins 仍按 CLI 退出码执行 fail-closed：P0/P1、`ERROR`、doctor `BLOCKED` 失败，P2/P3-only 成功。现有只读 checkout、预认证 self-hosted 用户、单 Provider、证据包和不修改被审查仓库的边界不变。

外部消费者必须按 summary schema v2 读取；`issues` 不再等于 blockers，必须使用 `blocking_issues`、`advisory_issues` 或每条 priority 区分。

## 5. RED / GREEN / 发布验收

1. RED 测试证明 v0.5.3 会把 P2/P3-only 结果错误分类为 `BLOCK`，且不支持 advisory 计数、PASS-with-advisories 或 schema v7/v2。
2. GREEN 测试覆盖空 findings、P0、P1、P2、P3、混合 findings、非法 PASS/BLOCK 组合、Markdown、JSON、Codex/Claude 单次调用和 CLI 退出码。
3. 同步 CLI、双端插件、marketplace、Skill、README、GitHub workflow、Jenkins 文档与契约测试到 `0.5.4` / `v0.5.4`。
4. 在干净 release commit 上运行 `make release-check VERSION=v0.5.4 VERIFY_COMPARE_REF=v0.5.3` 和四平台 `make dist`。
5. 原子推送 `main` 与 annotated `v0.5.4` Tag，创建 GitHub Release；下载公开资产逐字节比对、校验 checksum，并验证公开 installer/bootstrap。

## 6. 发布前证据

- RED：新增契约测试先因缺少 `AdvisoryIssues`、PASS-with-advisories 和新 prompt 合同而失败；原始摘要保存在 `reports/2026-08-13-v0.5.4-priority-aware-release-gate/red-evidence.md`。
- GREEN：空 findings、P0、P1、P2、P3、混合优先级、非法语义组合、双格式简报和 CLI 退出码测试均通过。
- 发布门禁：dirty-tree 预检 `make release-check VERSION=v0.5.4 VERIFY_COMPARE_REF=v0.5.3` 通过，含 Go 全量、26 个 qualification、8 个 live、2 个 mining、vet、shell syntax、格式和 diff 检查。
- Workflow：`actionlint v1.7.12` 通过。
- 最终 clean-tree 门禁、四平台 `dist`、commit/Tag/Release 和公开资产验证属于后续发布步骤；本节不预先声称其成功，终态以远端 Tag、GitHub Release 和发布交付记录为准。
