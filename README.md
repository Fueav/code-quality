# code-quality (`quality-review`)

Report-only 代码质量审查组件。它让当前宿主的完整原生 Agent（Codex 或 Claude Code）审查一个**已提交的代码增量**，冻结原始输出后再做薄的确定性分类——**只给建议，不阻断合并、不改 CI**。

## 形态

- **`quality-review`** — 静态 Go CLI，固定审查范围并调用本机已登录的完整 Codex 或 Claude Code Agent。
- **`plugins/code-quality/`** — 薄 Skill，只负责触发 CLI，不复制审查方法论。

## 安装

**1) CLI 二进制**（自动判平台、校验 sha256、装到 `~/.local/bin`）：

```sh
# 最新版
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh
# 指定版本
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh -s -- v0.4.2
```

确认：`quality-review version`

**2) Plugin**：按宿主复制整行命令；它会登记本仓库 marketplace 并安装 `code-quality`：

```sh
# Codex
codex plugin marketplace add Fueav/code-quality --ref v0.4.2 && codex plugin add code-quality@fueav-code-quality
```

已发布的 v0.4.2 仍只对 Codex 路径做过发布资格验证；源码中的 Claude Code 路径须在下一版本完成发布门禁后才构成正式版本声明。

## 使用

在 Codex 或 Claude Code 会话里用自然语言触发，或按宿主直接运行：

```sh
quality-review run-codex --repo . --goal "这次改动的意图或额外关注点"
quality-review run-claude --repo . --goal "这次改动的意图或额外关注点"
```

显式范围只需同时传 `--base <base> --target <target>`；`--diff-reason` 是可选审计说明，未提供时使用确定性的 `explicit_commit_range`。

默认链路只有一次顶层 Provider 调用：确定性固定并隔离 base→target；Codex 以 `gpt-5.6-sol` / `max` 执行一次普通 `codex exec`，Claude Code 以 `opus` / `max` 执行一次普通 `claude -p`。包装层不限制 Claude Code 的正常配置、`CLAUDE.md`、settings、Skills、commands、plugins、MCP/connectors、内置工具、Git 历史或网络；Git worktree 只隔离待审查代码，并保留原仓库的自动记忆身份，若 worktree 不可用则 Claude 路径明确失败，绝不回落到具有新记忆身份的 shared clone。Claude 使用原生 `auto` 权限模式，不传 `--safe-mode`、`--bare`、`--disable-slash-commands`、`--strict-mcp-config`、工具 allowlist、`--setting-sources`、自定义 `CLAUDE_CONFIG_DIR`/`HOME`、turn/budget 上限或受限权限模式；若本机版本不支持 `auto`，运行明确返回 `INCOMPLETE`，不会静默降级。原始最终回复和 JSONL 先冻结再分类；`--goal` 只转交用户上下文，系统不注入 rubric、输出 schema、验证器、复审或重试。

同一系统用户同时只允许一个原生审查。CLI 通过系统 UID 账户记录定位经过所有权验证的 home 目录，只读打开并锁定该目录 inode，不创建锁文件，也不受 `HOME`、`TMPDIR` 或 `XDG_CACHE_HOME` 改写影响；同一描述符会交给原生 Provider 子进程，嵌套或并发重复调用不会启动第二个 Provider，最后一个持有进程退出后锁自动释放。静态 Linux 构建在 `/etc/passwd` 缺少当前 UID 时明确失败，不回退到环境变量。

每次运行保留 `native-review.txt`、JSONL、stderr、`native-review-freeze.json` 和 `native-run-metrics.json`。分类器不解析 Markdown：只有整份输出精确等于受支持的无发现哨兵才返回 `PASS`；其他非空输出统一返回 `MANUAL_REVIEW` 并以冻结原文为准，进程失败或缺失/空输出返回 `INCOMPLETE`。

20 条 V1.2 底线保留为离线评测量尺，不注入运行时 Prompt。历史合成样本只用于回归和校准，不作为产品优越性门槛；未来 A/B 必须使用独立资格审查后的冻结样本，并把新发现的合理缺陷标记为争议样本而不是直接计作误报。

## 对照评测

`quality-review compare --product <findings.json> --baseline <findings.json>` 接受两份来源无关的 finding set，输出仅本产品、仅对照、双方共有三个分区，并附带逐条人工判定模板。每条输入包含 `id`、`comparison_key`、`dimension`、`code_locations` 和 `description`；两侧属于同一问题时由评测准备者赋相同 `comparison_key`。引擎不猜测语义等价，也不自动判断发现是否有效。

## 版本冻结

每个 release tag 冻结 schema、确定性范围逻辑、CLI 与离线评测量尺，报告可追溯到具体版本。

## 发布（维护者）

```sh
make release-check VERSION=vX.Y.Z VERIFY_COMPARE_REF=vPREVIOUS
git tag -a vX.Y.Z -m "vX.Y.Z"
make dist VERSION=vX.Y.Z    # 交叉编译到 dist/ + checksums.txt + install.sh
git push origin main vX.Y.Z
gh release create vX.Y.Z dist/* --title vX.Y.Z --notes "…"
```
