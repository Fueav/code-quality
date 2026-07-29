# 多采样合并机制规格(主线,交 Codex 实施与验证)

日期:2026-07-28。依据:方差矩阵实测(`code-quality-spec-first-experiment/reports/.../results.md` §复核与方差)——fd679bef 与 d28dd36f/Bearer 单轮命中率均为 1/3,三轮并集均命中;单轮 0/3 的缺陷并集仍为 0。结论:单次审查在难样本上本质是掷硬币,多独立采样 + 合并把 p≈1/3 变为 1-(1-p)³≈70%,是"提下限"的机制级实现。0.2.0 报告 §1 已推荐此架构("多独立 session + 显式合并裁决"),本规格将其落地为引擎能力并用回归集验证。

**注:`2026-07-28-code-quality-spec-first-v2-spec.md` 搁置不跑**(措辞级改动让位机制级改动;其"契约对照入复审轮"问题留待多采样数据出来后再议)。

## 前置(把本规格交给 Codex 即视为用户批准)

`chris/review-invalid` 分支(REVIEW_INVALID 引擎修复,测试全绿)合入 main;实验分支 `feat/multisample` 从合入后的 main 拉出。

## 一、引擎改动:`quality-review merge`

新增子命令:

```
quality-review merge --sessions <dir1>,<dir2>,... --output <dir>
```

1. 校验:每个 session 均为终局 COMPLETE、`review-result.json` 通过 validate、base/target/policy_version/skill_version 完全一致;任一不满足则整体拒绝并列明原因。不满足独立性的证明责任在调用方:merge 只记录事实,不宣称独立采样(呼应 0.2.0 §1 的降级原则)。
2. 合并:复用 `internal/session` 现有 `mergeFindings` 语义(rule_id + 排序后代码位置去重、ID 冲突确定性重命名),扩展到 N 组;`activated_rule_families`、`missing_context` 等按现有 mergeReviews 规则并集。
3. 溯源:输出新增 `sampling` 块——`sample_count`、每 session 目录名与发现数、每条合并后 finding 的 `found_in_samples`(命中它的样本序号列表)。`found_in_samples` 长度即该发现的稳定度,消费方可据此排序。
4. 产出:`--output` 下写合并版 `review-result.json` + `review-result.md`,经正常 Adjudicate;确定性:同输入任意顺序 → 逐字节相同输出(内部按 session 内容哈希排序)。
5. 测试(先 RED):跨样本去重、ID 冲突重命名、`found_in_samples` 正确性、base/target 不一致拒绝、非 COMPLETE 拒绝、确定性(乱序输入同输出)。`gofmt`、`go test ./...` 全绿。

## 二、验证实验(×3 采样 vs 单轮,main workflow 不变)

- 回归集 8 目标 / 11 条冻结缺陷:`2e87ffc1`、`781ee3be`、`b21dbb39`、`fd679bef`、`da79c064`、`b7f64515`(2 缺陷)、`6349a866`、`d28dd36f`(3 缺陷)。SHA 与缺陷描述:`reports/2026-07-28-route-b-historical-mining-eval/evidence/targets.json`。
- 每目标 3 个**独立新会话**(逐个全新 `codex exec`,隔离克隆、隔离 `CODEX_HOME`、`--ignore-user-config --ephemeral`,配方沿用 followup 规格 B),各自完整 prepare→审查→finalize,然后 `merge` 三个 session。
- 裁决(Codex 自判,一行依据):产出**每缺陷 × 每轮 hit/miss 矩阵** + 合并结果 hit/miss。已有的 d28dd36f main×3 矩阵可直接并入,对应目标只需补跑差额轮次。

## 通过标准

1. **稳定命中零丢失**:`da79c064/CHG-001`、`b7f64515/SEC-003` 在合并结果中命中;
2. **并集提升**:合并结果命中数 ≥ 本实验单轮最高命中数 + 2(用本实验矩阵内部对照,不依赖历史幸运轮);
3. **噪音可控**:合并后单目标发现数 ≤ 单轮中位数的 2 倍,且每条合并 finding 仍满足五条件门槛(逐条自查,超限逐条说明);`found_in_samples≥2` 的发现占比一并报告。

三项全过 → merge 进 main + 写产品接入规格的调查输入(真实宿主如何获得 N 个独立会话:多次人工触发 / 宿主 subagent / 外部编排,交由下一份规格决策);未全过 → 报告失败面,机制留分支。

## 产出

`reports/2026-07-28-multisample-experiment/`:`results.md`(矩阵、合并对照、gates 判定、成本实测 token/时长)+ `evidence/`(24 份单轮 + 8 份合并 review-result.json)。不改既有归档;不 push、不 tag。
