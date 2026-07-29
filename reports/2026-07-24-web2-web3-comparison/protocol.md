# code-quality v0.1.1 Web2/Web3 对照实验协议

## 目标

比较同一 Codex CLI、同一模型、同一推理强度、同一匿名代码增量下：

1. Codex 官方内置 `codex exec review --commit HEAD`；
2. 安装 code-quality v0.1.1 plugin 后，用自然语言“帮我审下这个改动”触发的 report-only 审查。

## 样本设计

- Web2：查询过滤计数、列表分页、数据统计各 1 例。
- Web3：日志扫块区间、reorg 后链索引恢复、PreBlock 事件索引各 1 例。
- 每例来自公开 GitHub 已合并修复。实验把修复提交的生产代码补丁反向应用到修复后的源码树，构造一个匿名 target commit；测试、CHANGELOG、PR 标题、issue 和 ground truth 不进入审查仓库。
- 每个匿名仓库只含两个提交：`baseline snapshot` 与 `candidate change`。
- 两条 lane 必须先全部完成并固化最终输出，之后才按 ground truth 评分。

## 固定环境

- CLI：Codex CLI 0.145.0。
- 模型：`gpt-5.6-terra`。
- 推理强度：`high`。
- code-quality：GitHub Release v0.1.1 的 CLI 与 plugin payload。
- 两条 lane 使用独立 `CODEX_HOME`，只共享同一份认证；builtin home 不安装 plugin，skill home 仅安装 code-quality plugin。
- 审查进程只读当前匿名仓库，禁止读取仓库外路径；不得运行会修改工作树的命令。

## 评分量表

每例、每条 lane 独立评分：

- `core_hit`（0/1）：明确指出 ground truth 的核心根因，并连接到正确的生产影响。
- `location`（0–2）：0=未定位或错误；1=文件/函数正确但范围模糊；2=具体改动行或最小代码块正确。
- `impact`（0–2）：0=错误；1=方向正确但缺关键触发条件/后果；2=触发条件与后果均准确。
- `fix`（0–2）：0=无/错误；1=可行但笼统；2=给出与历史修复等价的最小可执行方案。
- `evidence`（0–2）：0=纯猜测；1=有代码依据但链路不完整；2=用变更和相邻调用/不变量形成闭环。
- `false_positive_count`：与本次增量无关、不可由当前代码支持或生产影响不成立的独立 finding 数。
- `actionable`（0/1）：维护者可据报告直接确认问题并着手修复。

汇总指标：核心问题命中率、Web2/Web3 分组命中率、平均质量分（`location+impact+fix+evidence`，满分 8）、误报数、actionable 率、P50 时延、输入/输出 token。

## 判读边界

- 这是 6 个定向严重缺陷的 paired replay，不包含正常变更，因此不能估计真实 PR 分布下的误报率或总体准确率。
- 逆向修复补丁隔离了根因，但可能比自然演化中的原始引入提交更小、更容易审查。
- 单次模型运行存在随机性；本轮只报告单次 paired 结果，不声称统计显著性。
- code-quality 是宿主模型上的 policy/workflow 层，不是独立模型；结论描述“引导带来的增量”，不描述为两个模型的能力差。
