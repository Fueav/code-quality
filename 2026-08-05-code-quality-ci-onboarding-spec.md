# code-quality CI 引导规格

状态：CI 运行环境与 PR 审查单元纠正已批准，`v0.5.2` 实现完成；公开发布状态以 GitHub Release 为准。

基线：`v0.5.1` / `3a29389ee4f924ac6cfb861e2509c4f7ad45aaaf`。

目标版本：`v0.5.2`。`v0.5.0`、`v0.5.1` Tag 与已发布资产保持不可变。

## 1. 目标

在不改变 `quality-review` 现有 report-only 语义的前提下，提供两个互不混淆的正式入口：

1. 个人开发者在 Codex 或 Claude Code 中用一句自然语言完成固定版本安装、doctor 和一次只报告审查。
2. 一台受控的 self-hosted Linux runner 预先具备固定版本 `quality-review`、Provider CLI 和该系统用户的原生登录态；GitHub reusable workflow 只验证环境并管理审查范围、证据和执行健康，接入仓库只保留最小 caller。

## 2. 个人开发者契约

- 固定使用公开 `v0.5.2` bootstrap，并自动选择 `codex` 或 `claude`。
- bootstrap 返回绝对 CLI 路径；首次 doctor 和 run 不依赖 shell profile。
- doctor 不通过时不调用模型；未提交文件不进入审查范围。
- 审查只覆盖 committed base-to-target，不修改代码、Git、CI 或外部状态。

## 3. Linux CI 契约

- 仓库提供 `.github/workflows/code-quality-reusable.yml`，只通过 `workflow_call` 被业务仓库调用。
- caller 只传 `provider` 与可选模型参数，不传 base/target；默认 `reasoning_effort=low`，每次只运行一个 Provider。
- job 只接受私有仓库的可信同仓 `pull_request` 事件，并固定运行在 `code-quality` runner group 内带 `self-hosted`、`linux`、`code-quality` 标签的 runner；运行 Actions Runner 的同一个低权限系统用户必须预装 `quality-review v0.5.2`，并预先安装、登录所选 Provider。
- GitHub PR 以 `merge-base → head` 为唯一审查范围；目标分支 tip 只作为可追踪事实。结果 schema v5 记录 PR 编号、base/head ref、base tip、PR URL 与 run URL。
- reusable workflow 只验证 `quality-review` 精确版本；不安装任何 CLI，不接收 Provider API key，不复制登录目录，也不创建临时登录。`run-codex` 直接复用机器登录态启动一次原生 `codex exec`，Claude 路径同理。
- CI 不安装交互式插件。doctor 必须在模型调用前验证 Provider 二进制、原生能力、登录态和 Git 范围；`BLOCKED` 时不得启动 Provider。
- CI 固定使用 `production-ci` execution profile：Codex 为 read-only、忽略用户配置/rules、ephemeral；Claude Code 为 plan、safe-mode、strict MCP。个人默认 profile 不变。
- checkout 必须拉取完整历史、检出明确 target、关闭 persisted Git credentials；job 权限仅为 `contents: read`。
- 只允许可信同仓 PR；fork PR、Dependabot PR 必须跳过，不得使用 `pull_request_target` 执行未信任 target。
- review 输出写入仓库外的 `$RUNNER_TEMP`；doctor、运行摘要和完整 session 无论成功失败都上传为 artifact。
- `PASS` 和 `MANUAL_REVIEW` 保持成功退出并发布报告；`BLOCKED`、`INCOMPLETE` 或配置错误使执行健康检查失败。
- 旧 run 被同一 PR 的新提交取消，避免浪费模型额度。

## 4. 非目标

- 不把 finding 转换成强制合并门禁。
- 不自动向 PR 写评论，不授予 `pull-requests: write` 或 `contents: write`。
- 不在 workflow、仓库或 Actions secrets 中传递 Provider 凭据，不支持让持久机器登录态接触 fork 或其他不可信变更。
- 不负责安装 `quality-review`、Codex/Claude Code 或完成 Provider 登录；这些都是 self-hosted Linux runner 的显式前置条件。
- 不改变退出码或既有已发布资产；新增 schema v5，保留 v3/v4 不变。
- 不处理已接受的 curl 边缘重试改进；正常安装路径保持不变。

## 5. 验收

- Go 契约测试机械检查 workflow 的 PR-only 输入、权限、版本、认证、只读 profile、输出、上传与失败策略。
- README 有独立、可从头执行的“个人开发者”和“Linux CI”章节，并说明 self-hosted runner、预置登录态、范围、结果语义和安全边界。
- 契约测试必须证明 workflow 使用 self-hosted Linux runner，验证预装 `quality-review` 的精确版本，且不含 `provider_api_key`、`OPENAI_API_KEY`、`ANTHROPIC_API_KEY`、CLI 安装或 Provider 登录命令。
- `actionlint`、`go test ./...`、qualification/live/mining、`go vet ./...`、shell syntax 与 `git diff --check` 全部通过。
- CLI、Skill、双端 plugin descriptor、Claude marketplace、README、CI workflow 与测试必须统一为 `0.5.2` / `v0.5.2`。
- `make release-check VERSION=v0.5.2 VERIFY_COMPARE_REF=v0.5.1` 通过后，才允许原子推送 `main` 与 annotated Tag、创建 GitHub Release。
- 发布后验证公开资产及 checksums、隔离环境中的 Codex/Claude bootstrap，并在可信试点仓的已登录 self-hosted Linux runner 完成一次低思考 CI。

## 6. 历史验证证据（2026-08-05，v0.5.1）

- `go test .`：新增 README/workflow 契约测试先 RED，实施后 PASS。
- `make test`：Go 全量、qualification、live、mining 全部 PASS。
- `go vet ./...`、`sh -n install.sh`、`sh -n plugins/code-quality/scripts/bootstrap.sh`、workflow 内嵌 Bash `bash -n`：PASS。
- `actionlint v1.7.12`：reusable workflow 与固定 `v0.5.1` 的 README caller 示例均 PASS。
- `make release-check VERSION=v0.5.1 VERIFY_COMPARE_REF=v0.5.0`：发布前 dirty-tree 门禁 PASS；提交后仍须在 clean tree 重跑。
- `gofmt`、trailing-whitespace scan、`git diff --check`：PASS。
- 未伪造 hosted 结果：reusable workflow 尚未发布到可引用的 Tag/SHA，首个真实 GitHub Actions run 留作发布后验证。

上述证据只证明 `v0.5.1` 的旧 API-key/hosted-runner 实现，不证明本次纠正后的 self-hosted Linux runner 契约。`v0.5.2` 的新验证证据必须在实现 SHA 冻结后追加，不能复用旧结果。

## 7. 本次实现验证（2026-08-07，v0.5.2 发布前）

- 聚焦契约测试先 RED：旧 workflow 缺少 self-hosted runner 与预装 CLI 契约，并仍包含 API key、临时登录和 Provider 安装步骤。
- intake 回归测试证明目标分支前进时仍使用 PR merge-base，且 changed files 不包含目标分支自己的新增文件；PR 身份与 run URL 写入 schema v5。
- Provider argv 回归测试同时冻结个人 profile 的既有完整能力与 `production-ci` 的只读隔离参数；doctor 在模型调用前检查这些参数是否受当前 CLI 支持。
- reusable workflow 契约测试证明只接受私有同仓 PR、使用 `code-quality` runner group、不接收 base/target 或 Provider secret，并固定传入 `production-ci`。
- `go test .`、`go test ./...`、`make test`、`go vet ./...`、shell syntax、`git diff --check`：PASS。
- `actionlint v1.7.12`：PASS；`.github/actionlint.yaml` 声明自定义 `code-quality` runner label。
- `make release-check VERSION=v0.5.2 VERIFY_COMPARE_REF=v0.5.1`：PASS。
- commit、Tag、GitHub Release 与真实 self-hosted Linux runner 运行属于仓库外的发布证据，必须分别核验，不能由本节的本地测试结果代替。
