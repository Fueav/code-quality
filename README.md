# code-quality (`quality-review`)

Report-only 代码质量审查组件。在 Claude Code / Codex 会话里对一个**已提交的代码增量**做审查，发现本次改动引入或加重的生产底线缺陷（正确性、数据、稳定性、安全、兼容性），输出结构化报告——**只给建议，不阻断合并、不改 CI**。

## 形态

- **`quality-review`** — 静态 Go CLI（确定性引擎：基线固定、schema 校验、裁决、渲染报告）。不调模型、不需要 API key。
- **`plugins/code-quality/`** — Claude Code 与 Codex 共用的薄 Skill，在会话里触发流程。模型能力借用你当前已登录的 Claude Code / Codex 会话。

## 安装

**1) CLI 二进制**（自动判平台、校验 sha256、装到 `~/.local/bin`）：

```sh
# 最新版
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh
# 指定版本
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh -s -- v0.2.0
```

确认：`quality-review version`

**2) Plugin**：按宿主复制整行命令；它会登记本仓库 marketplace 并安装 `code-quality`：

```sh
# Codex
codex plugin marketplace add Fueav/code-quality --ref v0.2.0 && codex plugin add code-quality@fueav-code-quality

# Claude Code
claude plugin marketplace add Fueav/code-quality@v0.2.0 && claude plugin install code-quality@fueav-code-quality
```

仓库根目录同时提供 Codex 的 `.agents/plugins/marketplace.json` 与 Claude Code 的 `.claude-plugin/marketplace.json`，两端共用 `plugins/code-quality/` 的同一份 Skill。

## 使用

在 Claude Code / Codex 会话里，用自然语言触发（例如「帮我审一下这个改动」）。Skill 会：

1. `prepare` —— 钉住本次 commit 增量（base→target diff）；
2. 当前会话按 20 条底线规则审查引入/加重的缺陷；
3. `finalize` —— 零发现时明确要求一次复审，否则产出 `review-result.json` + `review-result.md`（report-only 建议）。

使用者无需配置模型、无需了解内部规则。

## 对照评测

`quality-review compare --product <findings.json> --baseline <findings.json>` 接受两份来源无关的 finding set，输出仅本产品、仅对照、双方共有三个分区，并附带逐条人工判定模板。每条输入包含 `id`、`comparison_key`、`dimension`、`code_locations` 和 `description`；两侧属于同一问题时由评测准备者赋相同 `comparison_key`。引擎不猜测语义等价，也不自动判断发现是否有效。

## 版本冻结

每个 release tag 把 policy（20 条规则）、schema、确定性引擎、CLI 一起冻结成一个不变版本，报告可追溯到具体版本。

## 发布（维护者）

```sh
make test
git tag vX.Y.Z && git push origin vX.Y.Z
make dist VERSION=vX.Y.Z    # 交叉编译到 dist/ + checksums.txt + install.sh
gh release create vX.Y.Z dist/* --title vX.Y.Z --notes "…"
```
