# 发布版使用链路观察

## 已验证通过

- `curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh` 在 macOS arm64 成功安装 `quality-review v0.1.1` 到 `~/.local/bin/quality-review`。
- 通过本地 marketplace wrapper 安装的 plugin payload 与 Git tag `v0.1.1` 中 `.codex-plugin/plugin.json`、`skills/code-quality/SKILL.md` 的 SHA-256 完全一致。
- 在允许写 Git 元数据的环境中，6/6 有效 plugin run 都执行了 `prepare → workflow → finalize`，语义结果均为 `MANUAL_REVIEW`。

## 使用链路阻点

1. GitHub tag `v0.1.1` 不含 Codex 所需的 `.agents/plugins/marketplace.json`。在 Codex CLI 0.145.0 中，直接把 `Fueav/code-quality` 或 `plugins/code-quality` 当 marketplace 添加会报“marketplace root does not contain a supported manifest”。本轮 wrapper 只补索引，plugin 内容未改。
2. 在 `workspace-write` sandbox 下，6/6 plugin run 都在 `prepare` 创建 `.git/worktrees` 时被拒绝，无法生成正式报告；改用一次性匿名 fixture + `danger-full-access` 后 6/6 跑通。对外文档需要给出权限前提，或让 CLI 避免要求宿主 Git 元数据可写。
3. 一次成功 run 在 `finalize` 后把 `.code-quality` 会话目录清理掉，导致正式 JSON/Markdown 路径失效；最终文本和执行事件仍证明 `COMPLETE/MANUAL_REVIEW`。Skill 应明确“保留 finalize 产物，`.code-quality` 不算需回滚的工作树修改”。
4. `review-result.json` 的 `execution` 字段仍把 token、duration、retry 记为 null；本轮只能从 Codex JSONL 外围事件恢复 code-quality lane 的用量。内置 `codex exec review --json` 则为所有 run 报告零 token，无法做公平成本对比。

这些是本机 Codex CLI 0.145.0 与 v0.1.1 的实测事实，不外推到 Claude Code 或未来 Codex 版本。
