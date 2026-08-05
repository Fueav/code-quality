# code-quality (`quality-review`)

用 Codex 或 Claude Code 的完整原生 Agent 审查一个**已提交的 Git 增量**，冻结原始证据并给出 `PASS / MANUAL_REVIEW / INCOMPLETE`。工具固定为 `report_only`：只给建议，不修改代码、Git、CI 或远端状态。

## 个人开发者：一句话安装并审查

开始前只确认三件事：当前是 macOS 或 Linux（arm64/amd64）；Codex 或 Claude Code CLI 已安装并登录；当前目录是 Git 仓库。然后先提交要审查的改动——未提交文件不属于审查范围。

在需要审查的仓库中，把下面整句话交给 Codex 或 Claude Code：

> 请为当前仓库安装并运行 Fueav code-quality v0.5.1；固定版本安装入口是 https://github.com/Fueav/code-quality/releases/download/v0.5.1/bootstrap.sh。请自动识别当前是 Codex 还是 Claude Code，使用对应的 `codex` 或 `claude` 参数完成 CLI 与插件安装，再检查宿主登录、版本、PATH、Git 基线、已提交差异和未提交文件。预检不通过时不要启动审查，只告诉我一个下一步；通过后执行一次只报告审查，用中文汇报结论和证据路径，不修改代码、Git、CI、远端或部署状态。

Agent 会根据当前宿主执行下面一个固定版本入口：

```sh
# Codex
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.1/bootstrap.sh | sh -s -- v0.5.1 codex

# Claude Code
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.1/bootstrap.sh | sh -s -- v0.5.1 claude
```

bootstrap 会同时安装 CLI 和对应插件，并输出 `QUALITY_REVIEW_BIN=<绝对路径>` 及下一条 doctor 命令。首次运行使用这个绝对路径，不依赖当前 shell 是否已包含 `~/.local/bin`；bootstrap 不会修改 shell profile。

如果要自己执行，先 doctor，只有状态为 `READY` 才运行审查：

```sh
# Codex
quality-review doctor --host codex --repo .
quality-review run-codex --repo . --goal "这次改动的意图或额外关注点"

# Claude Code
quality-review doctor --host claude-code --repo .
quality-review run-claude --repo . --goal "这次改动的意图或额外关注点"
```

`--goal` 只在确实有额外意图或关注点时添加。显式范围必须同时传 `--base <base> --target <target>`；`--diff-reason` 是可选审计说明。默认会比较本地 `origin/HEAD` 与 `HEAD` 的 merge-base 到 `HEAD`。

### 手工安装与排障

升级时优先直接重跑上面的固定版本 bootstrap；它会让 CLI 与当前宿主插件保持同一版本。只有排查安装问题时才需要下面的分步命令。

CLI 安装器会判断平台、校验 SHA-256，并安装到 `~/.local/bin`（可用 `INSTALL_DIR` 覆盖）：

```sh
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.1/install.sh | sh -s -- v0.5.1
```

Codex plugin：

```sh
codex plugin marketplace add Fueav/code-quality --ref v0.5.1
codex plugin add code-quality@fueav-code-quality
```

Claude Code plugin 使用 HTTPS 和固定 Tag，不要求 GitHub SSH：

```sh
claude plugin marketplace add https://github.com/Fueav/code-quality.git#v0.5.1
claude plugin install code-quality@fueav-code-quality --scope user
```

在已打开的 Claude Code 会话中安装或升级后，运行 `/reload-plugins`；新会话会自动加载。

### 你会得到什么

- `PASS`：原生审查精确返回无发现哨兵。
- `MANUAL_REVIEW`：原生审查返回了需要人阅读的内容；这不等于审查失败。
- `INCOMPLETE`：Provider 进程、证据冻结或报告发布不完整，应修复执行问题后重跑。

每次运行都会保留 `native-review.txt`、stdout JSONL、stderr、`native-review-freeze.json`、`native-run-metrics.json` 和最终报告。JSON 摘要中的 `session_dir` 是完整证据目录。

原生审查默认在仓库外的系统临时区创建一次运行独占、权限为 `0700` 的证据根目录。CLI 不会主动删除报告；长期归档时可传宿主允许写入、仓库外的绝对 `--output-root`。相对路径或经 symlink 解析回仓库内的路径会在创建 session 前被拒绝。

## 公司 CI：集中配置、仓库最小接入

CI 不需要安装 Codex 或 Claude Code 插件。推荐由平台团队集中维护本仓库的 [`.github/workflows/code-quality-reusable.yml`](.github/workflows/code-quality-reusable.yml)、一个专用 Provider API key 和版本升级；业务仓库只调用 reusable workflow。

这个入口固定安装 `quality-review v0.5.1`、Codex CLI `0.145.0` 或 Claude Code `2.1.220`，一次只调用一个 Provider，默认 `reasoning_effort: low`。它不会写 PR 评论，也不会把模型发现直接变成合并门禁。

### 1. 平台团队配置机器身份

在 GitHub Organization 或业务仓库创建一个 CI 专用 secret，并只授权需要试点的仓库：

| Provider | 建议的组织 secret | reusable workflow 内的使用方式 |
| --- | --- | --- |
| Claude Code | `CODE_QUALITY_CLAUDE_API_KEY` | 映射为 `provider_api_key`，仅在 doctor/run 步骤作为 `ANTHROPIC_API_KEY` 注入 |
| Codex | `CODE_QUALITY_OPENAI_API_KEY` | 映射为 `provider_api_key`，写入 runner 临时 `CODEX_HOME` 的机器登录 |

不要把个人订阅登录目录、个人 token 或本机配置复制进 CI。试点时先选一个 Provider；若要双端对比，创建两个独立 caller job 和两把独立受限密钥。

### 2. 业务仓库添加最小 caller

在业务仓库创建 `.github/workflows/code-quality.yml`：

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
    uses: Fueav/code-quality/.github/workflows/code-quality-reusable.yml@v0.5.1
    with:
      provider: claude
      base_sha: ${{ github.event.pull_request.base.sha }}
      target_sha: ${{ github.event.pull_request.head.sha }}
      model: sonnet
      reasoning_effort: low
      artifact_retention_days: 14
    secrets:
      provider_api_key: ${{ secrets.CODE_QUALITY_CLAUDE_API_KEY }}
```

示例固定引用已经包含该 reusable workflow 的 `v0.5.1`，不要改为 `main`。workflow 内部也固定使用同版本 CLI。改用 Codex 时只需把 `provider` 改为 `codex`、`model` 改为 `gpt-5.6-sol`，并把 secret 映射到 `CODE_QUALITY_OPENAI_API_KEY`。

`base_sha` 和 `target_sha` 必须是可解析的完整 40 位 commit SHA；workflow 会完整拉取历史并精确检出 `target_sha`。对于 GitHub PR，使用上面 event 中的 base/head SHA。每次 PR 更新都会取消同一 Provider 的旧 run，避免浪费额度。

### 3. 把“执行健康”设为 required check

CI 的门禁含义是“审查是否完整执行”，不是“模型是否报告了问题”：

| 结果 | CI 结论 | 含义 |
| --- | --- | --- |
| doctor `READY` | 继续 | CLI、身份、Git 范围与已提交差异可用 |
| doctor `BLOCKED` | 失败 | 不调用模型；先按 doctor 给出的下一步修复 |
| `PASS` | 成功 | 完整执行，原生审查返回无发现哨兵 |
| `MANUAL_REVIEW` | 成功 | 完整执行且有内容需要人读；保持 report-only |
| `INCOMPLETE` 或配置错误 | 失败 | Provider、证据或输入不完整，应重跑或修复配置 |

无论成功还是失败，job summary 都会给出摘要，并上传 doctor 输出、运行摘要和完整 session artifact。将 `review / <provider> report-only review` 设为 required check 后，`MANUAL_REVIEW` 不会阻塞合并，`BLOCKED / INCOMPLETE` 会阻塞。

### 4. 安全边界

- 只在可信的同仓 PR 上运行；fork PR、Dependabot PR 和 draft PR 会在 caller 与 reusable workflow 两层跳过，因为 Provider 拥有正常的工具和网络能力。[GitHub 官方限制](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-on-actions)说明 Actions secrets 不会提供给 Dependabot 触发的普通 PR workflow。
- 不要改成 `pull_request_target` 后再检出或审查 PR head；这会把密钥暴露给不可信代码。
- job 只有 `contents: read`，checkout 使用 `persist-credentials: false`，不授予 `contents: write` 或 `pull-requests: write`。
- Provider API key 只用于这个 job，按仓库限制访问范围并定期轮换；artifact 的读取权限沿用仓库权限。
- 当前每个 runner 系统用户同一时间只允许一个原生审查。GitHub-hosted runner 每个 job 独立，不会互相争用。

### 非 GitHub CI

GitLab CI、Jenkins 或其他 runner 使用相同契约：完整拉取 Git 历史，安装固定版本 CLI 与一个 Provider CLI，注入专用 API key，显式传入 base/target SHA，并把证据目录作为 `always` artifact 上传。

先把四个变量映射清楚：`WORKSPACE` 是 checkout 的绝对路径；`CI_TMP_DIR` 是 checkout 之外的绝对临时目录（可由 `mktemp -d` 创建）；`BASE_SHA` 和 `TARGET_SHA` 是完整 commit SHA。GitLab MR 通常分别使用 `CI_MERGE_REQUEST_DIFF_BASE_SHA` 与 `CI_COMMIT_SHA`；Jenkins 应在完整 fetch 后用目标分支和 `GIT_COMMIT` 计算 merge-base。然后运行，例如 Claude Code：

```sh
npm install --global @anthropic-ai/claude-code@2.1.220
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.1/install.sh |
  INSTALL_DIR="$CI_TMP_DIR/quality-bin" sh -s -- v0.5.1

export ANTHROPIC_API_KEY="$CODE_QUALITY_CLAUDE_API_KEY"
"$CI_TMP_DIR/quality-bin/quality-review" doctor --host claude-code --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA"
"$CI_TMP_DIR/quality-bin/quality-review" run-claude --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA" --diff-reason ci_explicit_commit_range \
  --model sonnet --reasoning-effort low --output-root "$CI_TMP_DIR/code-quality"
```

Codex runner 需要安装 `@openai/codex@0.145.0`，创建临时 `CODEX_HOME`，再用 `printenv OPENAI_API_KEY | codex login --with-api-key` 建立机器身份；其余 doctor、范围、退出码和 artifact 规则相同。

## 运行语义

CLI 会固定并隔离 committed base-to-target，然后只启动一次顶层原生 Provider。个人路径默认使用 Codex `gpt-5.6-sol / max` 或 Claude Code `opus / max`；公司 CI 为控制成本，默认使用 Codex `gpt-5.6-sol / low` 或 Claude Code `sonnet / low`。包装层不注入自研 rubric、输出 schema、复审或重试，也不削减宿主正常配置、规则、Skills、工具、MCP、插件或网络能力。

精确无发现哨兵返回 `PASS`；其他非空原生输出返回 `MANUAL_REVIEW` 并以冻结原文为准；进程或证据失败返回 `INCOMPLETE`。同一系统用户同时只允许一个原生审查。

## 维护者发布

```sh
make release-check VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git tag -a vX.Y.Z -m "vX.Y.Z"
make dist VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git push --atomic origin main vX.Y.Z
gh release create vX.Y.Z dist/* --title vX.Y.Z --notes-file <release-notes>
```
