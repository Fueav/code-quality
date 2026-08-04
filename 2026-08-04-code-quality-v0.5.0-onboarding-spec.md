# code-quality v0.5.0 双端引导与发布规格

状态：已批准，本地门禁通过，待公开发布
日期：2026-08-04

## 1. 目标

将已经实现的 Codex 与 Claude Code 双原生 Provider 作为 `v0.5.0` 正式发布，并把首次安装与使用收敛为一条可交给任一宿主 Agent 的自然语言请求。用户不需要手工拼接 CLI、marketplace、PATH、Git 范围或诊断命令。

Claude Provider 的执行、隔离、权限、证据和结果语义以 `2026-08-04-claude-code-native-review-spec.md` 为唯一事实源；本规格只定义双端引导、预检与发布契约。

## 2. 版本与分发契约

1. CLI、Claude/Codex plugin descriptor、Claude marketplace、README 固定命令和 Release Tag 必须统一为 `0.5.0` / `v0.5.0`。
2. Release 必须同时包含四个平台二进制、`checksums.txt`、`install.sh` 和 `bootstrap.sh`。
3. `bootstrap.sh` 必须以同一个显式 Tag 安装 CLI 与当前宿主插件：
   - Codex 使用 `Fueav/code-quality --ref v0.5.0`；
   - Claude Code 使用 HTTPS Git URL `https://github.com/Fueav/code-quality.git#v0.5.0`，不得要求 GitHub SSH 配置。
4. bootstrap 必须支持重复执行和从旧版本升级，只修改本产品的 CLI、marketplace 与 plugin，不修改 shell profile、项目代码、Git 状态或其他插件。
5. 首次运行必须使用安装后返回的绝对二进制路径，不依赖安装脚本改变父进程 `PATH`。
6. 默认审查证据必须写到宿主沙箱可写的系统临时区内、由进程安全创建且权限为 `0700` 的独占目录；canonical path 不得位于被审查仓库内，以免创建未跟踪输出并导致后续 doctor 自我阻断。长期归档只允许用户显式提供仓库外且宿主可写的绝对目录；相对路径与经 symlink 解析回仓库内的路径必须在创建 session 前失败。

## 3. 预检契约

新增：

```text
quality-review doctor --host <codex|claude-code> --repo <path>
```

`doctor` 不启动 Provider 或模型，只执行只读检查并输出 JSON：

- 当前宿主二进制存在、可报告版本且具有运行所需 CLI flags；
- 当前宿主已经登录；认证输出不得进入报告；
- review baseline 可确定且包含至少一个已提交文件；
- 工作树为 clean；未提交改动必须在模型调用前阻止自然语言路径；
- READY 报告包含 host、provider path/version、repository、base、target、changed files 和逐项检查；BLOCKED 报告给出一个明确的 `next_action`。

直接 `run-codex` / `run-claude` 的既有 committed-only 契约不在本次改写；官方 Skill 必须先运行 `doctor`，只在 READY 时启动 Provider。

## 4. 引导契约

README 首屏必须提供一条可复制的中文自然语言请求。宿主 Agent 按当前宿主完成：

1. 下载并运行固定 `v0.5.0` 的 bootstrap；
2. 使用绝对路径运行 doctor；
3. BLOCKED 时不启动审查，只汇报一个修复动作；
4. READY 时调用对应 Provider；
5. 用中文汇报三态结果、未提交改动状态和保留证据路径；
6. 不修改代码、提交、远端、CI 或部署状态。

README 同时保留 Codex 与 Claude Code 的人工安装命令、升级/重载说明、支持平台和 committed-only 限制。顶层 `quality-review --help` 与子命令 `-h` 必须成功退出。

## 5. 验收

### 机械验收

- doctor READY、未登录、缺少能力、dirty worktree、空提交差异和无 `origin/HEAD` 契约测试；
- doctor 测试证明不会调用 `codex exec`、`claude -p` 或任何模型入口；
- bootstrap 的双宿主 argv、固定 Tag、HTTPS Claude source、升级和绝对路径输出测试；
- README、Skill、marketplace、plugin、Makefile 和 Release 资产一致性测试；
- `make release-check VERSION=v0.5.0 VERIFY_COMPARE_REF=v0.4.2`；
- 四平台构建与 checksum 校验。

### 公开验收

- 原子推送 `main` 与 annotated `v0.5.0`，创建非 draft、非 prerelease GitHub Release；
- 下载全部公开资产，与本地 `dist/` 逐字节一致并验证 checksums；
- 在隔离配置中分别运行公开 bootstrap，确认 Codex 与 Claude Code plugin 均为 `0.5.0`；
- 公开安装后的 CLI `version`、`--help` 和两端 doctor 均符合契约；
- 至少完成一次 Codex 与一次 Claude Code 的公开 v0.5.0 真实仓库审查，证明两个 Provider 都能产出冻结证据且不改变被审查仓库。

## 6. 非目标

- 不改变 native review 方法、模型默认值、三态分类或 report-only 语义；
- 不审查未提交工作树内容；
- 不自动修改 shell profile、提交代码、推送业务仓库或改变 CI；
- 不引入第三个 Provider、重试、交叉验证或多 Agent 编排。
