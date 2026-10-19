---
name: code-quality
description: 审查已提交代码中的可执行缺陷，使用当前宿主的原生审查与只读裁决。
---

# Code Quality

是否需要审查由项目集成契约和用户要求决定。定位 `quality-review`，运行 `<bin> version` 确认版本为 `v0.5.12`。缺失时，仅在已授权安装的范围内使用固定版本 bootstrap；安装入口见仓库 README。

在 Codex 使用 `<bin> run-codex --repo <repo>`，Claude Code 使用 `<bin> run-claude --repo <repo>`。CLI 内部校验范围并冻结证据，`plan` 和 `doctor` 仅用于诊断。沿用用户选择的 `--model`、`--reasoning-effort` 和 `--execution-profile`；显式比较使用成对的 `--base-ref` / `--head-ref`，或精确 SHA 的 `--base` / `--target`。只有用户给出关注点时添加 `--goal`。

可信上次结果可用 `--review-scope incremental --previous-result <review-result.json>`。遵循 CLI 的恢复状态：`RESTRICTED_RETRYABLE` 只对返回的 `session_dir` 执行一次 `resume-restricted --session`；`FULL_REQUIRED` 表示前次证据不能用于本次增量审查，按项目集成契约决定完整审查或保持必需审查未完成，不自动扩大审查；`MANUAL_REQUIRED` 停止自动审查。活动审查结束前不重试。证据使用 CLI 默认仓库外目录，保留原始证据和宿主设置。

只展示 `review-summary.md`：`PASS` 表示本次审查通过，`BLOCK` 有保留的 P0/P1，`ERROR` 不能作为可信结论或放行必需审查。advisory 非阻断，不自动触发修复或新一轮审查；不展示被过滤的问题正文。审查本身不修改代码、Git、CI 或外部状态。
