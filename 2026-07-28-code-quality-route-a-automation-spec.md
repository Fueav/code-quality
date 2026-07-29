# 路线 A 全自动化规格:live 审查 + git 前向裁决(交 Codex)

日期:2026-07-28。取代人工标注方案:审查自动触发,发现由后续 git 历史自动裁决,无人工介入。方法论与路线 B 挖掘同构,方向相反(路线 B 从修复回溯缺陷,本方案从发现前瞻修复)。

## 明确的取舍(写进 README,不隐瞒)

自动裁决弱于人工标注:只有"后来被修了"能强确认;"一直没人动"分不清噪音和未修的真问题,只能给弱标签。换来的是零人工成本与全量覆盖。`confirmed_by_later_fix` 是最有价值信号:工具在 commit N 报告、修复者在 N+k 独立修掉 = 工具先于人看到问题。

## 组件(全部放 `pilot/live/`,数据放仓库外 `~/AiProject/code-quality-live/`)

### 1. `live_watch.sh` — 每日批量审查(cron)

- 监视仓库清单在 `~/AiProject/code-quality-live/config.json`(初始:agent_marketplace、general-agent-ai、code-quality)。
- 每仓库维护 watermark(上次已审 commit);对新增非 merge、触及源码、改动 ≤3000 行的提交逐个执行 product lane:
  - 隔离克隆配方与 lane prompt 沿用 `2026-07-28-code-quality-spec-first-v2-spec.md`(shared clone、剥 refs、禁用宿主 memory 的 codex exec);
  - 二进制用 main 最新 tag 构建;串行执行,单日上限 10 个提交(超出顺延,防止批量 rebase 打爆)。
- 归档:`~/AiProject/code-quality-live/reviews/<repo>/<sha>/review-result.json` + 一行 JSONL 索引(sha、时间、发现数、semantic_result、耗时)。
- 安装:`make live-install` 写入 crontab(每日一次,时间可配);`make live-uninstall` 移除。失败不重试本轮,记入 JSONL 由下轮补。

### 2. `live_adjudicate.py` — 每周前向裁决(cron)

对归档中每条未终局发现:

1. **机械预筛**:扫描该 repo 自发现以来的新提交,找触及 `code_locations` 同文件 ±10 行的提交;无候选且发现年龄 <30 天 → 保持 `open`。
2. **配对判定**(仅对有候选者,每对一次 `codex exec -s read-only --output-schema`):给出发现全文与候选提交 diff,判 `fixes`(修复了该发现描述的问题)/ `touches_only`(改了位置但与该问题无关)/ `unclear`。
3. **标签状态机**:`open` → `confirmed_by_later_fix`(存在 fixes 判定)/ `superseded`(位置被重写但非修复,终局,不计入噪音)/ `stale_probable_noise`(open ≥30 天且无候选)。终局标签不再复判。
4. 汇总:滚动输出各标签占比、零发现率、按 D1–D4 与仓库拆分,格式对齐 `pilot/live_report_summary.py` 的口径(`confirmed_by_later_fix` 映射 adopted 类,`stale_probable_noise` 映射 noise 类);写 `~/AiProject/code-quality-live/summary.md`(覆盖式)+ 按周留存快照。

### 3. 测试与验收

- watermark 推进、3000 行过滤、单日上限:单元测试。
- 裁决状态机(含终局不复判、±10 行匹配边界):单元测试,fixture 用合成小仓库。
- 端到端冒烟:对 code-quality 仓库最近 2 个真实提交跑 `live_watch.sh` 一次,归档与 JSONL 齐全,`live_adjudicate.py` 空转(无候选)不报错。
- README(`pilot/live/README.md`):架构、取舍声明、安装/卸载、数据目录布局。

## 不做

- 不做 per-commit hook(侵入用户工作流);不做 CI 接入;不做模型自评标注(违背"裁决来自现实"原则);不改动 quality-review 引擎与 policy。

## 与现有工作的关系

- v2 实验(spec-first)独立进行,互不阻塞;live 数据两周后首次汇总,作为 lens 调优与"缺失型缺陷"设计的真实分布输入。
- 若某 `confirmed_by_later_fix` 案例修复者当时未看报告,单独标注为 `independent_confirmation`——这是"工具先于人"的最强证据,积累起来就是对外主张价值的素材。
