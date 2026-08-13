---
name: code-quality
summary: 使用当前 Codex 或 Claude Code 的完整原生能力，预检并审查一个已提交改动。
description: 当用户要求检查、review 或审查当前分支已提交改动中的可执行缺陷时使用；也用于首次安装后的自然语言审查。
---

# Code Quality

使用当前宿主的完整原生审查能力，不要复制审查方法论。

1. 用 `command -v quality-review` 定位 CLI；若未找到，检查 `$HOME/.local/bin/quality-review`。对候选 CLI 运行 `<bin> version`，只有输出精确等于 `quality-review v0.5.4` 才继续；不存在或版本不一致时，运行 `curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.4/bootstrap.sh | sh -s -- v0.5.4 <codex|claude>`，并使用其输出的 `QUALITY_REVIEW_BIN` 绝对路径；不要修改 shell profile。
2. Codex 先运行 `<bin> doctor --host codex --repo <repo>`，Claude Code 先运行 `<bin> doctor --host claude-code --repo <repo>`。显式范围必须同时传 `--base` 与 `--target`，`--diff-reason` 只转交用户给出的原因。doctor 为 `BLOCKED` 时不要启动 Provider，只汇报 `next_action`。
3. doctor 为 `READY` 后，在 Codex 运行 `<bin> run-codex --repo <repo>`，在 Claude Code 运行 `<bin> run-claude --repo <repo>`。仅当用户明确给出意图或关注点时添加 `--goal`；显式范围沿用 doctor 的同一组参数。
4. CLI 只审查 committed base→target，发起恰好一次顶层原生 Provider 调用，冻结原始输出后确定性分类。不得削减宿主工具、MCP、插件、设置或上下文，不得增加自研编排、验证器或重试；遇到活动审查不要重试或绕过。
5. 默认证据根目录由 CLI 在仓库外的系统临时区安全创建，适配宿主沙箱；不得改到被审查仓库内。只向用户展示 `review-summary.md`：`PASS` 表示没有 P0/P1 阻断项，可继续发布，P2/P3 advisory 仍保留；`BLOCK` 表示存在 P0/P1，必须先修复；`ERROR` 表示扫描不可信且必须重跑。需要审计时再提供 `evidence_dir`；不要删除证据，不要修改代码、Git、CI 或外部状态。
