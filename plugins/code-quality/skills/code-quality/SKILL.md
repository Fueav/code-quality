---
name: code-quality
summary: 使用当前宿主的完整原生能力，对一个已提交改动执行单次、只报告的代码审查。
description: 当用户要求审查已提交改动中的可执行缺陷时使用；可携带用户明确给出的目标。
---

# Code Quality

使用当前宿主的完整原生审查能力，不要在宿主 Prompt 中复制审查方法论。

1. 在 Codex 中运行 `quality-review run-codex --repo <repo>`；在 Claude Code 中运行 `quality-review run-claude --repo <repo>`。仅当用户明确给出意图或关注点时添加 `--goal <intent>`；显式范围必须同时提供 `--base <base> --target <target>`，`--diff-reason <reason>` 也只转交用户给出的原因。
2. CLI 固定并隔离已提交范围，只发起恰好一次顶层原生 Provider 调用，再冻结原始输出并做确定性分类。Codex 保留正常配置、工具与 Skills；不得削减 Claude Code 的工具、MCP、插件、设置或用户上下文，也不得增加自研编排、验证器或重试。每个系统用户只允许一个活动审查；遇到占用时不要重试或绕过，可直接使用当前宿主审查或等待占用结束。
3. 汇报状态、语义结果、Provider 调用次数、raw freeze path、metrics path 和最终报告路径。不要删除 session 目录或报告；审查永远不改变 CI 成功状态。
