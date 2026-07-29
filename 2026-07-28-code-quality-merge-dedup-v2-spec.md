# merge 去重 v2 + 零成本重分析规格(交 Codex)

日期:2026-07-28。前置:多采样实验 FAIL(`code-quality-multisample-experiment/reports/2026-07-28-multisample-experiment/results.md`),败因解剖:gate 3 的"13.2% 稳定"被 `rule_id + 精确行号` 去重键低估——同一语义根因因行号抖动(b21dbb39 的 51/52/109 三连)或 rule_id 标注差异(da79c064 的 COR/CHG 之别)被拆成多条单样本 finding。本规格修合并键并在**已保留的 24 个 session 上重新 merge**,零 lane 运行成本。

## 一、`merge` 去重 v2(feat/multisample 分支上迭代)

1. **容差折叠键**:两条 finding 判为同一根因,当且仅当存在文件相同且行距 ≤10 的 code_location 配对覆盖双方位置集的多数,且二者 dimension 相同(rule_id 不同但同维度不阻止折叠;跨维度不折叠)。折叠为传递闭包(A~B、B~C → 同组),组代表的选取确定性:样本数最多 → 位置集最小行号最小 → 内容哈希。
2. **折叠组输出**:代表 finding 保留;`found_in_samples` 取组内并集;新增 `folded_variants`(被折叠条目的 id、rule_id、位置,保完整溯源);`rule_id` 取组内多数,平票取代表的。
3. **分层渲染**:review-result.md 按 `found_in_samples` 数降序分两层——"多样本一致"在前,"单样本"在后并明确标注置信级别;JSON 结构不分层,靠 `found_in_samples` 字段表达。
4. 确定性保持:乱序输入逐字节同输出。测试(先 RED):行号抖动折叠(±10 内/外)、跨 rule_id 同维度折叠、跨维度不折叠、传递闭包、代表选取确定性、`folded_variants` 完整性、原有全部 merge 测试不回归。

## 二、重分析(只用已保留 session,不跑任何新 lane)

对 `.experiment/multisample-runs/` 里 24 个原 session 按目标重新 merge(v2 引擎),重算并**预登记**以下判定(在看到数字前固定,防事后挪门柱):

1. 4 条已知缺陷命中在合并输出中全部保留(任意层);
2. 折叠后噪音 gate(合并 finding 数 ≤ 2×单轮中位数)通过目标数 ≥ 6/8;
3. 折叠后 `found_in_samples≥2` 占比 ≥ 40%(对照修复前 13.2%);
4. 附加报告项(不设门槛,如实记录):折叠组数与最大组、每目标折叠前后条数、"多样本一致"层是否包含全部 4 条已知命中(预期 781ee3be、b21dbb39 两条救回命中是单样本层——如实呈现,这是分层设计保留它们而非过滤它们的理由)。

全过 → merge v2 进 main 的资格成立,同时把裁决材料(worst-run 2 → merged 4 的下限口径 vs 3× token 成本,成本实测已在原报告)整理成"是否把 `--samples 3` 作为可选模式"的产品决策单,交用户拍板;未全过 → 失败面报告,机制留分支。

## 产出

原实验目录追加 `v2-remerge.md`(折叠前后对照、四项判定、决策单)与 `evidence-v2/`(8 份重合并 review-result.json)。不改既有归档,不 push、不 tag。
