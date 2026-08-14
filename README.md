# code-quality (`quality-review`)

用 Codex 或 Claude Code 的完整原生 Agent 审查一个**已提交的 Git 增量**，用 `PASS / BLOCK / ERROR` 直接回答是否可以继续发布流程。工具只读代码，不修改代码、Git、CI 或远端状态。

## 个人开发者：一句话安装并审查

开始前只确认三件事：当前是 macOS 或 Linux（arm64/amd64）；Codex 或 Claude Code CLI 已安装并登录；当前目录是 Git 仓库。然后先提交要审查的改动——未提交文件不属于审查范围。

在需要审查的仓库中，把下面整句话交给 Codex 或 Claude Code：

> 请为当前仓库安装并运行 Fueav code-quality v0.5.6；固定版本安装入口是 https://github.com/Fueav/code-quality/releases/download/v0.5.6/bootstrap.sh。请自动识别当前是 Codex 还是 Claude Code，使用对应的 `codex` 或 `claude` 参数完成 CLI 与插件安装，再检查宿主登录、版本、PATH、Git 基线、已提交差异和未提交文件。预检不通过时不要启动审查，只告诉我一个下一步；通过后执行一次只读审查，只展示简明结论、必须修复的问题和非阻断 advisory，不修改代码、Git、CI、远端或部署状态。

Agent 会根据当前宿主执行下面一个固定版本入口：

```sh
# Codex
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.6/bootstrap.sh | sh -s -- v0.5.6 codex

# Claude Code
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.6/bootstrap.sh | sh -s -- v0.5.6 claude
```

bootstrap 会同时安装 CLI 和对应插件，并输出 `QUALITY_REVIEW_BIN=<绝对路径>` 及下一条 doctor 命令。首次运行使用这个绝对路径，不依赖当前 shell 是否已包含 `~/.local/bin`；bootstrap 不会修改 shell profile。

如果要自己执行，先固定一组合同参数，再依次 plan、doctor 和运行；三个阶段必须使用同一组参数：

```sh
# Codex
quality-review plan --host codex --repo . --model gpt-5.6-sol --reasoning-effort max --execution-profile personal
quality-review doctor --host codex --repo . --model gpt-5.6-sol --reasoning-effort max --execution-profile personal
quality-review run-codex --repo . --model gpt-5.6-sol --reasoning-effort max --execution-profile personal

# Claude Code
quality-review plan --host claude-code --repo . --model opus --reasoning-effort max --execution-profile personal
quality-review doctor --host claude-code --repo . --model opus --reasoning-effort max --execution-profile personal
quality-review run-claude --repo . --model opus --reasoning-effort max --execution-profile personal
```

`--goal` 只在确实有额外意图或关注点时添加，并且也要原样传给三个阶段。默认会比较本地 `origin/HEAD` 与 `HEAD` 的 merge-base 到 `HEAD`。需要明确合并方向时，同时传 `--base-ref <目标分支>` 与 `--head-ref <来源分支>`；旧的 `--base <commit> --target <commit>` 继续表示调用方已经算好的精确提交范围。两组参数不能混用，`--diff-reason` 是可选审计说明。

### 明确方向与增量复核

例如把 Deploy 合入 Production，本地预检、正式执行与 CI 应传同一方向：

```sh
review_args=(--repo .
  --base-ref origin/production
  --head-ref origin/deploy/dockerhost-dev
  --review-scope full
  --model gpt-5.6-sol
  --reasoning-effort max
  --execution-profile production-ci)
quality-review plan --host codex "${review_args[@]}"
quality-review doctor --host codex "${review_args[@]}"
quality-review run-codex "${review_args[@]}"
```

`plan` 不调用 Provider，只冻结 base tip、head、merge-base、changed files、合同摘要和 `review_key`。Codex Exec 与 Claude Code 都不需要支持分支参数：CLI 先把 refs 解析成提交，在 current head 的 detached checkout 中准备确定的 diff，再启动一次原生 Provider。

同一 PR 只追加提交时，公司 CI 或 Harness 可以把上一份不可变详细结果交给 CLI：

```sh
review_args=(--repo .
  --base-ref origin/production
  --head-ref origin/deploy/dockerhost-dev
  --review-scope incremental
  --previous-result /trusted/reviews/previous-review-result.json
  --model gpt-5.6-sol
  --reasoning-effort max
  --execution-profile production-ci)
quality-review plan --host codex "${review_args[@]}"
quality-review doctor --host codex "${review_args[@]}"
quality-review run-codex "${review_args[@]}"
```

INCREMENTAL 只审 `previous_head..current_head`，同时复核上一轮未解决的 P0/P1。若目标分支前进、发生 rebase、合同或 Provider 配置变化、上一份结果不可信、或没有新增提交，CLI 会在调用模型前返回 `FULL_REQUIRED`、`provider_invocations=0` 和退出码 4；调用方应明确改跑 FULL，不能把它当成 `PASS / BLOCK / ERROR`。

`review_scope` 只表示 `FULL / INCREMENTAL`。公司 CI 若要缓存或防止旧结果覆盖新提交，应在原始 schema-v8 结果外表达 `EXECUTED / REUSED` 与 `CURRENT / SUPERSEDED`；CLI 本身不缓存、不发布 PR 状态，也不内置“修复—重跑”循环。详见 [公司 CI 结果 envelope 合同](docs/company-ci-review-result-envelope.md)。

### 手工安装与排障

升级时优先直接重跑上面的固定版本 bootstrap；它会让 CLI 与当前宿主插件保持同一版本。只有排查安装问题时才需要下面的分步命令。

CLI 安装器会判断平台、校验 SHA-256，并安装到 `~/.local/bin`（可用 `INSTALL_DIR` 覆盖）：

```sh
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.6/install.sh | sh -s -- v0.5.6
```

Codex plugin：

```sh
codex plugin marketplace add Fueav/code-quality --ref v0.5.6
codex plugin add code-quality@fueav-code-quality
```

Claude Code plugin 使用 HTTPS 和固定 Tag，不要求 GitHub SSH：

```sh
claude plugin marketplace add https://github.com/Fueav/code-quality.git#v0.5.6
claude plugin install code-quality@fueav-code-quality --scope user
```

在已打开的 Claude Code 会话中安装或升级后，运行 `/reload-plugins`；新会话会自动加载。

### 你会得到什么

- `PASS`：没有 P0/P1 阻塞问题，可以继续发布流程；P2/P3 仍会作为 advisory 展示。
- `BLOCK`：存在至少一个 P0/P1 必须解决的问题，暂停发布。
- `ERROR`：扫描不可信或未完成，暂停发布并修复环境后重跑。

人类只看 `review-summary.md`，机器读取 `review-summary.json`。CLI stdout 同样只返回结果、发布建议、问题数量、问题列表及简报/证据路径；原始 Provider 输出、日志、指标和冻结哈希保留在 `evidence_dir`，不占据主视野。

原生审查默认在仓库外的系统临时区创建一次运行独占、权限为 `0700` 的证据根目录。CLI 不会主动删除报告；长期归档时可传宿主允许写入、仓库外的绝对 `--output-root`。相对路径或经 symlink 解析回仓库内的路径会在创建 session 前被拒绝。

## Linux CI：复用自托管 Runner 的原生登录态

本节的 `jobs: uses:` 配置只适用于 GitHub Actions。使用 Jenkins 的团队请直接阅读 [Jenkins 生产 CI 接入](docs/jenkins-production-ci.md)。

这个入口面向一台受控的 self-hosted Linux runner。运行 GitHub Actions Runner 的同一个系统用户必须已经安装 `quality-review v0.5.6`，并安装、登录 Codex 或 Claude Code；workflow 不接收 Provider API key，不安装任何 CLI，也不创建临时登录。`quality-review run-codex` 会直接复用该用户的登录态，原生启动一次 `codex exec`；选择 Claude 时同理启动 `claude`。

CI 不需要安装 Codex 或 Claude Code 插件。reusable workflow 只验证预装的 `quality-review` 版本，然后以同一组合同参数执行 `plan → doctor → 原生审查 → 发布简报与证据`。生产路径以 PR 为审查单元：套件从 GitHub PR 事件读取 base tip 与 head，计算真实 `merge-base → head` 范围，并把 PR 身份冻结到 schema v8 结果中。一次只调用一个 Provider，默认 `reasoning_effort: max`；它不会写 PR 评论，只有 P0/P1 问题会返回 `BLOCK` 并使 required check 失败，P2/P3-only 结果保持 `PASS`。

### 1. 准备 Linux runner

创建名为 `code-quality` 的 runner group，只授权给接入的可信私有仓库；给组内 Linux runner 增加 `self-hosted`、`linux`、`code-quality` 标签。先切换到实际运行 Actions Runner 服务的系统用户，安装套件并确保安装目录属于该服务的 `PATH`：

```sh
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.6/install.sh |
  INSTALL_DIR="$HOME/.local/bin" sh -s -- v0.5.6
command -v quality-review
quality-review version  # 必须精确输出 quality-review v0.5.6
```

再验证所选 Provider：

```sh
# Codex 路径
command -v codex
codex --version
codex exec --help >/dev/null
codex login status

# Claude Code 路径
command -v claude
claude --version
claude auth status --json
```

登录必须在 runner 机器上、以这个系统用户预先完成。不要把登录目录、token 或 API key 复制到仓库、caller workflow 或 GitHub Actions secrets。workflow 会先验证 `quality-review` 精确版本；`doctor` 会在模型调用前再次检查 Provider CLI 能力、登录态、Git 范围与工作区。任何一项不满足都会失败或返回 `BLOCKED`，不会启动 Provider。

### 2. 仓库添加最小 caller

在接入仓库创建 `.github/workflows/code-quality.yml`：

```yaml
name: code quality

on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]

permissions:
  contents: read

jobs:
  review:
    if: >-
      !github.event.pull_request.draft &&
      github.event.pull_request.user.login != 'dependabot[bot]' &&
      github.event.pull_request.head.repo.full_name == github.repository
    uses: Fueav/code-quality/.github/workflows/code-quality-reusable.yml@v0.5.6
    with:
      provider: claude
      model: sonnet
      reasoning_effort: max
      artifact_retention_days: 14
```

示例固定引用 `v0.5.6`，不要改为 `main`。公司侧统一使用 `reasoning_effort: max`。改用 Codex 时只需把 `provider` 改为 `codex`、`model` 改为 `gpt-5.6-sol`；两种 Provider 都不传 secrets。job 会被调度到 `code-quality` runner group 内、带对应标签的 self-hosted Linux runner。

caller 不传 `base_sha` 或 `target_sha`。reusable workflow 只接受 `pull_request` 事件，完整拉取历史、精确检出 PR head，再由套件计算 merge-base；目标分支在 PR 创建后继续前进，也不会把目标分支自己的新增提交混进本次审查。每次 PR 更新都会取消同一 Provider 的旧 run，避免浪费额度。

### 3. 把 AI 审查设为 required check

CI 直接给出能否继续发布流程的结论：

| 结果 | CI 结论 | 含义 |
| --- | --- | --- |
| doctor `READY` | 继续 | CLI、身份、Git 范围与已提交差异可用 |
| doctor `BLOCKED` | 失败 | 不调用模型；先按 doctor 给出的下一步修复 |
| `PASS` | 成功 | 没有 P0/P1 阻塞问题；P2/P3 advisory 不阻塞发布 |
| `BLOCK` | 失败 | 存在至少一个 P0/P1 必须修复的问题，暂停发布 |
| `ERROR` 或配置错误 | 失败 | 扫描不可信或未完成，应修复后重跑 |

无论成功还是失败，job summary 都直接渲染 `review-summary.md`。Artifact 只在顶层暴露简报、plan/doctor/run 摘要和一个 `evidence.tar.gz`。将 `review / <provider> release-gate review` 设为 required check 后，`BLOCK`、`ERROR` 和 doctor `BLOCKED` 都会阻塞。

### 4. 安全边界

- reusable workflow 只在私有仓库的可信同仓 PR 上运行；fork PR、Dependabot PR 和 draft PR 会在 caller 与 reusable workflow 两层跳过。
- CI 固定传 `--execution-profile production-ci`：Codex 使用 `read-only` sandbox，并忽略用户配置、execpolicy rules 与 session 持久化；Claude Code 使用只读 `plan` 权限、`safe-mode` 和空 MCP 配置。个人路径仍默认使用完整原生能力。
- Service/Runner 若增加网络、apps、plugins、web search 或本地代码读取限制，必须按[公司 CI 结果 envelope 合同](docs/company-ci-review-result-envelope.md)版本化外围 policy；policy 变化不能复用旧结果或继续 INCREMENTAL。
- 使用专用 runner 和专用低权限系统用户；不要在该用户下保存与审查无关的凭据。用 `code-quality` runner group 限制可调度仓库，不要把带登录态的 runner 暴露给公开仓库的任意工作流。
- 不要改成 `pull_request_target` 后再检出或审查 PR head；这会让持久机器登录态接触不可信代码。
- job 只有 `contents: read`，checkout 使用 `persist-credentials: false`，不授予 `contents: write` 或 `pull-requests: write`。
- artifact 的读取权限沿用仓库权限。当前每个 runner 系统用户同一时间只允许一个原生审查，因此这台机器的 runner 并发必须与该限制匹配。

### 非 GitHub CI

Jenkins 使用独立的 [生产接入说明](docs/jenkins-production-ci.md)。GitLab CI 或其他 runner 使用相同契约：在实际运行 job 的 Linux 系统用户下预装固定版本 `quality-review`，并预先安装、登录一个 Provider；job 完整拉取 Git 历史，显式传入 base/target SHA，并把证据目录作为 `always` artifact 上传。

先把四个变量映射清楚：`WORKSPACE` 是 checkout 的绝对路径；`CI_TMP_DIR` 是 checkout 之外的绝对临时目录（可由 `mktemp -d` 创建）；`BASE_SHA` 和 `TARGET_SHA` 是完整 commit SHA。GitLab MR 通常分别使用 `CI_MERGE_REQUEST_DIFF_BASE_SHA` 与 `CI_COMMIT_SHA`。然后运行，例如 Claude Code：

```sh
claude auth status --json
test "$(quality-review version)" = 'quality-review v0.5.6'

review_args=(--repo "$WORKSPACE"
  --base "$BASE_SHA" --target "$TARGET_SHA"
  --diff-reason ci_explicit_commit_range
  --review-scope full
  --execution-profile production-ci
  --model sonnet
  --reasoning-effort max)
quality-review plan --host claude-code "${review_args[@]}"
quality-review doctor --host claude-code "${review_args[@]}"
quality-review run-claude "${review_args[@]}" --output-root "$CI_TMP_DIR/code-quality"
```

Codex runner 先以 job 的系统用户运行 `codex login status` 与 `codex exec --help` 验证现有环境，再使用 `doctor --host codex` 和 `run-codex`；无需导出 API key 或创建临时 `CODEX_HOME`。

## 运行语义

CLI 会固定并隔离 committed base-to-target，然后只启动一次顶层原生 Provider。个人路径默认使用 Codex `gpt-5.6-sol / max` 或 Claude Code `opus / max`，并保留宿主正常配置与能力；公司 Linux CI 使用 Codex `gpt-5.6-sol / max` 或 Claude Code `sonnet / max`，并由 `production-ci` profile 限制为只读、无自定义扩展的审查。包装层只增加简明 findings JSON Schema，不增加自研 rubric、复审或重试。

结构化 findings 中存在 P0/P1 时返回 `BLOCK`；只有 P2/P3 或 findings 为空时返回 `PASS`，advisory 仍保留在 Markdown 和 JSON 简报中；格式、Provider 或证据失败返回 `ERROR`。同一系统用户同时只允许一个原生审查。

## 维护者发布

```sh
make release-check VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git tag -a vX.Y.Z -m "vX.Y.Z"
make dist VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git push --atomic origin main vX.Y.Z
gh release create vX.Y.Z dist/* --title vX.Y.Z --notes-file <release-notes>
```
