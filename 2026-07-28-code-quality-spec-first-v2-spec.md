# spec-first v2 规格:契约对照移入复审轮(交 Codex)

日期:2026-07-28。前置:v1(契约通读置于第一轮 lens 前)判 FAIL——契约类样本受益(781ee3be 命中)但非契约类受损(fd679bef 1/3),全局改动产生注意力挤出;方差测量确认单次运行不可作 gate 依据(d28dd36f Bearer 在 main 也仅 1/3)。

## 前置动作(需用户已批准)

`chris/review-invalid` 的 REVIEW_INVALID 引擎修复先合入 main(与本实验无关、测试全绿、能消除无效运行浪费);v2 分支 `exp/spec-first-v2` 从合入后的 main 拉出。

## 改动(唯一变量)

**第一轮 workflow 步骤完全不动**(恢复 main 版本)。只改复审轮指令(`policy/v1.2/workflow.md` 中 REREVIEW_REQUIRED 分支,及 SKILL.md 对应句;净行数不增):

> 复审轮做两件事:(1) 对 `rereview_scope` 内维度反证式检查;(2) 定位与 `changed_files` 相关的已声明契约(spec 文档、OpenAPI/接口定义、协议与配置约定、README 行为承诺),逐一对照本次变更,契约违反即候选发现(通常 COR-001/CHG-001),不受 `rereview_scope` 维度限制。只报告新发现,空为合法。

## 实验协议(×3 起步,多数决)

- 目标 7 个:错位组 `2e87ffc1`、`781ee3be`、`b21dbb39`、`fd679bef` + 对照 `da79c064`、`b7f64515` + 双漏组代表 `6349a866`(缺失型,看复审契约对照是否意外覆盖)。
- 运行:v2 分支每目标 ×3;`fd679bef` 补 main 基线 ×3(它已成争议目标,需自己的基线方差)。其余基线沿用已有 main 数据(d28dd36f 矩阵、原始单次)。
- 隔离克隆、lane prompt、禁用宿主 memory 机制沿用 followup 规格 B;REVIEW_INVALID 生效后无效运行应自动修复,若仍产生无效运行则该轮作废重跑并记录。
- 裁决:对冻结缺陷按目标给 3 轮 hit 计数;**多数决(≥2/3)为该目标结论**。

## 通过标准(全部以多数决计)

1. 错位组 `2e87ffc1`、`781ee3be`、`b21dbb39` 多数决命中 ≥2;
2. `fd679bef` v2 多数决 ≥ main 基线多数决;`da79c064`、`b7f64515/SEC-003` 多数决不丢失;
3. 单目标发现数中位数不超过 main 对应值 +2,新增发现满足五条件门槛。

三项全过 → v2 进 main(policy 升 1.2.1);过 1-2 项 → 报告细化失败面,不进 main;全败 → 放弃"契约对照"方向,记录证据。

## 产出

`reports/2026-07-28-spec-first-experiment/` 追加 `v2-results.md`(hit 矩阵、多数决表、gates 判定、workflow diff)与 `evidence-v2/`(全部 review-result.json)。不改既有归档。
