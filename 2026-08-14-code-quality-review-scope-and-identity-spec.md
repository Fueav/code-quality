# code-quality v0.5.5 审查范围、增量复核与结果身份规格

状态：Owner 已批准，按本规格实施并发布 `v0.5.5`。

基线：`v0.5.4` / `d10e5db0534c0a45ad7a0bcec529e4635f450426`。

## 1. 目标

让本地、公司 CI 和非 GitHub CI 对同一个 PR 使用同一组可验证输入，得到相同的审查范围与 `review_key`；在同一 PR 只追加提交时，允许只复核新增增量和上一轮未解决的 P0/P1，降低多轮拉锯成本。

本次保留 `v0.5.4` 的发布下限：只有 P0/P1 阻断，P2/P3 仅作为 advisory。CLI 仍是只读评估工具，不修改代码、Git、CI、PR 或远端状态。

## 2. 三个互不混用的维度

以下概念不得放进同一个枚举：

- 审查范围 `review_scope`：`FULL` 或 `INCREMENTAL`。
- 结果来源 `result_source`：`EXECUTED` 或 `REUSED`。
- 生命周期 `lifecycle_status`：`CURRENT` 或 `SUPERSEDED`。

CLI 产生一次不可变的 `EXECUTED` 审查结果。可信缓存、`REUSED` 判定和发布时的 `CURRENT / SUPERSEDED` 判定由公司 CI 集成层负责，并通过外部 envelope 包装 CLI 结果，不得回写或改造原始结果：

```json
{
  "result_source": "EXECUTED",
  "lifecycle_status": "CURRENT",
  "review_result": {}
}
```

## 3. 显式 PR 方向

`doctor`、`run-codex` 和 `run-claude` 增加成对参数：

```text
--base-ref <destination-ref>
--head-ref <source-ref>
```

例如把 Deploy 分支合入 Production 分支：

```sh
quality-review run-codex --repo . \
  --base-ref origin/production \
  --head-ref origin/deploy/dockerhost-dev
```

CLI 必须在调用 Provider 前冻结并输出：

1. base ref 解析出的 base tip commit；
2. head ref 解析出的 head commit；
3. 两者的 merge-base；
4. 实际审查范围 `merge-base..head`；
5. 该范围内排序、去重后的 changed files。

`--base-ref` 与 `--head-ref` 必须同时提供。现有 `--base <commit> --target <commit>` 精确提交范围保持兼容；两组参数不得混用。没有显式参数时，现有 GitHub PR、GitLab MR 和本地 `origin/HEAD` 自动发现行为保持兼容。

## 4. FULL 与 INCREMENTAL

### 4.1 FULL

`--review-scope full` 为默认值。显式 ref 模式审查 `merge-base..head`；现有精确提交模式继续审查调用方给定的 `base..target`。

FULL 的 Provider 输入包含整个 changed-files 集合，输出当前完整审查结论。

### 4.2 INCREMENTAL

`--review-scope incremental` 必须同时提供 `--previous-result <review-result.json>`。调用方不再单独传 previous head；它由上一份不可变结果提供，避免两个真相源。

INCREMENTAL 的 Provider 输入只包含：

- `previous_head..current_head` 的新增 committed delta；
- 上一轮仍需复核的 P0/P1 finding 及其稳定 finding identity；
- 判断这些旧 finding 是否已解决所需的最小上下文。

Provider 必须分别返回新 findings 和每个旧 P0/P1 的 `RESOLVED / UNRESOLVED` 结论。未解决的旧 P0/P1 继续进入当前结果并保持阻断；已解决的旧 finding 留在 resolution 证据中，不再计入当前 blocker。上一轮 P2/P3 不要求逐条复核。

只有同时满足以下条件才允许 INCREMENTAL：

- repository identity、base ref、head ref 与上一轮一致；
- 当前 base tip 与上一轮一致；
- review contract、Provider、model、reasoning effort、execution profile 与上一轮一致；
- 上一轮 head 是当前 head 的严格祖先；
- 上一份结果通过当前版本支持的 schema、内容和 lineage 校验；
- delta 非空。

目标分支变化、base tip 前进、rebase/force-push、review contract 变化、Provider 配置变化、无新增提交或 previous result 不可信时，CLI 必须在 Provider 调用前返回机器可读 `FULL_REQUIRED`，且 `provider_invocations=0`；不得静默扩大为 FULL，也不得把它伪装成 `PASS / BLOCK / ERROR` 审查结果。

## 5. 确定性审查身份

CLI 在 Provider 调用前计算 `review_key`。相同的规范化输入必须产生逐字节相同的 key；运行时间、session 路径、日志路径、临时目录和随机 ID 不得进入 key。

FULL key 至少绑定：

- repository identity；
- base/head refs 及解析后的 commits；
- merge-base 与实际 changed-files；
- tool version、详细结果 schema、Provider 输出 schema、prompt/rubric contract；
- Provider host、model、reasoning effort、execution profile；
- 规范化 review goal；
- `review_scope=FULL`。

INCREMENTAL key 还必须绑定 `parent_review_key`、previous head、current head、delta changed-files 和 `review_scope=INCREMENTAL`。

实现可以把稳定合同聚合为可审计的 `contract_digest`，但结果必须同时保留足够字段，允许消费者解释 key 为什么变化。key 使用明确版本前缀和 SHA-256；序列化必须规范、与 Go map 遍历顺序无关。

## 6. 版本化输出

- 原生详细结果升级到 schema v8；v3-v7 文件保持不可变。
- `review-summary.json` 升级到 schema v3。
- 详细结果至少新增：`review_key`、`contract_digest`、`review_scope`、`parent_review_key`、`previous_head`、`current_head`、`delta_changed_files`、`previous_finding_resolutions` 和 `new_findings`。
- finding 获得由规范化 finding 内容确定的稳定 identity；同一份冻结输入不能因运行顺序改变 identity。
- FULL 结果的 `parent_review_key`、`previous_head` 和 resolutions 使用明确的空值语义；INCREMENTAL 结果必须完整记录 lineage。
- Markdown 与 JSON summary 明确显示 scope、review key、当前 head、blocking/advisory 数量；INCREMENTAL 还显示已解决、未解决和新增 finding 数量。
- 结果校验必须拒绝非法 scope/lineage、重复或未知 resolution、遗漏旧 blocker resolution、key/digest 不一致，以及与 P0/P1 判定不一致的 `PASS / BLOCK`。

## 7. 公司 CI 集成合同

本仓库只交付 CLI 原语和文档化集成合同，不在 CLI 内实现公司级缓存或 PR 发布状态。

集成层应当：

1. 在执行前用规范化输入得到 review identity；可信存储命中完全相同的 `review_key` 时可返回 `REUSED`，Provider 调用数为 0；
2. 发布前重新读取 PR 当前 base/head，并用 compare-and-swap 语义核对本次冻结值；已变化的结果标为 `SUPERSEDED`，不得作为当前 required check 结论发布；
3. 对复用结果进行签名或 attestation 校验，并保留原始不可变 CLI 结果；
4. 用外部 envelope 表达 `result_source` 和 `lifecycle_status`。

CLI 不声称重复模型调用会生成相同 findings；它只保证相同输入的范围、合同摘要和 `review_key` 相同。

## 8. Harness 职责

Harness Skill 或项目内 Harness 负责 `修复 → 测试 → 提交 → 复查 → 推送` 的编排、自动轮次上限和人工升级策略。CLI 只提供可组合的 FULL/INCREMENTAL 审查原语与机器结果，不内置循环，也不决定何时修改代码。

## 9. 明确不做

- 不自动修改、提交或推送代码；
- 不在 CLI 内重试、循环或限制修复轮数；
- 不猜测 Deploy/Production 等业务分支关系；
- 不增加第二 Provider、验证 Agent 或交叉复核；
- 不实现公司级缓存、签名、PR compare-and-swap 或状态发布；
- 不改变 `v0.5.4` 的 P0/P1 阻断、P2/P3 advisory 语义；
- 不承诺相同输入的两次模型执行产生相同文本或 findings。

## 10. RED / GREEN / 发布验收

1. RED 测试证明 `v0.5.4` 无法接受显式 base/head refs、无法区分 FULL/INCREMENTAL、没有 review identity，并会重复调用 Provider。
2. `Deploy → Production` fixture 必须证明 base tip、head、merge-base 和 changed-files 与 `git diff merge-base..head` 一致。
3. 本地与 CI 使用相同规范化输入时产生相同 `review_key`；改变任一受绑定输入必须改变 key。
4. INCREMENTAL fixture 只把 delta 和上一轮 P0/P1 交给 Provider，且完整覆盖 resolved、unresolved、新 finding 与非法 resolution。
5. rebase、目标 ref/base tip、review contract 或 Provider 配置变化时返回 `FULL_REQUIRED`，Provider 调用数为 0。
6. 旧 `--base/--target`、GitHub PR、GitLab MR 和默认本地发现契约继续通过。
7. schema v8 / summary v3、CLI help、doctor、Codex/Claude 单次执行、文档和插件契约同步验证。
8. 公司 CI 的 `REUSED / SUPERSEDED` 只提供版本化集成 fixture/示例和合同测试；不伪造外部系统已部署证据。
9. 在干净 release commit 上运行 `make release-check VERSION=v0.5.5 VERIFY_COMPARE_REF=v0.5.4`、workflow syntax/actionlint 和四平台 `make dist`。
10. 原子推送 `main` 与 annotated `v0.5.5` Tag，创建 GitHub Release；公开下载资产逐字节比对、校验 checksum，并验证 installer 与 Codex/Claude bootstrap。

## 11. 实施纪律

- 先提交本规格，再开始架构设计与 RED 测试；后续实现不得静默修改本规格。
- 架构设计使用 deep Module / Interface / Seam / Adapter / Depth / Leverage / Locality 语言，并把选择与删除测试写入仓库文档。
- 若实现发现规格冲突，必须先停止并取得 Owner 决策，不以代码便利性反向改需求。
- 发布证据只记录真实执行结果；Tag、Release 和公开资产验证成功前不得预先声称发布完成。
