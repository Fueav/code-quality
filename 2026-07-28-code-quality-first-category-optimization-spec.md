# Code Quality 第一类优化实施规格(交 Codex 实施)

日期:2026-07-28。基线:main @ 656be6c(v0.2.0)。

本规格只包含"不需要新评测背书"的四项改动:依据是已有失败证据或纯逻辑推理。任何 lens 措辞的质量性调优、compare/评测口径改动、多独立 session 采样均**不在本轮范围**。

通用约束:
- 遵循仓库既有测试风格;每项先写 RED 契约测试再实现。
- 修改 `SKILL.md`、`policy/**.md` 等 prompt/文档时先删后加,净行数默认不增。
- 本轮不 push、不 tag、不发 release;版本号(`SkillVersion`、plugin.json、README 安装命令)留到下次 release 统一升为 0.3.0,不在本轮改。

---

## 改动 1:prepare 在 Git 元数据不可写时退回 shared clone

### 证据与根因

7-24 评测中 workspace-write sandbox 下 6/6 run 在 prepare 阶段失败:`internal/session/session.go` 的 `addWorktree`(`git worktree add --detach`)需要写主仓库的 `.git/worktrees`,sandbox 拒绝该写入,一份正式报告都没产出。会话目录本身(默认 `.code-quality/`,在 workspace 内)是可写的,被拒的只有主仓库 `.git` 写入。

### 行为契约

1. `Prepare` 先按现状尝试 `git worktree add --detach`(最便宜,零对象复制)。
2. worktree 失败时,自动退回:
   - `git clone --shared --no-checkout <repositoryRoot> <layout.RepositoryDir>`
   - `git -C <layout.RepositoryDir> checkout --detach <targetCommit>`
   - 所有写入都落在会话目录内,不碰主仓库 `.git`。
   - 注:`--shared` 通过 alternates 共享对象库,即使 target commit 不被任何 ref 引用,checkout 仍能命中对象,无需 fetch。会话是短生命周期的,alternates 的 gc 风险可接受,不要用 `--dissociate`(会复制对象,大仓库代价高)。
3. `session-metadata.json` 与 `Prepared` 返回体新增 `checkout_mode: "worktree" | "clone"`(metadata `schema_version` 保持 1;跨版本 prepare/finalize 已被 `SkillVersion` 相等性校验挡住,加字段安全)。
4. 清理按模式分派:
   - `worktree` 模式:维持现有 `git worktree remove --force`。
   - `clone` 模式:直接 `os.RemoveAll(RepositoryDir)`,删除前必须校验该路径确实位于 `SessionDir` 之下,否则拒绝删除(参照 `cleanupPartialSession` 的防护写法)。
   - `Finalize` 的 `cleanupWorktree` 从 metadata 读 `checkout_mode` 决定分派;prepare 失败路径上的 defer 清理同样分派。
5. `SKILL.md` 第 1 步措辞更新为两种模式的事实:prepare 优先创建 git worktree(需写 `.git/worktrees`),宿主拒绝该写入时自动退回会话目录内的 shared clone,不写主仓库 Git 元数据;仍先向用户说明再执行。净行数不增。
6. `plugin_contract_test.go` 若锁定 SKILL.md 内容,同步更新。

### 验收

- 新测试:把测试仓库 `.git` 目录 chmod 为不可写(或对 `addWorktree` 注入失败),`Prepare` 成功返回 `checkout_mode=clone`,`RepositoryDir` 是 checkout 到 target commit 的可用仓库,主仓库 `.git` 无任何新增条目。
- 新测试:clone 模式下 `Finalize` 完成后 `RepositoryDir` 被删除,`SessionDir` 与两份报告保留。
- 新测试:clone 模式清理拒绝删除 `SessionDir` 之外的路径。
- 既有 worktree 路径测试全部保持绿。

---

## 改动 2:去掉"最多 3 条发现"上限,禁止合并独立根因

### 证据与根因

7-24 的 web3-chain-indexer-reorg 样本:内置审查报 3 条命中全部 3 个已知缺陷组件;本工具受"最多 3 个根因 + 鼓励合并"引导,合并成 1 条并漏掉 section 边界 off-by-one。对定位为"提高质量下限"(降低漏报)的工具,数量上限与合并倾向直接制造漏报。schema 的 `findings` 数组本无 `maxItems`,上限纯属 policy 文本。

### 行为契约

统一替换为同一语义:**每个满足 5.1 门槛的独立根因报一条发现,按影响从高到低排序,不设固定数量上限;只有同一根因的多处表现才允许合并,不得把不同根因捆进一条发现。** 噪音控制完全依靠既有的发现门槛(引入或加重 + 具体位置 + 现实输入 + 实质影响 + 具体修复),不依靠数量截断。

落点(全部为文本替换,净行数不增):

- `policy/v1.1/review-lens.md:3`:"最多 3 个最高影响的独立根因"。
- `policy/v1.1/rubric.md:245`(5.1 门槛)与 `rubric.md` §6.1 第 4 步("保留至多三个")。
- `policy/v1.1/workflow.md:8`:"Report at most the three highest-impact independent root causes"。

policy 版本处理:

- 这是审查语义变更,`policy/manifest.json` 的 `policy_version` 从 `1.1.1` 升为 `1.2.0`;目录 `policy/v1.1/` 更名 `policy/v1.2/`,同步 `bundle.go` 的 embed 路径与读取路径、`manifest.json` 的 `rubric` 路径、rubric.md 头部版本与日期。
- 全仓 grep `policy/v1.1` 与 `V1.1`,同步所有引用(含 `SKILL.md` 的 summary/description 若提及 V1.1)。历史 reports/ 快照不改。

### 验收

- 全仓 grep 无 "最多 3"/"最多报告三"/"at most the three" 残留(reports/ 历史快照除外)。
- 新测试或改造既有测试:一份含 5 条不同 `rule_id` 独立发现的 main review 通过 finalize,5 条全部进入 `review-result.json`。
- `go test ./...` 绿;embed 路径更名后 `bundle.go` 相关测试绿。

---

## 改动 3:复审从"仅零发现触发"泛化为"对未激活维度反证复审"

### 证据与根因

现在 `internal/session/finalize.go` 只在第一轮零发现时返回 `REREVIEW_REQUIRED`;第一轮有任何发现就直接终结。7-24 的 6 个样本每例都有发现,复审路径一次都没走到——该机制目前只覆盖小概率分支。提下限的核心手段是降低漏报方差,复审应覆盖每次运行的盲区,而不是只覆盖零发现。

### 行为契约

1. 定义 `rereview_scope`:D1–D4 中**第一轮没有任何发现的维度**集合(由第一轮 findings 的 `rule_id` 映射到维度计算,映射用 `policy/manifest.json` 的 rules 表)。
2. 第一轮 main review 合法解析后:
   - `rereview_scope` 非空且 `rereview.json` 不存在 → 返回 `REREVIEW_REQUIRED`,返回体在现有字段基础上新增 `rereview_scope`(维度数组);`NextReviewPath` 等字段维持现状。
   - `rereview_scope` 为空(四个维度第一轮都有发现)→ 直接 `writeComplete`,`completed_review_rounds=1`、`retry_count=0`。
   - 零发现时 `rereview_scope` 自然等于全部四维,行为向后兼容现有零发现复审。
3. 第二轮语义(改 `policy/**/workflow.md` 与 `SKILL.md` 第 4 步,净行数不增):复审只针对 `rereview_scope` 内的维度做反证式检查——主动寻找"该维度确实存在缺陷"的反例,只报告新增发现,允许为空;不重复第一轮已报维度。
4. 合并、去重、ID 冲突重命名逻辑不变。两轮后 `retry_count=1`;两轮均零发现的可验证裁决原因(`quality/adjudicate.go`)不变。状态机上限仍为 2 轮。

### 验收

- 新测试:第一轮仅 D2 有发现 → `REREVIEW_REQUIRED` 且 `rereview_scope=["D1","D3","D4"]`;第二轮补一条 D3 发现 → 合并进最终报告,`completed_review_rounds=2`、`retry_count=1`。
- 新测试:第一轮四个维度均有发现 → 直接 `COMPLETE`,`completed_review_rounds=1`。
- 既有测试改造:零发现路径断言 `rereview_scope` 为全四维;两轮零发现裁决原因不变。
- 成本说明(写进实施报告即可,不是代码):复审范围从全量收窄为未激活维度,单轮增量成本有界;与改动 4 的省量部分抵消。

---

## 改动 4:压缩运行成本(diff 上下文与强制读取项)

### 证据与根因

7-24 数据:本工具 P50 86.5s vs 内置 36s,单例均摊约 37 万 input token。两个确定的机械浪费:

- `internal/session/session.go` `writeTrustedDiff` 用 `--unified=40`,每个 hunk 带 40 行上下文,trusted.diff 体积数倍膨胀;而模型本就拥有完整 checkout,可以自己按需读上下文。
- `policy/**/workflow.md` 第 1 步强制读 `review-request.json`、`trusted.diff`、`rubric.md`、`evidence-context.json` 四个文件,第 5 步还需理解 `model-review.schema.json`——五次读取轮次,其中 schema 全文与空 evidence 读取是纯开销。

### 行为契约

1. `writeTrustedDiff` 的 `--unified=40` 改为 `--unified=6`。
2. workflow.md 第 5 步:内联一个最小合法输出示例(仅含 0.2.0 已定的必需字段:`id`、`rule_id`、`code_locations`、`production_impact`、`minimal_fix`,加顶层必需数组),读 `model-review.schema.json` 降级为"不确定时再查";schema 文件仍写入会话、finalize 校验逻辑不变。
3. evidence 为空时,`Prepared` 输出与 workflow 措辞让模型跳过读取 `evidence-context.json`(例如 `Prepared` 增加 `evidence_present: bool`,workflow 第 1 步注明 evidence 缺失时跳过)。
4. workflow.md 净行数不增(内联示例的增行用删减换取,可压缩第 1 步的枚举式措辞)。

### 验收

- 新/改测试:trusted.diff 以 unified=6 生成(对固定 fixture 断言体积显著小于 unified=40 的产物,或直接断言 git 参数)。
- 新测试:无 evidence 时 `Prepared.evidence_present=false`。
- workflow.md 内联示例本身能通过 `quality.DecodeModelReview` 校验(加一个从文档提取示例或复制同构 JSON 的测试,防示例烂掉)。
- `go test ./...` 绿。

---

## 实施顺序与产出

按 1 → 2 → 3 → 4 实施,每项独立提交,提交信息注明改动号。完成后输出实施报告:RED→GREEN 证据、逐文件增删行数、`gofmt` 与 `go test ./...` 最终输出,格式参照 `2026-07-27-code-quality-v0.2.0-orchestration-report.md`。
