# code-quality v0.5.8 原生冻结后受限裁决断点恢复规格

状态：Owner 已在本任务中批准按本规格实现；release、公司 CI 部署和服务重启仍需再次确认。

产品基线：`v0.5.7` / `aeafa239a137b55b47a9305f14e95c3fc9fcc38e`。实现分支同时包含已交付的 Harness Repository Contract。

## 1. 问题与目标

v0.5.7 把一次审查实现成同一进程内的单体事务：Native Review 冻结后立即执行 Restricted Adjudication；第二阶段遇到 Provider quota、capacity、rate limit、deadline 或进程中断时，只能让外围重新启动完整事务。延长 Jenkins 总超时只能降低同一进程被终止的概率，不能保存阶段完成事实，也不能避免下一次构建重新支付一次完整 Native Review。

v0.5.8 把阶段边界变成可验证、可恢复的持久契约。Native 成功冻结后，后续执行复用同一 review identity 和冻结证据，只允许继续 Restricted；任何验证失败都在 Provider 调用前关闭恢复路径，且绝不静默回退为 FULL。

事故仓库、历史提交、原始 Provider 日志和本机手工恢复目录只作为仓库外只读证据，不进入本仓库、测试 fixture、release artifact 或公开文档。

## 2. 状态机

一个 v0.5.8 session 只有以下持久状态：

```text
PLANNED
  -> NATIVE_RUNNING
  -> NATIVE_FROZEN
  -> RESTRICTED_RUNNING
  -> RESTRICTED_RETRYABLE
  -> PUBLISHED
  -> MANUAL_REQUIRED
  -> TERMINAL_ERROR
```

允许的转移为：

- `PLANNED -> NATIVE_RUNNING`；
- `NATIVE_RUNNING -> NATIVE_FROZEN | PUBLISHED | TERMINAL_ERROR`；
- `NATIVE_FROZEN -> RESTRICTED_RUNNING`；
- `RESTRICTED_RUNNING -> PUBLISHED | RESTRICTED_RETRYABLE | MANUAL_REQUIRED | TERMINAL_ERROR`；
- `RESTRICTED_RETRYABLE -> RESTRICTED_RUNNING | MANUAL_REQUIRED | TERMINAL_ERROR`；
- `PUBLISHED`、`MANUAL_REQUIRED`、`TERMINAL_ERROR` 为终态。

`PUBLISHED` 只表示已原子发布一份完整、可验证的 v10 PASS/BLOCK/ERROR 结果。Restricted retryable 失败不发布 PASS、BLOCK 或旧式 ERROR result；外围从 checkpoint 状态发布自己的 ERROR/HOLD 或 retryable check。`MANUAL_REQUIRED` 不发布 CLI result，返回退出码 5。不可恢复的范围、合同、证据或协议错误进入 `TERMINAL_ERROR`，返回退出码 1。

## 3. Session checkpoint

新增严格的 `native-session-checkpoint` schema v1。`checkpoint.json` 位于 session 根目录，每次状态更新都用同目录临时文件、`fsync`、原子 rename 和目录 `fsync` 完成。checkpoint 至少绑定：

- `tool_version=0.5.8`、checkpoint schema、单调 sequence、当前状态和 publication 状态；
- `review_key`、`contract_digest`、完整 review identity；
- repository identity、FULL/INCREMENTAL scope、base/head refs、base tip、merge base、base commit、target commit；
- 排序后的 full/provider changed files、trusted diff 大小和 SHA-256；
- Provider host、model、reasoning effort、execution profile；
- result schema、Provider output schema、实际 Native prompt、rubric、Restricted policy、Restricted schema及其 SHA-256；
- Native final message、stdout、stderr、freeze manifest、metrics及各自大小和 SHA-256；
- 完整的私有 Native intermediate outcome，以及冻结 P0/P1 finding ID、内容和顺序；
- Native/Restricted attempt ledger、最终采用的 Restricted attempt、是否经过 resume、恢复前 session digest；
- published result、Markdown、summary 的 digest，或明确的未发布状态。

`session_digest` 使用带版本前缀的规范 JSON SHA-256，覆盖所有稳定身份、冻结输入、Native 证据和已经完成的 attempt record；时间、绝对 session 路径和 checkpoint 当前状态不进入该 digest。resume 成功后的 v10 结果记录恢复开始时验证通过的 `session_digest`。

v0.5.8 不恢复没有 checkpoint 的 v0.5.7 session，也不迁移旧 layout。版本、schema 或 digest 不匹配时在 Provider 调用前拒绝。

## 4. 文件与锁安全

- session、`input/`、`output/`、`restricted-attempts/NNNN/` 必须是 owner-controlled、非 symlink 的 `0700` 目录；文件必须是 owner-only `0600` 或冻结后的只读 `0400` regular file。
- 所有读取先 `Lstat`，打开后验证 inode、类型、大小和权限；拒绝 symlink、hard-link 替换、路径逃逸及 session 外 artifact path。
- 每次 Provider 调用同时持有全局 Provider lease 和 session 级非阻塞独占锁。锁 FD 继承给 Provider 子进程，因此父进程终止但子进程仍存活时，另一个 resume 不能并发启动。
- 恢复前验证 checkpoint、所有已登记 digest、attempt 目录、publication 状态和锁；任一失败时 `provider_invocations=0`。
- 恢复时从 checkpoint 记录的 owner-controlled repository object store 验证 target commit 存在，并重建该 commit 的 detached checkout。CLI 不 fetch、不切换业务分支、不修改被审查仓库。

## 5. Attempt ledger

Native attempt 每个 review identity 固定为 1 次。Restricted 最多 2 次：首次 transaction 加至多一次正式 resume。

Restricted attempt 使用不可覆盖目录：

```text
output/restricted-attempts/0001/
output/restricted-attempts/0002/
```

每个目录包含独立 stdout、stderr、final message、freeze manifest、metrics 和 `attempt.json`。`attempt.json` 记录 attempt number、是否由 resume 启动、开始/完成时间、结构化 failure class、状态、artifact digest 和 attempt digest。创建使用 `O_EXCL`；完成后不得重写。

最终审计恒等式：

```text
provider_attempts_total = native_attempts + restricted_attempts
native_attempts = 1
0 <= restricted_attempts <= 2
provider_attempts_total <= 3
```

成功 result 还记录 `adopted_restricted_attempt`、`resumed` 和可空的 `resumed_session_digest`。没有 Native P0/P1 时 `restricted_attempts=0`，不会进入 resume。

## 6. 失败分类与重试边界

只有以下结构化分类可进入 `RESTRICTED_RETRYABLE`：

- `PROVIDER_QUOTA`；
- `PROVIDER_CAPACITY`；
- `PROVIDER_RATE_LIMIT`；
- `DEADLINE_EXCEEDED`；
- `PROCESS_INTERRUPTED`。

artifact digest/路径/权限错误、review identity 或 contract 漂移、policy/schema/prompt 不一致、target commit 不存在、finding ID/顺序变化、Restricted schema 或证据验证失败以及未知协议错误均为 terminal。不得把未知错误默认为 retryable。

checkpoint 停在 `NATIVE_FROZEN` 时，resume 可以启动 Restricted attempt 0001。checkpoint 停在 `RESTRICTED_RUNNING` 且 session 锁已释放时，resume 将未完成 attempt 冻结为 `PROCESS_INTERRUPTED` 后再决定是否还有一次额度；该未完成调用计入 Restricted attempt。

第二次 Restricted 失败后，无论错误原本是否 retryable，都原子进入 `MANUAL_REQUIRED`、返回退出码 5；后续 resume 幂等返回同一状态且 Provider 调用数为 0。`PUBLISHED` session 再次 resume 幂等返回现有结果，Provider 调用数为 0。

## 7. 正式 CLI

唯一生产恢复入口为：

```text
quality-review resume-restricted --session <absolute-session-dir>
```

该命令只接受绝对 session 目录及 Restricted deadline/heartbeat 运行参数；不接受 repo、base/head、scope、model、effort、profile 或 goal 覆盖，避免产生第二个真相源。私有 manual helper 不是生产入口。

`run-codex` / `run-claude` 新增独立可配置 Native 与 Restricted deadline；CLI 捕获 SIGINT/SIGTERM，通过 context 终止 Provider，并尽可能冻结部分日志和更新 checkpoint。运行中的 Provider 每 45 秒默认向 stderr 输出一次不含路径、finding、token、凭据或 Provider 原文的 heartbeat：stage、attempt、elapsed。

## 8. Metrics 与公开信息边界

Native 与每个 Restricted attempt 分别记录 duration、input/output/cached-input tokens；Provider 不提供 usage 时记录结构化 unavailable reason。quota/capacity/rate-limit 不得解释为宿主 CPU/RAM 故障。

公开 result、summary、GitHub Check 和 company envelope 不得包含被 adapter 丢弃的 candidate 正文、raw Provider log、token 数、凭据或宿主绝对路径。私有 checkpoint 可以保存冻结 candidate，但必须保持 owner-only。

## 9. Wire compatibility 决策

v9 的 `execution.provider_invocations` 只能是 1 或 2，无法无歧义表达一次恢复后的真实 3 次 attempt。因此：

- `review-result-v8.schema.json`、`review-result-v9.schema.json` 保持逐字节不变；
- `review-result-envelope-v1.schema.json`、`review-result-envelope-v2.schema.json` 保持逐字节不变，并继续分别引用 v8/v9；
- 新增 result schema v10；
- 新增 company envelope v3，且只引用 v10；
- 新增 checkpoint v1、Restricted attempt v1 和 stage metrics v2 schema；
- prompt/review contract 升级并绑定 Restricted policy/schema 与 rubric digest，因此 v0.5.7 result 不会被当作同合同增量 parent；
- review round lineage 与 stage retry 继续是两个维度。Restricted retry 不创建新的 FULL/INCREMENTAL review round。

v10 `execution` 在 v9 字段基础上新增：

- `native_attempts`；
- `restricted_attempts`；
- `provider_attempts_total`；
- `adopted_restricted_attempt`；
- `resumed`；
- `resumed_session_digest`。

`provider_invocations` 为兼容人类和现有 summary 语义继续保留，但在 v10 中必须等于 `provider_attempts_total`，取值 1 到 3。

## 10. Company CI service 契约

公司 service 必须：

1. 把 session root 和 Git object mirror 持久化在 Jenkins workspace 外，并配置 owner-only 权限和安全保留期；
2. 以 `(repository, review_key, contract_digest, runner_policy_version)` 建立可信索引；
3. GitHub rerun 或明确内部 retry 只在 checkpoint 为 `NATIVE_FROZEN`、`RESTRICTED_RUNNING` 或 `RESTRICTED_RETRYABLE` 时调用 `resume-restricted`；
4. 通过数据库 CAS/分布式锁与 CLI session lock 双重防止同一 session 并发恢复；
5. checkpoint 缺失或不可验证时返回人工可见的 ERROR/HOLD，不自动 clone 并重跑 FULL；人工明确发起的新 FULL 是独立操作；
6. GitHub Check 只显示 `Native Review reused`、当前阶段、Restricted attempt 数、各阶段耗时和安全的 retryable/manual reason；
7. 发布前继续执行 base/head compare-and-swap，并用 envelope v3 包装 v10 result。

本仓库交付 CLI、schema、文档和可执行 contract fixture；公司 service 的真实仓库必须单独提交并验证。未经 Owner 再次确认，不部署、不重启 Jenkins/CI。

## 11. Module / Interface / Seam 设计

- `internal/session` 是深 Session Store Module：拥有 owner-only layout、原子文件、digest、detached checkout 和 session lock；上层不拼接私有路径。
- `internal/nativereview` 是 Recovery Transaction Module：拥有状态转移、failure classification、attempt ledger、resume admission、Provider lease 和 publication sequencing。
- `quality` 是 Result Contract Module：拥有 v10 execution accounting、结果验证和 candidate 过滤；不读取文件或执行 Provider。
- Provider Interface 保持只描述 Codex/Claude 调用协议；重试、checkpoint 和 service orchestration 不下沉到 adapter。
- resume seam 以 fake Provider 和持久临时 session 测试；同一 suite 同时覆盖冷重启、并发锁、篡改、幂等和第二次失败。

删除选择：不保留 v0.5.7 单一 `restricted-adjudication.*` 可覆盖路径，不增加私有 manual helper，不在一个 CI 进程中循环重试，不让 service 从 raw log 推断状态。

## 12. 实施计划与 RED 证据

1. 固定 v8/v9 与 envelope v1/v2 checksum，并添加 v10/checkpoint/attempt/envelope-v3 schema contract tests。
2. 先写 fake Provider 回归：首次 Restricted quota/timeout 后保持 `RESTRICTED_RETRYABLE` 且无 result。
3. 添加 resume success、Native invocation=0、finding order/identity、冷重启和一次性结果等价测试。
4. 添加 trusted diff、freeze、policy、schema、target commit、contract 篡改的 pre-provider rejection matrix。
5. 添加两个并发 resume、PUBLISHED 幂等、第二次失败 MANUAL_REQUIRED、无 P0/P1 单调用测试。
6. 为 Codex/Claude 检查恢复调用仍是 read-only 且注入同一 policy。
7. 实现 Session Store、checkpoint、attempt ledger、state machine、resume CLI、deadlines、heartbeat 和 metrics。
8. 更新 company CI service、envelope v3 fixture、Jenkins/README/插件/version surfaces。
9. 运行 `make verify-change`，提交 clean candidate 后运行 `VERIFY_COMPARE_REF=origin/main make verify-candidate`、完整 release-check、race、vet、qualification/live/mining suites 和四平台构建。
10. 对 exact clean candidate 执行 Code Quality 自审；只有 PASS 才可进入 PR/集成。release、tag、GitHub Release、公司 CI deploy/restart 均等待 Owner 再次确认。

RED 证据记录在 `reports/2026-08-19-v0.5.8-restricted-resume/red-evidence.md`，closeout 只填写真实执行结果。

## 13. 验收

- fake Provider 完整复现 Native 成功、Restricted 失败、进程冷重启、Restricted 独立恢复；恢复日志证明 Native invocation 为 0。
- 恢复结果与一次性成功结果在忽略 attempt/resume 审计字段后语义一致；共同发布字段逐字节一致。
- checkpoint、attempt 和 published result 形成完整 digest 链；任一受绑定内容篡改均在 Provider 前失败。
- Native attempt 不超过 1，Restricted 不超过 2，总 attempt 不超过 3。
- v8/v9 与 envelope v1/v2 checksum 和 validation 全部保持；v10/envelope-v3 准确表达真实 attempt。
- `go test ./...`、race、vet、格式、qualification/live/mining、Harness candidate/release gates 和仓库 `release-check` 全部通过。
- 最终报告给出根因、状态机、CLI/service 路径、attempt 计费、schema 兼容、测试证据、commit SHA，以及未执行的 release/deploy 步骤。
