# code-quality (`quality-review`)

Report-only 代码质量审查组件。它对一个**已提交的代码增量**调用 Codex 原生 review，发现本次改动引入或加重的可执行缺陷，输出结构化报告——**只给建议，不阻断合并、不改 CI**。

## 形态

- **`quality-review`** — 静态 Go CLI，固定审查范围并调用本机已登录的 Codex CLI 原生 review 模式。
- **`plugins/code-quality/`** — 薄 Skill，只负责触发 CLI，不复制审查方法论。

## 安装

**1) CLI 二进制**（自动判平台、校验 sha256、装到 `~/.local/bin`）：

```sh
# 最新版
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh
# 指定版本
curl -fsSL https://github.com/Fueav/code-quality/releases/latest/download/install.sh | sh -s -- v0.4.0
```

确认：`quality-review version`

**2) Plugin**：按宿主复制整行命令；它会登记本仓库 marketplace 并安装 `code-quality`：

```sh
# Codex
codex plugin marketplace add Fueav/code-quality --ref v0.4.0 && codex plugin add code-quality@fueav-code-quality

# Claude Code
claude plugin marketplace add Fueav/code-quality@v0.4.0 && claude plugin install code-quality@fueav-code-quality
```

仓库根目录同时提供 Codex 的 `.agents/plugins/marketplace.json` 与 Claude Code 的 `.claude-plugin/marketplace.json`，两端共用 `plugins/code-quality/` 的同一份 Skill。

## 使用

在 Codex 会话里用自然语言触发，或直接运行：

```sh
quality-review run-codex --repo . --goal "这次改动的意图或额外关注点"
```

默认链路只有一次语义调用：确定性固定并隔离 base→target；执行一次 `codex exec review`；确定性适配原生结果并发布报告。`--goal` 只在用户显式提供时加入，作为可选关注点而不是审查边界。系统不再自动选择方向，也不追加验证器、复审或重试。

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
