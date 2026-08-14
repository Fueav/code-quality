---
name: code-quality
summary: 使用当前 Codex 或 Claude Code 的完整原生能力，预检并审查一个已提交改动。
description: 当用户要求检查、review 或审查当前分支已提交改动中的可执行缺陷时使用；也用于首次安装后的自然语言审查。
---

# Code Quality

使用当前宿主的完整原生审查能力，不要复制审查方法论。

1. 用 `command -v quality-review` 定位 CLI；若未找到，检查 `$HOME/.local/bin/quality-review`。对候选 CLI 运行 `<bin> version`，只有输出精确等于 `quality-review v0.5.7` 才继续；不存在或版本不一致时，运行 `curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.7/bootstrap.sh | sh -s -- v0.5.7 <codex|claude>`，并使用其输出的 `QUALITY_REVIEW_BIN` 绝对路径；不要修改 shell profile。
2. 先解析一组合同参数并原样复用：Provider 对应的 `--model`、`--reasoning-effort max`、`--execution-profile <personal|production-ci>`、范围、scope、previous result、goal 与 diff reason。Codex 依次运行 `<bin> plan --host codex --repo <repo>` 和 `<bin> doctor --host codex --repo <repo>`，Claude Code 使用 `<bin> plan --host claude-code` 与 `<bin> doctor --host claude-code --repo <repo>`；每条命令都追加同一组合同参数。显式方向成对传 `--base-ref` / `--head-ref`，旧精确范围成对传 `--base` / `--target`；doctor `BLOCKED` 时只汇报 `next_action`。
3. doctor 为 `READY` 后，在 Codex 运行 `<bin> run-codex --repo <repo>`，在 Claude Code 运行 `<bin> run-claude --repo <repo>`，继续追加同一组合同参数。仅当用户明确给出关注点时添加 `--goal`；只有调用方已有可信上一份结果时，才可使用 `--review-scope incremental --previous-result <review-result.json>`。
4. CLI 只审查冻结的 committed 范围：先做一次原生发现；仅在出现 P0/P1 时，再用同一 Provider 做一次强制只读的 V1.2 受限裁决。`FULL_REQUIRED` / exit 4 表示增量合同不成立，应改跑 FULL；`MANUAL_REQUIRED` / exit 5 表示 `FULL → INCREMENTAL` 两轮已经用完，必须转人工，Harness 不得启动第三轮。两者都是 Provider 调用数为零的 plan 状态，不是 `PASS / BLOCK / ERROR`。个人发现调用不得临时改写宿主工具、MCP、插件、设置或上下文；公司外围限制以 `runner_policy_version` 隔离复用。Harness 负责修复编排，遇到活动审查不要重试或绕过。
5. 默认证据根目录由 CLI 在仓库外的系统临时区安全创建，不得改到被审查仓库内。只向用户展示 `review-summary.md`：`PASS` 表示没有达到生产下限的 P0/P1，可继续发布，P2/P3 advisory 仍保留；`BLOCK` 表示受限裁决保留了 P0/P1；`ERROR` 表示扫描或裁决不可信。被过滤的候选正文只留在 `evidence_dir` 的冻结原始证据中，不向用户复述；不要删除证据，不要修改代码、Git、CI 或外部状态。
