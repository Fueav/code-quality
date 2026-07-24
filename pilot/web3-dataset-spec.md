# Web3 历史变更数据集采集规格

用途：为 `historical_pilot`（真实项目 report-only 校验）挑选并标注 3 个真实项目、≥30 个历史变更。目标是判定减重后的 skill 是否**达到内置审查的水平**（`caught_up_to_builtin_review`）。

> 真实的 `base/target SHA + 核心问题 + 历史证据`必须来自你们真实仓库。commit 标题不是 ground truth；severe 标签必须能指向已知核心问题和历史证据（事故 / 修复 PR / issue）。

## 1. manifest 字段（照填，historical pilot 脚手架会校验）

```json
{
  "schema_version": 1,
  "profile": "report_only_historical_pilot",
  "projects": [
    {"id": "settlement", "repository": "/abs/path/to/repo", "maintainer": "owner-name"}
  ],
  "changes": [
    {
      "id": "settlement-001",
      "project_id": "settlement",
      "ground_truth": "severe",
      "base_commit": "<40位 hex>",
      "target_commit": "<40位 hex>",
      "labeler": "maintainer-name",
      "label_note": "充值 nonce 复用导致重复入账；见事故 #1234 与修复 PR #1240"
    }
  ]
}
```

硬约束：
- `projects` 恰好 3 个，`id / repository / maintainer` 均非空。
- `changes` ≥ 30 个；`ground_truth` 为 `severe` 或 `normal`；≥15 severe + ≥15 normal。
- `base_commit` 与 `target_commit` 都是 40 位 hex 且不相等；初始化时还会校验每个 change 是**一个 first-parent commit**。
- 每个 project 至少贡献 1 个 change。
- `label_note` 非空；severe 的 `label_note` 必须点名核心问题 + 历史证据锚点。

## 2. 三个项目选法

1. **资金 / 链上 / 复杂数据类**：结算、清算、跨链桥、钱包 / 托管后端。最能暴露严重缺陷。
2. **普通后端服务（Web2 场景）**：列表查询、分页、搜索过滤、数据统计与聚合报表，不直接碰资金。
3. **历史重、测试弱的老项目**：暴露存量兼容与迁移问题。

## 3. severe 变更去哪找 —— 缺陷类型学（映射 20 条规则）

在候选变更的 diff 里寻找下列模式；命中且有历史证据的，标 `severe`。数据集需**同时覆盖 Web3 特有场景与通用后端（Web2）场景**。

硬要求：选样时先确认标注的核心问题是该 diff 中**最突出、最该被抓的缺陷**；否则两条 lane 都可能优先报告更显眼的问题，虚低核心问题命中率并稀释对照区分度。

### 3a. Web3 特有场景

| 规则 | Web3 典型严重场景 |
|---|---|
| COR-003 数值边界 | wei/gwei/decimals 换算错、18 位小数截断、预言机价格精度、滑点计算、区块号/时间戳边界 |
| COR-005 幂等重复 | 交易重放、事件重复消费致重复入账/提现/mint、nonce 复用 |
| COR-004 事务边界 | 链上转账成功但链下记账失败、充值确认与入账分离后不一致 |
| COR-002 业务不变量 | 资金守恒被破坏、允许负余额、双花、供应量超发 |
| COR-001 明确契约 | 实现与合约 ABI / spec / 公共接口契约相反 |
| DES-004 权威数据源 | 多 RPC/节点读到不一致、reorg 后读旧状态、缓存链上余额过期、拿未确认状态当真相 |
| DES-001/002/003 方向与数据流 | 为每个用户轮询全链、每次从创世块全量重扫、循环里逐个 tx/block 调 RPC |
| DES-005 同步批处理 | 同步请求里等全链扫描或大批回填 |
| SEC-001 认证授权 | 签名/owner/admin 校验绕过、跨用户资金操作、提现权限漏检 |
| SEC-002 不可信输入 | 地址/calldata/签名未校验、重入(reentrancy)路径、SSRF 打内部 RPC、危险反序列化 |
| SEC-003 敏感信息 | 私钥/助记词/API key 进日志或提交进仓库 |
| REL-005 错误恢复 | tx 失败无限重试风暴、reorg 处理死循环、pending 永久卡死、吞掉链错误 |
| REL-001/002/004 稳定性 | 无界扫链、RPC 无超时、websocket 订阅/goroutine 泄漏 |
| REL-003 并发正确性 | 并发处理同一 nonce/账户余额、事件乱序、丢失更新 |
| CHG-001 兼容性 | ABI/事件/响应字段变更破坏调用方、链 ID/网络配置语义变、地址派生方式变致存量资产不可达 |
| CHG-002 迁移发布 | 链上合约升级与链下代码不兼容、索引器 schema 迁移丢数据、不可逆迁移 |

### 3b. 通用后端 / Web2 场景（列表查询、数据统计）

| 规则 | Web2 典型严重场景 |
|---|---|
| COR-003 数值/统计 | JOIN 致重复计数（count/sum 翻倍，应 DISTINCT）、NULL 当 0 统计错、分页 off-by-one、limit/offset 错、时区致按天/月统计错位、金额取整 |
| COR-002 业务不变量 | 聚合破坏守恒（总额对不上）、状态机允许非法流转 |
| SEC-001 认证授权 | 列表查询缺 WHERE 租户/用户隔离，返回他人数据；越权访问 |
| SEC-002 不可信输入 | 排序/过滤/搜索参数拼进 SQL（注入）、动态查询构造 |
| DES-003 循环远程调用 | 列表接口 N+1 查询、循环里逐条查库/调服务 |
| DES-002 增量处理 | 每次全量重算统计而非增量、深分页大 offset 全表扫 |
| DES-005 同步批处理 | 同步请求里跑大聚合/全表统计导致超时 |
| DES-004 权威数据源 | 缓存的统计值过期/与源不一致 |
| REL-001 无界资源 | 列表无分页/无上限全量加载、结果集随数据增长 |
| REL-002/004 稳定性 | 大查询无超时、游标/连接不释放 |
| COR-004 事务边界 | 多步写失败留部分成功 |
| COR-005 幂等 | 重复提交/重复请求产生重复副作用 |

## 4. normal（对照组）选法

外观相似、但有硬上限 / 幂等键 / 兼容路径 / 充分保护的**正常变更**。例：看着像重复扣款风险、但有唯一约束兜底；看着像无界扫链、但有严格分页与小上限。对照组用于检验误报护栏。

不要全部使用 severe 的修复 commit 作为 normal；修复本身可能引入新的边角问题，污染误报率。至少混入一部分独立的、当年平稳上线的正常 feature commit。

## 5. 标注纪律

- 一个 change = 一个 first-parent commit，记全 `base/target` 40 位 SHA。
- severe：`label_note` 点名核心问题 + 历史证据（事故/修复 PR/issue 编号）。
- normal：`label_note` 说明为何是正常变更（有何保护）。
- 标签一经冻结不可改；后续修复与私有标签不得让审查 Agent 看到。

## 6. 成功判据（跑完自动计算，见 `historical_pilot.py::summarize`）

`caught_up_to_builtin_review = true` 需同时满足：

- `builtin_only == 0`：内置审查命中核心问题、而 skill 漏掉的严重变更数为 0。
- `skill.severe_issue_discovery.found >= builtin.severe_issue_discovery.found`。
- `skill.manual_review_findings.normal_rate <= builtin.manual_review_findings.normal_rate`（护栏：正常变更误报不高于内置）。

护栏不可去除：skill 是宿主会话薄封装，去掉护栏后"透传内置 + 乱报"即可平凡地追平召回。

## 7. 采集完成后的流程

```bash
python3 pilot/historical_pilot_initialize.py --manifest <manifest.json> --output .code-quality/historical-pilot-v1
python3 pilot/historical_pilot_verify.py     --workspace .code-quality/historical-pilot-v1
python3 pilot/qualification_batch.py         --workspace .code-quality/historical-pilot-v1 --workers 4
python3 pilot/historical_pilot_review_packet.py --workspace .code-quality/historical-pilot-v1
# 维护者逐条判决 skill / builtin 两条 lane
python3 pilot/historical_pilot_verify.py     --workspace .code-quality/historical-pilot-v1 --write-summary
```

依赖：本机安装 `codex` CLI，且 terra/high 模型与配额可用（skill + builtin 两条 lane，约 2×变更数 次模型运行）。
