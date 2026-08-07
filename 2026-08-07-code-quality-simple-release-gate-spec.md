# code-quality v0.5.3 简明发布门禁规格

状态：实现完成，发布前验证通过；等待创建公开 `v0.5.3` Release。

基线：`v0.5.2` / `b81c8d3d53ab484653aa69b29f9313bd1877ce3d`；Jenkins 文档已合入 `main` 的 `69e97a906750b73aa97a91b1fce51ec4333c7a3d`。

## 1. 目标

PR 审查完成后，人类主界面只回答两件事：是否存在必须解决的问题，以及 AI 代码审查是否允许继续发布流程。后台仍保留完整、可验证的原始证据，但不得让证据文件淹没主结论。

## 2. 外部结果契约

只保留三个结果：

- `PASS`：没有有效阻塞问题，`release=CONTINUE`，退出码为 0。
- `BLOCK`：至少一个有效阻塞问题，`release=HOLD`，退出码非 0。
- `ERROR`：Provider、结构化输出、证据冻结或报告发布不可信，`release=HOLD`，退出码非 0。

`BLOCK` 与 `ERROR` 都必须使 Jenkins/GitHub CI required check 失败。此结论只代表 AI 代码审查门禁；完整发布仍需编译、测试、安全扫描等其他 Gate 通过，因此面向人的文案使用“可以继续发布流程”，不得声称“已经可以发布”。

## 3. 简明输出契约

每次完成的 Provider 调用必须生成：

1. `review-summary.md`：人类权威入口，首屏展示结果、发布建议、阻塞问题数量；每个问题只展示优先级、标题、位置、原因和最小修复建议。
2. `review-summary.json`：机器入口，只包含 `result`、`release`、`blocking_issues` 和同样的 `issues`。
3. `evidence.tar.gz`：CI 后台证据包，包含完整 session、原始 Provider 输出、stderr/stdout、运行指标、哈希和详细结果。

CLI stdout 摘要不得再要求消费者理解 session 内部结构，只包含上述简明结果和 `summary_path`、`evidence_dir`。详细 `review-result.json` 可继续作为内部审计事实，但不作为开发者主入口。

## 4. Provider 输出契约

- Codex 与 Claude Code 都必须使用各自原生的 JSON Schema structured output 能力。
- Provider 最终消息只允许 `findings` 数组；每条 finding 必须包含 `priority`、`title`、`location.path/start_line/end_line`、`reason` 和 `suggestion`。
- `location.path` 必须是本次 changed-files 中的仓库相对路径；任何未知字段、非法优先级、空字段、非法行号范围或非 changed-file 位置都使整个运行变为 `ERROR`，不得部分接受后返回 `PASS`。
- 空 `findings` 为 `PASS`；一个或多个有效 finding 为 `BLOCK`。不再通过任意非空文本推导 `MANUAL_REVIEW`。
- Provider 仍只调用一次，production CI 仍只读、无外部写入。

## 5. Jenkins 与 GitHub CI

- Jenkins 控制台和 GitHub job summary 直接渲染 `review-summary.md`。
- CI 主 artifact 只暴露 `review-summary.md`、`review-summary.json`、doctor/run 摘要和单一 `evidence.tar.gz`。
- Jenkins 继续以 PR 的 `merge-base -> head` 为范围，固定 `reasoning_effort=high`；机器已有 Provider 登录态，不使用 API Key。
- `BLOCK` 不得被 `continue-on-error` 最终吞掉；summary/evidence 发布完成后，最终 Gate 必须失败。

## 6. 版本与验收

- CLI、双端插件、marketplace、Skill、README、CI workflow、Jenkins 文档和测试统一为 `0.5.3` / `v0.5.3`。
- 新 schema 使用 v6；v3、v4、v5 文件保持不可变。
- 契约测试先证明旧实现无法生成结构化 finding、仍返回 `MANUAL_REVIEW` 且不阻塞 CI，再实现到 GREEN。
- `go test ./...`、qualification/live/mining、`go vet ./...`、shell syntax、workflow syntax、格式与 diff 检查全部通过。
- `make release-check VERSION=v0.5.3 VERIFY_COMPARE_REF=v0.5.2` 通过后，才允许提交、原子推送 `main` 与 annotated Tag，并创建 GitHub Release。
- 发布后下载公开资产逐字节比对并校验 checksums；分别验证公开 Codex/Claude bootstrap 与结构化结果路径。

## 7. 发布前证据

- RED：新增契约测试最初因缺少结构化 `reason`、`suggestion` 和 `ERROR` 结果而编译失败。
- GREEN：`go test ./...`、`go vet ./...`、actionlint、shell/JSON schema/diff 检查全部通过。
- Provider 协议：Codex `--output-schema` 与 Claude Code `--json-schema` 均已真实返回 `{"findings":[]}`。
- 发布门禁：`make release-check VERSION=v0.5.3 VERIFY_COMPARE_REF=v0.5.2` 通过，含 26 个 qualification、8 个 live 和 2 个 mining 测试。
