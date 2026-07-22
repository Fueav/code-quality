# Code Quality V1 完整资格验证执行计划

日期：2026-07-22
状态：执行中
冻结候选提交：由初始化器从干净工作树写入 `baseline.json`，不得手工指定

## 1. 目标

完成 V1 report-only 发布候选的宿主会话资格验证，生成可复现、经人工确认且 `qualification_complete=true` 的证据包，使其具备进入真实项目 report-only 试点的资格。

本计划不启用自动阻断，不扩大 20 条底线规则，不建设模型 Runner 或人工放行平台。

## 2. 当前基线

- Go 测试和 `go vet ./...` 通过。
- 确定性矩阵覆盖 20 条规则、60 个案例，结果为 60/60 通过。
- 60 个真实 Git fixture 已通过结构、语言文件、JSON、Go 编译和全量 Git 物化检查；语义边界仍待独立人工批准。
- 现有 replay 记录为 3 条，均来自 Claude Code，人工状态均为 `pending`；其路径泄露案例类型，只作为 canary，不计入最终盲测资格证据。
- 当前 replay 汇总为 `qualification_complete=false`。
- 远端没有 release tag；当前产物仍是发布候选。

## 3. 资格验证合同

资格矩阵固定为：

| 案例类型 | 每条规则案例数 | 每案例运行数 | 总运行数 |
|---|---:|---:|---:|
| 严重正例 | 1 | 3 | 60 |
| 不得阻断反例 | 1 | 1 | 20 |
| 证据不足案例 | 1 | 1 | 20 |
| **合计** | **3** |  | **100** |

每条运行必须来自真实 base/target Git commit，经过 `prepare → main review → optional verifier → finalize → replay record`，并保存最终 JSON、确定性 Markdown、会话输入、模型输出和 replay record。

正式运行必须是盲测：执行 Agent 不能读取 `evals/cases.json`、预期结果、rule ID、案例类型或私有 run 映射。仓库目录、任务名称和会话提示只使用随机 run ID；只有编排器和事后人工复核人可以读取 run ID 到 case ID 的映射。已接触预期结果的会话不得生成正式资格记录。

`qualification_complete=true` 需要同时满足：

- 60 个案例全部覆盖；
- 20 个正例各有 3 次稳定运行；
- 结果、规则 ID、S/T/E 与案例预期一致；
- 没有重复根因或无效记录；
- 每次最多 2 个 Agent、最多 1 个 verifier；
- Claude Code 与 Codex 都有有效记录；
- 每次运行均由独立人工确认。

token 和耗时当前不是汇总布尔值的硬条件，但必须采集，供真实项目试点判断成本和时延。

## 4. 执行阶段

### Q0：冻结与预检

- 固定 Policy、Schema、Skill、CLI 和案例矩阵对应的提交。
- 运行 Go 测试、vet、确定性 eval 和现有证据验证。
- 盘点真实 fixture、宿主工具和现有 replay 记录。

退出条件：机械门禁通过，缺口可量化。

### Q1：补齐真实 Git fixture

按 D1、D2、D3、D4 四批补齐 60 个 fixture。每个 fixture 必须把决定预期结果的事实放进可审查的代码、配置、Schema、迁移或契约中，不得只在说明文字里宣称结论。

- 正例必须闭合真实入口、触发条件、规模或确定输入、因果链和严重后果。
- 反例必须包含可直接证明不得阻断的硬上限、保护或兼容机制。
- 证据不足案例必须保留具体风险路径，但有意不提供将 T2/E1 提升到 T3/E2 的生产事实。

每批先通过 fixture 完整性检查，再由人工抽查边界，之后才允许进入模型回放。

退出条件：60/60 fixture 完整，文件结构可物化为 base/target commit，预期边界经人工批准。

### Q2：宿主会话回放

先用 `DES-003` 完成端到端 canary，再以全新随机 run ID 和隔离会话按 D1 → D2 → D3 → D4 批量运行。canary 不计入正式 100 次运行。

正例三次运行至少包含一次 Claude Code 和一次 Codex；第三次在两种宿主间交错分配。反例和证据不足案例在两种宿主间交错分配，保持总工作量接近平衡。

每完成一条规则就立即运行 replay summarize；不把无效输出留到批次末尾集中处理。

退出条件：100 个有效记录，60 个案例全覆盖，宿主和 Agent 限制满足合同。

### Q3：独立人工复核

人工复核人检查真实入口、变更归因、触发事实、完整因果链、规则归属、S/T/E、根因去重和 verifier 结论。

- 正确：标记 `confirmed`。
- 错误：标记 `overturned` 并记录具体原因；修复 fixture、Policy 或流程后重新运行，不篡改旧记录。
- 无法判断：保持 `pending`，不得计入完成。

执行 Agent 不得自行把自己的运行标记为人工确认。

退出条件：所有计入资格验证的运行均经独立人工确认。

### Q4：最终证据包与试点准入

- 重新校验每份最终 JSON，并确定性重渲染 Markdown。
- 汇总 replay，要求 `qualification_complete=true`。
- 保存冻结提交、工具版本、fixture 清单、结果路径、人工结论和运行指标。
- 固定 RC 版本并生成真实项目 report-only 接入清单。

退出条件：证据包可从冻结提交复现，试点准入评审通过，所有规则仍为 `report_only`。

## 5. 证据目录合同

资格验证证据保存在未跟踪目录 `.code-quality/qualification-v1/`：

```text
.code-quality/qualification-v1/
├── baseline.json
├── quality-review
├── fixtures/<case-id>/
├── repositories/<case-id>/
├── sessions/<host>/<case-id>/<run-number>/
├── results/<host>/<case-id>/<run-number>/
├── replay-records/
├── human-reviews/
├── qualification-summary.json
└── evidence-summary.json
```

仓库只提交 fixture、验证脚本和不含运行证据的合同；真实会话和审查结果保持未跟踪，避免把被审查代码或模型输出误当作产品源码发布。

## 6. 进入真实项目试点前的最终检查

- `qualification_complete=true`；
- RC 有固定版本且安装方式可复现；
- 三个试点项目和维护联系人已确定；
- report-only CI 只发布报告，不把语义 `BLOCK` 映射成失败；
- 已准备至少 30 个历史改动，并定义人工标注责任人；
- token、费用、失败率和时延有统一采集口径；
- 没有启用自动阻断。
