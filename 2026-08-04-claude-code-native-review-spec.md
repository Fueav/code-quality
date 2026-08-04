# Claude Code 原生审查兼容规格

状态：已实现并通过本地及真实仓库验收，待版本发布
日期：2026-08-04

## 1. 目标

`quality-review` 同时支持 Codex 与 Claude Code，并继续只做原生审查能力的薄封装。工具负责确定提交范围、准备一次性检出、启动一次原生 CLI、冻结原始证据并发布三态结果；不实现自己的审查方法、候选编排、复核器或重试链。

## 2. 不可破坏的事实

1. `run-codex` 仍调用一次顶层 `codex exec`，现有行为与结果语义不变。
2. `run-claude` 调用一次顶层 `claude -p`。Claude 在这次原生运行内部如何使用模型、工具或子代理，由 Claude 自己决定，包装层不得重编排。
3. 隔离对象只有待审查代码的检出目录，不隔离 Claude 的正常运行环境。
4. 两个 Provider 都使用提交级 `base..target` 范围；用户未提交的工作树修改不纳入审查。
5. 包装层只做确定性的 `PASS`、`MANUAL_REVIEW`、`INCOMPLETE` 分类；原生 `native-review.txt` 是审查内容的权威证据。

## 3. Claude 能力等价契约

Claude 进程必须继承调用者的普通环境和用户配置，因此可以正常读取项目及用户级 `CLAUDE.md`、settings、Skills、commands、plugins、MCP/connectors、内置工具、Git 历史与网络能力。

基线调用使用 Claude 原生自动权限模式：

```text
claude -p --output-format stream-json --verbose --no-session-persistence \
  --permission-mode auto --model <model> --effort <effort> <prompt>
```

以下能力削减不得出现在基线调用中：

- `--safe-mode`
- `--bare`
- `--disable-slash-commands`
- `--strict-mcp-config`
- `--tools` 或任何工具 allowlist
- `--setting-sources`
- 自定义或缩减后的 `CLAUDE_CONFIG_DIR`、`HOME`
- `--json-schema`、`--max-turns`、`--max-budget-usd`
- `plan`、`dontAsk` 等受限权限模式

不得在 `auto` 不受支持时静默降级。此类失败必须保留原始 stderr，并发布 `INCOMPLETE`。

`run-claude` 的一次性检出必须是原仓库注册的 Git worktree，以保留原项目的自动记忆身份。不得回落到拥有独立项目身份的 shared clone；worktree 创建失败时必须在启动 Claude 前明确失败。历史 `prepare/finalize` 离线路径不启动原生 Claude 进程，不属于此限制。

Prompt 只提供审查范围、可选用户目标和“只报告，不修改、提交、推送、部署或改变外部状态”的动作边界，不注入自研 rubric、步骤或输出改写规则。

## 4. Provider 接口与一次调用语义

公共执行核心只依赖 Provider 的以下职责：

- 提供 `host`、默认模型和默认 effort；
- 构造原生 CLI 调用；
- 从冻结的 JSONL 解析用量；
- 必要时从冻结的 JSONL 提取最终消息。

结果 schema 升级到 v4，使用 `provider_invocations: 1` 表示包装层启动了一次顶层原生 CLI。它不声称底层只有一次模型 API 调用，也不限制 Claude 原生运行内部的工具步骤或子代理。

## 5. 证据与失败语义

每次运行冻结三份只读原始证据：

1. `native-review.txt`
2. `native-review.stdout.log`
3. `native-review.stderr.log`

Codex 继续由 `--output-last-message` 产生最终消息。Claude 的最终消息来自 `stream-json` 中唯一、完成的 `result` 事件。包装层在进程结束后先解析 JSONL，再以独占创建方式写入最终消息；冻结时必须重新解析被锁定的 JSONL，并确认其结果与最终消息逐字节一致。缺失、重复、错误或不完整的 Claude `result` 事件均不得伪装成成功结果。

进程失败、输出缺失、证据无法冻结、权限模式不可用或协议无法解析时，结果为 `INCOMPLETE`；非空且不是精确无问题哨兵的原生输出为 `MANUAL_REVIEW`。

## 6. CLI 与插件

- 保留 `quality-review run-codex`。
- 新增 `quality-review run-claude`，默认模型 `opus`、effort `max`，支持与 Codex 相同的范围和输出参数。
- 插件说明根据当前宿主选择对应命令，不要求 Claude 安装或调用 Codex。
- README 和插件文案使用中文说明“双原生 Provider”和能力不降级边界。

## 7. 验收

机械契约必须覆盖：

- 两个 Provider 的完整 argv、默认值与禁止参数；
- Claude 子进程继承普通环境，工作目录仅指向一次性检出；
- Claude JSONL 最终消息、用量、错误与证据一致性；
- `claude-code` session、schema v4、三态分类与旧 `run-codex` 回归；
- CLI 和插件双 Provider 路由。

真实验收必须在同级 `Agent Marketplace` 仓库上分别运行直接 Claude 与包装器 Claude，比较初始化事件中可见的工具/MCP/插件能力，并确认包装器仍能完成原生审查、冻结证据且不改变被审查仓库。

## 8. 非目标

- 不接入 Claude 官方 `/code-review` 多代理插件或 `ultrareview`。
- 不评判 Claude 与 Codex 的审查质量高低。
- 不自动重试、交叉验证或结构化改写原生 findings。
- 本次不合并 `main`、不打 Tag、不发布或推送。

## 9. 实现与验收结论

1. 执行核心已抽为 Provider 边界；`run-codex` 保留原 argv 回归契约，`run-claude` 只启动一次顶层 `claude -p`，结果 schema v4 用 `provider_invocations: 1` 表达这层事实。
2. Claude argv、环境继承、唯一成功 `result`、错误结果、用量、最终消息物化、冻结后二次解析与逐字节一致性均有机械契约；能力削减参数均不在调用中。
3. Go 全量测试、资格测试、race、vet、格式、diff 检查、Claude plugin/marketplace 原生校验以及 Darwin/Linux 的 amd64/arm64 交叉构建均通过。
4. 在同级 Agent Marketplace 的真实提交范围上，直接 Claude 与包装器 Claude 都完成了原生审查，并找出相同的实质审计问题；原仓库在运行前后保持干净，包装器的一次性检出已清理。禁止 clone 降级的收口修改完成后，又用最终二进制重跑了一次真实包装器审查并通过。
5. 最终包装器初始化事件使用 `auto` 权限，暴露 28 个工具、4 个当前配置的 MCP servers、57 个 slash commands、5 个 agents、27 个 skills 和 2 个已安装插件；早先同一配置快照下的直接运行与包装器能力字段也逐项一致。实际审查使用了 Bash、Read 与 ReportFindings，说明工具不是只有声明而不可用。
6. 包装器的自动记忆路径解析到原始 Agent Marketplace 项目身份，而不是临时检出身份；直接对照所用的独立 clone 拥有单独身份属于预期差异，不代表包装器丢失上下文。Claude 路径已禁止 shared-clone 回退，worktree 失败时不会启动降级审查。
7. 包装器运行产出 `COMPLETE / MANUAL_REVIEW`、一份成功 `result` 事件、三份只读原始证据和可用 token 指标；Claude Code 没有修改、提交、推送、部署或改变外部状态。
