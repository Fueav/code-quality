# 数据集扩充规格:挖掘流水线产品化 + 多仓库扩挖(交 Codex)

日期:2026-07-28。背景:采样线按预登记封存(v3 差 1 条 finding,n=28 上 1 条=3.6pp——样本量不足使门槛判定近似掷硬币);难度 4 缺失型样本仅 4 条。**任何后续机制实验(缺失型机制、采样线重启)都需要更大的冻结数据集先行。** 本规格两件事:把今天验证过的挖掘流水线从临时脚本变成仓库工具,再用它扩挖。

## 一、流水线产品化(`pilot/mining/`)

今天的流水线(见 `reports/2026-07-28-route-b-historical-mining-eval.md` §数据集构建)以临时脚本形态运行,已丢失。按同一配方重建为仓库内工具:

1. `prefilter.sh <repo_path>`:列出修复类提交(`-i --grep 'fix|hotfix|revert|bug'`)中触及真实源码的(排除纯 docs/CI/测试/配置模板;源码后缀白名单),输出 `KEEP <sha> :: <subject>` 清单。
2. `trace.sh <repo_path> <fix_sha>`:对单个修复提交跑 `codex exec -s read-only --output-schema mining-result.schema.json --ephemeral`(隔离 CODEX_HOME 配方同前),prompt 要求:理解修复 → git 溯源引入提交(给证据链)→ 按 20 条 lens 归类或 OUT_OF_SCOPE → 判 material / static_detectable / difficulty 1-5。schema 在今天报告的流程描述基础上**新增一个字段**:`defect_class ∈ {wrong_code, missing_safeguard}`(写错的代码 vs 该做的保障没做)——这是缺失型机制线的核心标签。
3. `aggregate.py`:汇总结果 → 过滤(material + static_detectable + 引入提交可定位且改动 ≤3000 行)→ 按引入提交去重 → 输出冻结数据集条目。
4. 批量驱动 `mine_repo.sh <repo_path>`:prefilter → 并发 ≤4 跑 trace → aggregate。单元测试覆盖 prefilter 白名单、aggregate 过滤与去重;trace 的 prompt 与 schema 作为文件納入版本控制。

## 二、扩挖(候选仓库自 `/Users/chris/AiProject/` 同级)

1. 机械筛选候选:枚举同级 git 仓库,保留"总提交 ≥30 且修复类提交 ≥5"者,排除已挖的 agent_marketplace、general-agent-ai 与 code-quality 自身及各 `*-worktrees` 目录;按修复提交数降序取**前 5 个**跑 `mine_repo.sh`。
2. 汇总产出 `pilot/dataset/v2/`:
   - `targets.json`:新目标 + 既有 13 目标(从 `reports/2026-07-28-route-b-historical-mining-eval/evidence/targets.json` 迁移,补 `defect_class` 标签——按缺陷描述判定并给一行依据);
   - `README.md`:构建方法、口径、逐仓库统计(候选数/淘汰原因分布/产出数)。
3. **规模目标(不达标如实报告,不硬凑)**:总目标 ≥30、其中 `missing_safeguard` ≥10、difficulty ≥3 的 ≥15。达标后此数据集冻结为 v2,后续机制实验的功效声明必须引用它的 n。

## 边界

- 全程只读挖掘,不跑任何 product/builtin lane(评测是后续机制实验的事);
- 不 push、不 tag;`pilot/mining/` 与 `pilot/dataset/` 在新分支交付;
- 挖掘成本实测记录(逐仓库 token/时长),供决定是否继续扩到 5 个以外的仓库。
