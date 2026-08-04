# code-quality (`quality-review`)

用当前 Codex 或 Claude Code 的完整原生 Agent 审查一个**已提交的 Git 增量**，冻结原始证据后发布 `PASS / MANUAL_REVIEW / INCOMPLETE`。它只给建议，不修改代码、不阻断合并、不改变 CI。

## 一句话开始

在需要审查的仓库中，把下面整句话交给 Codex 或 Claude Code：

> 请为当前仓库安装并运行 Fueav code-quality v0.5.0；固定版本安装入口是 https://github.com/Fueav/code-quality/releases/download/v0.5.0/bootstrap.sh。请自动识别当前是 Codex 还是 Claude Code，使用对应的 `codex` 或 `claude` 参数完成 CLI 与插件安装，再检查宿主登录、版本、PATH、Git 基线、已提交差异和未提交文件。预检不通过时不要启动审查，只告诉我一个下一步；通过后执行一次只报告审查，用中文汇报结论和证据路径，不修改代码、Git、CI、远端或部署状态。

Agent 使用下面对应的固定版本入口：

```sh
# Codex
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.0/bootstrap.sh | sh -s -- v0.5.0 codex

# Claude Code
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.0/bootstrap.sh | sh -s -- v0.5.0 claude
```

bootstrap 会输出 `QUALITY_REVIEW_BIN=<绝对路径>` 和下一条 doctor 命令。首次运行必须使用该绝对路径，不依赖当前 shell 已包含 `~/.local/bin`。它不会修改 shell profile。

## 使用前提

- macOS 或 Linux，arm64/amd64；
- 当前宿主 CLI 已安装并登录；
- 当前目录是 Git 仓库，并能确定 `origin/HEAD`，或用户明确给出 `base` 与 `target`；
- 要审查的改动已经提交。未提交文件不属于审查范围，官方自然语言路径会在模型调用前停止。

## 预检与运行

```sh
# Codex
quality-review doctor --host codex --repo .
quality-review run-codex --repo . --goal "这次改动的意图或额外关注点"

# Claude Code
quality-review doctor --host claude-code --repo .
quality-review run-claude --repo . --goal "这次改动的意图或额外关注点"
```

`--goal` 仅在用户明确给出意图或关注点时添加。显式范围必须同时传 `--base <base> --target <target>`；`--diff-reason` 是可选审计说明。

## 人工分步安装

升级时优先直接重跑上面的固定版本 bootstrap；它会让 CLI 与当前宿主插件保持同一版本。需要排查安装步骤时，再使用下面的分步命令。

CLI 安装器自动判平台、校验 SHA-256，并安装到 `~/.local/bin`（可用 `INSTALL_DIR` 覆盖）：

```sh
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.0/install.sh | sh -s -- v0.5.0
```

Codex plugin：

```sh
codex plugin marketplace add Fueav/code-quality --ref v0.5.0
codex plugin add code-quality@fueav-code-quality
```

Claude Code plugin 使用 HTTPS 和固定 Tag，不要求 GitHub SSH：

```sh
claude plugin marketplace add https://github.com/Fueav/code-quality.git#v0.5.0
claude plugin install code-quality@fueav-code-quality --scope user
```

若在已打开的 Claude Code 会话中安装或升级，运行 `/reload-plugins`；新会话会自动加载。

## 运行语义

CLI 确定性固定并隔离 committed base→target，然后只启动一次顶层原生 Provider：Codex 默认 `gpt-5.6-sol / max`，Claude Code 默认 `opus / max`。包装层不注入自研 rubric、输出 schema、复审或重试，也不削减宿主正常配置、规则、Skills、工具、MCP、插件或网络能力。

每次运行保留 `native-review.txt`、stdout JSONL、stderr、`native-review-freeze.json`、`native-run-metrics.json` 和最终报告。精确无发现哨兵返回 `PASS`；其他非空原生输出返回 `MANUAL_REVIEW` 并以冻结原文为准；进程或证据失败返回 `INCOMPLETE`。同一系统用户同时只允许一个原生审查。

原生审查默认在仓库外的系统临时区创建一次运行独占、权限为 `0700` 的证据根目录，兼容宿主沙箱且不会在被审查仓库内生成未跟踪目录。CLI 不会主动删除报告；若要跨系统临时目录清理周期长期归档，请使用宿主允许写入、仓库外的绝对 `--output-root`。相对路径或经 symlink 解析回仓库内的路径会在创建 session 前被拒绝。

## 维护者发布

```sh
make release-check VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git tag -a vX.Y.Z -m "vX.Y.Z"
make dist VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git push --atomic origin main vX.Y.Z
gh release create vX.Y.Z dist/* --title vX.Y.Z --notes-file <release-notes>
```
