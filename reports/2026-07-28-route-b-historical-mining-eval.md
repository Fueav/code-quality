# 路线 B 历史缺陷挖掘与双 lane 对照评测(2026-07-28)

## 数据集构建

- 来源:`agent_marketplace`(151 提交)与 `general-agent-ai`(88 提交)真实 git 历史。
- 流水线:机械预筛(修复类提交且触及源码,47/52)→ Codex CLI 逐个溯源(`codex exec -s read-only --output-schema`,47/47 零失败)→ 终筛(material + static_detectable + 引入提交可定位且非巨型基线提交)。
- 终筛淘汰:9 范围外、9 静态不可发现、12 引入提交为 6.5 万行仓库基线(历史被压缩,无法作为变更审查样本)。
- 产出:**13 个评估目标(引入提交)、16 条已知缺陷**(难度 1:2 条,难度 2:6 条,难度 3:4 条,难度 4:4 条)。
- 评估隔离:每目标 shared clone 检出到引入提交、剥除全部 refs 与 remote,修复提交对模型不可见。

## 双 lane 设置

- product:quality-review main@0b2b565(第一类四项改动后),`codex exec -s workspace-write` 走完整 prepare→审查→finalize(含 rereview_scope 复审)。
- builtin:同一隔离克隆,`codex exec -s read-only` + 中性审查 prompt(无 lens、无流程)。
- 同一宿主模型;裁决人:Claude(逐条对照挖掘阶段冻结的缺陷描述)。

## 逐条裁决

| 目标 | 已知缺陷 | 难度 | product | builtin |
|---|---|---|---|---|
| 01cdc343 | SEC-001 公网 header 信任绕过 JWT | 1 | ✅ | ✅ |
| efb374a4 | SEC-001 docs/openapi 匿名暴露 | 1 | ✅(精确) | ✅(宽泛覆盖,未点名 docs) |
| b7f64515 | SEC-003 脱敏黑名单逸出 | 2 | ✅ | ✅ |
| b7f64515 | REL-005 状态查询只看退出码假成功 | 2 | ❌ | ❌ |
| d2040212 | SEC-003 Authorization trailer 泄漏 | 2 | ❌ | ❌ |
| da79c064 | CHG-001 身份前缀不稳定 | 2 | ✅ | ✅ |
| d28dd36f | COR-001 chat 身份前缀错误 | 2 | ❌ | ❌ |
| d28dd36f | CHG-001 Bearer 大小写兼容性回归 | 2 | ✅ | ✅ |
| 2e87ffc1 | COR-001 compute queries 无结构 schema | 3 | ❌ | ✅ |
| 781ee3be | COR-001 流式输出整段缓冲,TTFT 退化 | 3 | ❌ | ✅ |
| b21dbb39 | SEC-001 legacy 身份模式默认可冒充 | 3 | ❌ | ✅(经 compose 未传变量角度) |
| fd679bef | SEC-001 身份头随缺省 URL 发到公开域名 | 3 | ✅ | ❌ |
| e23f25e1 | REL-005 部署无闭环验证假成功 | 4 | ❌ | ❌ |
| 8f85af1e | COR-001 私网 listener 分离部署不可达 | 4 | ❌ | ❌ |
| d28dd36f | CHG-001 反向调用无内部路由被 401 | 4 | ❌ | ❌ |
| 6349a866 | COR-001 身份未传递到 Agent 工具 | 4 | ❌(两轮后 PASS) | ❌ |

## 汇总

| 指标 | product | builtin |
|---|---:|---:|
| 已知缺陷命中(全部 16) | 6 | 9 |
| 难度 ≤2(8 条) | 5 | 5 |
| 难度 3(4 条) | **1** | **3** |
| 难度 4(4 条) | 0 | 0 |

## 结论

1. **数据集有区分度**(此前 7-24 六样本两边均 100%,无法区分):难度 3 层首次拉开差距,且 4 个错位样本方向 3:1 **不利于 product**。
2. **难度 3 的三次失守共性**:2e87ffc1 与 781ee3be 均为"契约/规范符合性"缺陷,builtin 自由阅读仓库 spec 后命中,product 被 20 条 lens 引导到规则形状的发现(缓冲上限、分类器误报)而错过头条问题。lens 疑似产生注意力挤出:引导了搜索方向,也挤掉了"对照 spec 通读"的行为。此假设是第二类调优解冻后的首要验证项。
3. **难度 4(缺失型缺陷:该做的保障没做)双方全灭**:部署后不验证、身份不传递、内部路由缺失。这是两条 lane 共同的能力边界,也是"提下限"工具最该建立优势的层。
4. 第一类改动在真实链路得到验证:workspace-write 下 `checkout_mode=clone` 退回生效(旧版 6/6 全挂场景跑通);复审状态机每次运行都触发(retry_count=1);去上限后 product 单目标最多报 5 条独立发现,广度大于 builtin 且未见明显噪音——但广度尚未转化为已知缺陷命中率。
5. 局限:单次运行无方差数据;裁决人为单一 Claude 会话;builtin 的 efb374a4、b21dbb39 记命中采用了"宽泛覆盖同根因"的从宽口径。

## 工件

- 目标清单与裁决明细:`evidence/targets.json`、`evidence/adjudication.json`(本目录)
- 挖掘与评估原始输出:会话 scratchpad `mining/`、`eval/`(临时目录,如需归档请另行拷贝)
