# code-quality v0.5.7 受限裁决与两轮收敛规格

## 目标

保留原生 Codex Exec / Claude Code 审查作为发现层，但不再让原生优先级直接决定发布门禁。原生结果冻结后，仅把 P0/P1 候选交给受限生产下限裁决；未达到下限的候选不得进入面向人的结果。自动修复复查最多两轮，第二轮之后必须转人工。

## 单轮流水线

1. `plan` 冻结 committed Git 范围、Provider 合同和 review identity。
2. 原生 Provider 在隔离 checkout 中发现问题；原始 final message、stdout、stderr 和指标先冻结。
3. 若原生结果没有 P0/P1，直接发布；P2/P3 保持 advisory，不触发第二次调用。
4. 若存在 P0/P1，同一个 Provider、model 和 reasoning effort 在只读、无宿主自定义扩展的环境中执行一次受限裁决。受信 policy 作为 Codex developer instructions 或 Claude system prompt 注入，输入只包含冻结的 P0/P1 ID 和内容。
5. 普通代码严格校验裁决结构、ID、顺序和仓库内证据，并重新计算是否保留。模型的 `recommended_disposition` 不参与最终决定。
6. 未保留候选只在原生冻结证据中存在；`review-result.json`、Markdown 和 summary 不包含候选标题、理由或建议。裁决调用或协议失败时发布 `ERROR/HOLD`，同样不复制候选正文。

## 阻断下限

裁决语义的唯一真相源是 `policy/v1.2/restricted-adjudication.md`；结构真相源是 `schemas/restricted-adjudication-output.schema.json`；普通代码只机械实现并校验二者规定的 conjunction。其他文档不得另行定义或放宽生产下限。

## 两轮收敛

- 自动第 1 轮是 `FULL`。
- 自动第 2 轮只能是以该 FULL 结果为 parent 的一次 `INCREMENTAL`。
- 再以 INCREMENTAL 结果请求 INCREMENTAL 时，`plan`、`doctor` 和 `run-*` 必须在创建 session 或调用 Provider 前返回 `MANUAL_REQUIRED`、`provider_invocations=0` 和退出码 5。
- `MANUAL_REQUIRED` 不得回退成自动 FULL；外围必须把剩余真实 blocker 交给人处理。人工明确发起的新 FULL 不属于同一自动链。
- `FULL_REQUIRED` 仍只表示 lineage、范围或合同不再兼容，退出码为 4；它与两轮上限无关。

## 版本化合同

- `quality-review v0.5.7`
- native result schema v9：允许每轮 1 或 2 次 Provider invocation，增加 `DISMISSED` resolution 和无候选正文的 adapter drop。
- prompt contract v3：绑定受限裁决 policy、结构化 schema 和强制只读调用方式。
- v8 及更早 schema 保持不可变；新增外部 envelope-v2 引用 v9，envelope-v1 继续固定引用 v8。

## 验收

- 无 P0/P1 时仅调用一次 Provider。
- 不可达或证据不足的 P0/P1 经过第二次调用后为 PASS，且四个人类/机器发布面均不出现候选正文。
- 满足全部下限的候选保持 BLOCK。
- 裁决失败为 ERROR/HOLD，不回退到原生优先级。
- 第三次自动审查不调用 Provider、不创建 evidence session，并返回 MANUAL_REQUIRED。
- Codex 与 Claude 的裁决调用都强制只读并注入同一 policy。
