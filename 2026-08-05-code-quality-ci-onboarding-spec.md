# code-quality CI 引导规格

状态：已批准，实现完成；`v0.5.1` patch release 已获批准，待发布验证。

基线：`v0.5.0` / `dab895d8c3cae72f77119d36890a0863b08632bc`。

目标版本：`v0.5.1`。`v0.5.0` Tag 与已发布资产保持不可变。

## 1. 目标

在不改变 `quality-review` 现有 report-only 语义的前提下，提供两个互不混淆的正式入口：

1. 个人开发者在 Codex 或 Claude Code 中用一句自然语言完成固定版本安装、doctor 和一次只报告审查。
2. 公司平台团队用一个 GitHub reusable workflow 集中管理 Provider、机器身份、版本、证据和执行健康；业务仓库只保留最小 caller。

## 2. 个人开发者契约

- 固定使用公开 `v0.5.1` bootstrap，并自动选择 `codex` 或 `claude`。
- bootstrap 返回绝对 CLI 路径；首次 doctor 和 run 不依赖 shell profile。
- doctor 不通过时不调用模型；未提交文件不进入审查范围。
- 审查只覆盖 committed base-to-target，不修改代码、Git、CI 或外部状态。

## 3. 公司 CI 契约

- 仓库提供 `.github/workflows/code-quality-reusable.yml`，只通过 `workflow_call` 被业务仓库调用。
- caller 必须显式传入 `provider`、`base_sha` 和 `target_sha`；默认 `reasoning_effort=low`，每次只运行一个 Provider。
- reusable workflow 固定安装 `quality-review v0.5.1`、Codex CLI `0.145.0` 或 Claude Code `2.1.220`；CI 不安装交互式插件。
- Provider 机器身份通过单个 `provider_api_key` secret 注入：Codex 在临时 `CODEX_HOME` 登录，Claude 仅在 doctor/run 步骤读取 `ANTHROPIC_API_KEY`。
- checkout 必须拉取完整历史、检出明确 target、关闭 persisted Git credentials；job 权限仅为 `contents: read`。
- 只允许可信同仓 PR；fork PR、Dependabot PR 必须跳过，不得使用 `pull_request_target` 执行未信任 target。
- review 输出写入仓库外的 `$RUNNER_TEMP`；doctor、运行摘要和完整 session 无论成功失败都上传为 artifact。
- `PASS` 和 `MANUAL_REVIEW` 保持成功退出并发布报告；`BLOCKED`、`INCOMPLETE` 或配置错误使执行健康检查失败。
- 旧 run 被同一 PR 的新提交取消，避免浪费模型额度。

## 4. 非目标

- 不把 finding 转换成强制合并门禁。
- 不自动向 PR 写评论，不授予 `pull-requests: write` 或 `contents: write`。
- 不在 CI 中共享个人订阅登录，不支持带密钥运行 fork 或其他不可信变更。
- 不改变 CLI schema、退出码、Provider prompt 或 v0.5.0 已发布资产。
- 不处理已接受的 curl 边缘重试改进；正常安装路径保持不变。

## 5. 验收

- Go 契约测试机械检查 workflow 的输入、权限、版本、认证、输出、上传与失败策略。
- README 有独立、可从头执行的“个人开发者”和“公司 CI”章节，并说明 Provider、密钥、范围、结果语义和安全边界。
- `actionlint`、`go test ./...`、qualification/live/mining、`go vet ./...`、shell syntax 与 `git diff --check` 全部通过。
- CLI、Skill、双端 plugin descriptor、Claude marketplace、README、CI workflow 与测试必须统一为 `0.5.1` / `v0.5.1`。
- `make release-check VERSION=v0.5.1 VERIFY_COMPARE_REF=v0.5.0` 通过后，才允许原子推送 `main` 与 annotated Tag、创建 GitHub Release。
- 发布后验证公开资产及 checksums、隔离环境中的 Codex/Claude bootstrap，并在可信试点仓完成一次低思考 hosted CI。

## 6. 验证证据（2026-08-05）

- `go test .`：新增 README/workflow 契约测试先 RED，实施后 PASS。
- `make test`：Go 全量、qualification、live、mining 全部 PASS。
- `go vet ./...`、`sh -n install.sh`、`sh -n plugins/code-quality/scripts/bootstrap.sh`、workflow 内嵌 Bash `bash -n`：PASS。
- `actionlint v1.7.12`：reusable workflow 与固定 `v0.5.1` 的 README caller 示例均 PASS。
- `make release-check VERSION=v0.5.1 VERIFY_COMPARE_REF=v0.5.0`：发布前 dirty-tree 门禁 PASS；提交后仍须在 clean tree 重跑。
- `gofmt`、trailing-whitespace scan、`git diff --check`：PASS。
- 未伪造 hosted 结果：reusable workflow 尚未发布到可引用的 Tag/SHA，首个真实 GitHub Actions run 留作发布后验证。
