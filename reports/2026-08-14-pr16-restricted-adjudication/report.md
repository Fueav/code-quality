# PR #16 受限裁决定向实验

## 结论

这个真实案例支持在原生 Codex 审查之后增加“冻结结果受限裁决层”。PR #16 最后一轮原生结果是 1 个 P1，因此基线为 `BLOCK`；同一条 finding 原样输入受限裁决后，普通代码计算为 `REJECT`，最终不阻断。

这证明该架构能过滤本 PR 最后一轮的犄角旮旯问题，但单例不能证明总体错误率“显著下降”。

## 冻结范围

- PR：[moss-site/agent_marketplace#16](https://github.com/moss-site/agent_marketplace/pull/16)
- 原生报告：[最终增量审查](https://github.com/moss-site/agent_marketplace/pull/16#issuecomment-5292448482)
- 实验 base：`de83c595dc75271cf81080a10c6a4389b1ca3404`
- 实验 target：`cbbd7cc76041333fb0b25df06661d671d88db7c8`
- 原生 finding：`[P1] Drain the worker lease before starting the handoff clock`
- 原生 finding 哈希：`b0126f63d0c12dda257481d73cf14c3eca6f84942ad08e6f807ec5257e16e10d`
- 裁决模型：`gpt-5.6-sol`，reasoning `max`
- 调用次数：1；只读、无网络、忽略仓库 prompt/rules、无 hooks

| 维度 | 原生结果 | 受限裁决 |
| --- | --- | --- |
| 门禁 | BLOCK | 不阻断 |
| 优先级/处置 | P1 | REJECT |
| 有效性 | 未拆分 | CONTRADICTED |
| 影响 | 未拆分 | S2 |
| 触发可达性 | 未拆分 | T1 |
| 证据 | 未拆分 | E1 |
| 本轮引入或恶化 | 隐含为是 | false |
| 具体触发 | 隐含为是 | false |
| 完整因果链 | 隐含为是 | false |

## 为什么不应阻断

原生结论需要一个额外前提：旧 worker 在 `reserveBaseWeight` 已提交、`UserFillsByTime` 尚未开始的相邻代码窗口中，被外部挂起超过 10 分钟，随后恢复。目标仓库没有部署事件、调用行为、测试或复现证明这个触发可达。

独立核验与裁决证据一致：

1. advisory lease `(8411, 1)` 覆盖完整 worker run，并且在实验 base 已存在。
2. reservation 后立即调用 `UserFillsByTime` 的顺序在 base 已存在；目标代码中没有能在两者之间阻塞 10 分钟的应用路径。
3. 保留配置给单次 HTTP 调用 10 秒超时；模块预算为 600，而仓库文档称上游 IP 限额为 1200。即使人为制造一次最多 120 weight 的逸出，也没有证据闭合到供应商限流、生产中断或重试风暴。
4. migration 23 没有获取 advisory lease，但它新增持久 fence 并重启 10 分钟 handoff，相对 base 收窄了旧 worker 的可达路径。
5. retained tests 验证 fence、handoff clock 和新旧写入行为，没有覆盖“reservation 后进程暂停超过 10 分钟再恢复”的场景。

把 `introduced_or_worsened_by_change=false` 判成 `CONTRADICTED/REJECT` 略偏强；如果采取更保守口径，也可以给 `INSUFFICIENT/MANUAL_REVIEW`。但该 finding 同时只有 S2/T1/E1，按冻结规则两种口径都不能成为 P1/BLOCK。

## 对架构的判断

这个案例显示，问题不在原生 Codex 能否发现边缘风险，而在发现后的评级权仍被原生 priority 直接控制。两轮强制收敛只能限制同一次流程的轮数；每次修复产生新 head 后，原生审查仍可把更窄的新假设重新定成 P1，因此会形成 `4 P1 → 2 P1 → 1 P1 → 1 P1` 的追逐。

受限裁决层改变的是退出条件：

1. 原生 Codex 继续自由发现问题。
2. 原生结构化结果立即冻结，不让裁决层新增、拆分、合并或改写 finding。
3. 只把 P0/P1 送入裁决，按项目“保下限”文档判断 S/T/E、变更归因、具体触发和完整因果链。
4. 模型只返回事实字段；普通程序按固定公式计算最终 `BLOCK`。
5. 未达下限的问题最多进入人工查看或 advisory，不再触发新的自动修复轮。

## 成本与边界

- 本次裁决耗时 447.7 秒。
- Codex 记录的用量为 1,281,807 input tokens、14,698 output tokens。
- 因而它适合只裁 P0/P1，不适合再审全部 P2/P3。
- 本次是定向案例验证，不是盲集统计；能说“PR #16 的最终误阻断被过滤”，不能说“总体错误显著减少”。

建议下一步用最近 10 到 20 个已经有人工终局判断的 P0/P1 做 shadow replay，只运行冻结裁决，不重新运行原生审查。这样每个样本只有一次调用，能直接测假阻断下降和严重问题保留率。
