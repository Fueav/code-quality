# merge 折叠 v3:模型提案 + 引擎校验(封顶迭代,交 Codex)

日期:2026-07-28。前置:v2 重分析 FAIL(`v2-remerge.md`),确立两个事实:(1) 同根因跨采样会被标不同 rule_id/维度,维度不可作折叠守卫;(2) 行距既漏(同根因隔 57 行)又误(不同根因隔 10 行)。机械键达到误差下限。本版把语义判断交还模型、引擎只做结构校验与溯源——与本项目"模型收集事实、程序校验字段"的既有架构原则一致。**预登记承诺:本版若 FAIL,采样线封存留分支,主线转向,不再迭代。**

## 一、两阶段 merge(chris/merge-dedup-v2 分支上迭代)

1. `merge --sessions ... --propose --output <dir>`:校验逻辑同 v1(COMPLETE、同 base/target/版本),导出 `merge-candidates.json`——全部 finding 编号列出(id、rule_id、code_locations、production_impact、minimal_fix、来源 session 序号),不做任何折叠。
2. 模型(编排会话)写 `fold-proposal.json`:分组数组,每组含成员编号列表 + 一行 `reason`(为何同根因);单元素组合法(不折)。
3. `merge --sessions ... --apply --proposal fold-proposal.json --output <dir>`:
   - 结构校验:每条 finding 恰好出现在一组;组员非空;**跨文件禁折**(组内任意两成员的位置集必须共享至少一个文件,否则整体拒绝);提案含未知编号则拒绝。
   - 语义不校验(那是模型的职责),但全量溯源:代表项选取沿用 v2 确定性规则,`folded_variants` 记录全部被折成员,新增 `fold_reason` 原样保留。
   - 确定性:给定同一提案与同批 session,乱序输入逐字节同输出。提案文件本身归档进 evidence。
4. 测试(先 RED):候选导出完整性、每-finding-恰好一组校验、跨文件拒绝、未知编号拒绝、单元素组、确定性、v2 既有测试不回归(v2 机械折叠路径保留为 `--auto` 兼容或直接移除,二选一并在报告说明)。

## 二、重分析(同 24 session,零 lane 成本)

- 每目标一次 `codex exec -s read-only --output-schema`(隔离 CODEX_HOME 同前),输入仅 `merge-candidates.json`,产出 `fold-proposal.json`;随后 `--apply`。
- 预登记门槛(与前两轮完全相同,不挪门柱):已知命中 4/4 保留;噪音 gate ≥6/8;稳定占比 ≥40%。
- 预登记对照点(在门槛之外必须逐一核对并报告):
  1. 负对照:`781ee3be` 的 `chat_behavior.py:200`(secret 请求误拒)与 `:210`(交易教程误拒)**不得**被折;
  2. 正对照:`b21dbb39` 的 `identity.py:51/52/109` header-body wallet mismatch 应折为一组;
  3. 正对照:`da79c064` 的 `handlers_chat.go:249` COR/CHG 同行对应折为一组。
  对照点失败即使门槛通过也判 FAIL(说明模型提案不可靠)。
- 提案调用的 token 成本单独实测记录。

## 三、出口(二选一,无第三条路)

- **PASS**:merge v3 获得合入 main 资格;产品决策单(`--samples 3` 可选模式)更新后提交用户拍板。
- **FAIL**:采样线封存(分支保留、报告归档),在 `v3-remerge.md` 写明封存理由与重启条件;主线转向"缺失型缺陷机制 + 路线 A 真实数据",不再消耗预算。

## 产出

原实验目录追加 `v3-remerge.md` + `evidence-v3/`(8 份提案 JSON + 8 份合并结果)。不改既有归档,不 push、不 tag。
