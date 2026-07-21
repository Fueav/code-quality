# 代码质量检测模块 V1 实现设计

日期：2026-07-21  
状态：待评审

## 1. 目标

V1.1 Rubric 已确定检测范围、四个维度、20 条底线规则、S/T/E 判定、阻断公式、报告合同和 Agent 上限。本文不重新定义这些内容，只规定如何把它们实现为可运行、可验证、可独立接入的模块。

> 在任意 Git 仓库中零配置检查一个可信代码增量，发现本次改动引入或加重的生产底线缺陷，并由确定性程序计算最终结果。

V1 只提升代码质量下限，不评价命名、格式、一般代码风格、抽象是否优雅或是否存在更漂亮的写法。

## 2. 权威边界

- V1.1 Rubric Markdown：检测范围、规则语义、S/T/E、阻断公式和报告字段。
- V1.1 优化设计：兼容性规则族、基线合同、Agent 资源边界和验证矩阵。
- 本文：仓库形态、运行流程、组件边界、零配置接入、安全隔离和实施顺序。
- 首版将批准的 Rubric 无语义变化地导入 `policy/v1.1/rubric.md`；只允许中性化产品命名。导入后按版本冻结。
- `policy/manifest.json` 只保存规则 ID、维度、状态、版本和机械限制，不复制规则语义。

## 3. 产品形态

V1 使用一个独立仓库，暂称 `code-quality`，发布一个静态 Go CLI：`quality-review`。一个发行版本同时固定 Policy、Schema、Runner、Validator、Adjudicator、Renderer 和 Eval cases。

不拆分多个版本线，不并入 `harnessctl`、Harness Driven Development 或 Harness Template Sync。

普通使用者在 Git 仓库中直接运行：

```bash
quality-review
```

高级调用可显式指定：

```bash
quality-review run --base <commit> --target <commit> \
  --diff-reason <reason> --output <directory>
```

首次运行不要求项目配置、项目质量上下文、Evidence Pack、`AGENTS.md`、Template、Harness Skill 或 `harnessctl`。

默认输出：

```text
.code-quality/review-result.json
.code-quality/review-result.md
```

JSON 是唯一权威结果，Markdown 由程序确定性生成。

## 4. 总体架构

```text
Git repository / CI environment
  → Intake：可信 base、target、diff reason、changed files
  → Context Discovery：相关入口、调用链、测试、配置、契约、证据
  → Review Runner：主 Agent；必要时一个批量验证 Agent
  → Model Review JSON
  → Validator + Adjudicator：确定性校验与裁决
  → Report Renderer
```

建议结构：

```text
code-quality/
├── cmd/quality-review/
├── internal/{intake,discovery,review,validation,adjudication,report}/
├── policy/v1.1/
├── prompts/
├── schemas/
├── evals/
├── plugins/code-quality/       # 可选薄 Skill
└── docs/
```

## 5. Intake：可信增量识别

基线识别优先级：

1. 显式 `--base`、`--target` 和 `--diff-reason`；
2. GitHub Pull Request 或 GitLab Merge Request 的可信环境变量；
3. 本地 `origin/HEAD` 与 `HEAD` 的 merge-base。

V1 只审查已提交的 commit 增量。未提交工作树变更不进入检查，CLI 必须明确提示。无法可靠确定基线时结果为 `INCOMPLETE`；不得静默改用 `HEAD~1`、最近 tag 或更大范围 Diff。

Intake 生成符合 `review-request.schema.json` 的请求，包含 V1.1 已规定的完整基线字段。

## 6. Context Discovery：零配置上下文发现

上下文发现是模块内部阶段，不是接入方交付物。Runner 从 Diff 出发，读取与改动直接相关的：

- 修改的函数、类型、接口和数据结构；
- 真实入口、调用方、被调用方和副作用；
- 错误、重试、并发、事务和资源生命周期路径；
- 相关测试、配置、Schema、Spec、迁移和运行文档；
- base 版本实现和必要 Git 历史；
- 与 target commit 匹配的 CI 或 Harness evidence。

V1 不开发跨语言静态调用图。Go 程序提供 Diff 和仓库种子，主 Agent 使用只读搜索和代码导航扩展上下文。

缺少生产规模、部署配置或业务事实时，检查仍须完成；依赖这些事实的结论不得猜测，只能降低证据/触发确定性或进入 `MANUAL_REVIEW`。

## 7. Harness 与普通仓库

Harness 是可选证据来源，不是运行依赖。普通仓库使用 Git、代码、现有测试和文档；Harness 仓库若存在与 target commit 匹配且 digest 有效的 evidence，Runner 自动读取。过期、损坏或不匹配的 evidence 不参与裁决，并在报告中说明。

两类仓库使用完全相同的 Policy、Runner、Schema、Adjudicator 和 Verdict。`harnessctl` 不调用模型，两个 Harness Skill 不拥有质量规则，Template Sync 不成为运行时依赖。

## 8. 模型执行与 Agent 控制

V1 只支持一个固定版本的 Codex CLI 后端，不提前建设多供应商抽象。

主 Agent 负责确认行为变化、找到真实入口、追踪调用链与数据流、激活相关规则、形成候选并按根因去重。

只有存在潜在 `BLOCK` 候选时，Runner 才启动一个独立验证 Agent，批量反证全部候选：检查不可达路径、硬上限、既有幂等/鉴权/超时保护、历史归因和证据缺口。

V1 最多使用 2 个 Agent，严于 V1.1 的 3 Agent 总上限：

```text
无阻断候选：1 个主 Agent
有阻断候选：1 个主 Agent + 1 个批量验证 Agent
```

合并规则：

```text
refuted      → 不得 BLOCK
insufficient → MANUAL_REVIEW
confirmed    → 交由 Adjudicator 应用完整公式
```

Agent 不得修改代码、执行项目脚本、安装依赖、访问网络、读取仓库外文件、提交/推送或自行启动子代理。报告记录 Agent 数、token、耗时、重试和失败。

## 9. Policy、Prompt 与 Skill

`policy/v1.1/rubric.md` 保存完整规则语义；manifest 只保存 `policy_version`、`rule_id`、`dimension`、`status`、`agent_limit` 和 `schema_version`。

Prompt 只包含本次任务数据、权威指针、只读限制、停止条件和输出 Schema，不复制 20 条规则。可选 Skill 只负责触发和指向 CLI/报告，不拥有 Policy、Agent 工作流或裁决逻辑。CI 直接调用 CLI。

## 10. Schema 与确定性裁决

V1 只需要三份核心 Schema：

1. `review-request.schema.json`：Runner 生成的基线和范围；
2. `model-review.schema.json`：模型候选、激活规则、缺失上下文和执行信息；
3. `review-result.schema.json`：最终权威报告。

模型输出是候选，不是最终 verdict。普通程序必须：

1. 校验基线、字段、枚举、位置、真实入口、触发条件、因果链和 Agent 上限；
2. 验证 `rule_id` 属于当前 Policy；
3. 根据验证 Agent 结果修正候选资格；
4. 应用 V1.1 唯一阻断公式；
5. 按 `INCOMPLETE > BLOCK > MANUAL_REVIEW > PASS` 计算总体结果。

字段缺失、模型超时、Schema 无效、基线不可靠或 Runner 失败必须为 `INCOMPLETE`，不得静默降为 `PASS`。

## 11. 安全与隔离

每次检查使用新的 target commit 临时工作区，默认只读并在完成后清理。模型凭证只提供给模型步骤，不提供给项目代码、测试、依赖安装或构建脚本。

Policy、Prompt 基础约束、Schema、Agent 上限、Adjudicator 和 CI 映射固定在发行版本中，不从当前 PR 读取。PR 描述、代码注释、日志和仓库文档均按不可信数据处理，不能覆盖权限、规则、预算或输出合同。

## 12. Rollout 与 Eval

V1 首发只支持 `report_only` 实际动作。报告计算真实语义结果，但不阻止合并：

```yaml
semantic_result: BLOCK
rollout_mode: report_only
ci_action: publish_report
```

规则初始状态均为 `report_only`。每条规则至少包含严重正例、不得阻断反例和 `MANUAL_REVIEW` 证据不足案例。Eval 记录命中、S/T/E 稳定性、根因去重、人工结论、推翻原因、Agent 数、token 和耗时。

确定性测试覆盖 Schema、阻断公式、降级规则、总体优先级和 Markdown 渲染。完成 V1.1 规定的重复运行和历史回放后，规则才允许进入后续自动阻断版本。V1 不实现人工放行平台。

## 13. 实施顺序

1. **确定性核心**：Policy 导入、三份 Schema、Validator、Adjudicator、Renderer；
2. **零配置 Intake**：显式参数、GitHub、GitLab、本地 Git 和 `INCOMPLETE` 路径；
3. **单 Agent Review**：只读工作区、固定 Codex 调用、结构化候选、预算和超时；
4. **批量验证 Agent**：仅验证潜在阻断候选并执行合并规则；
5. **Eval 与试点**：历史回放、普通仓库/Harness fixture、report-only CI 示例。

每一阶段必须先有确定性测试和 fixture，再进入下一阶段。

## 14. V1 非目标

- Web UI、数据库、常驻服务、远程策略中心或多模型切换；
- 自动修改代码、提交或创建 PR；
- 风格、命名、质量总分或全面覆盖率判断；
- 脏工作树审查或自建跨语言调用图；
- 在模型凭证环境中执行项目代码；
- 强制项目配置、项目上下文或 Harness 接入；
- 首次发布即阻断合并。

## 15. 验收标准

- 任意有可信 commit 基线的 Git 仓库可零配置运行；
- 无 Harness、无项目配置、无 CI evidence 时仍能完成审查；
- Harness evidence 只增强证据，不改变核心路径；
- 同一合法模型报告在不同机器得到相同 verdict 和 Markdown；
- 模型不能直接决定 CI 状态；
- Agent 硬上限为 2，且无候选时只使用 1 个；
- 所有 `BLOCK` 候选满足 V1.1 完整字段、验证和归因要求；
- 执行或输入不完整时明确输出 `INCOMPLETE`；
- V1 只发布 report-only，不包含自动阻断能力。
