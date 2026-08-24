# code-quality v0.5.9 可观测审查生命周期规格

状态：Owner 已在本任务中批准按本规格实现；push、PR、release、公司 Service 部署和服务重启仍需再次确认。

工作流：`HARNESS-SPEC-FIRST-FEATURE`。

产品基线：`v0.5.8` / `1d0557960ef769272addb579be39aa1949395993`。

## 1. 问题与目标

v0.5.8 已为运行中的 Native 和 Restricted Provider 进程每 45 秒向 stderr 输出安全文本 heartbeat，并记录完成后的阶段 metrics。该输出只证明进程仍在等待，缺少可版本化的开始、冻结、重试和终态事件；公司 Service 只能解析自由文本，无法可靠驱动五分钟状态播报、跨进程去重和阶段耗时诊断。

v0.5.9 把进度输出升级为旁路、只读、Provider-neutral 的生命周期事件契约。它缩短“不可观测的黑盒时间”，不宣称降低模型 wall-clock，不改变 Native/Restricted prompt、模型、reasoning effort、生产下限、Provider 调用上限、结果 schema 或发布结论。

## 2. 进度事件合同

新增 `schemas/review-progress-event-v1.schema.json`。每条事件包含：

- `schema_version=1`；
- 当前 CLI invocation 内从 1 开始严格递增的 `sequence`；
- `event`、`stage`、RFC3339Nano UTC `occurred_at` 和非负 `elapsed_ms`；
- 计划可用后附带 `review_key` 与 `review_scope=FULL|INCREMENTAL`；
- Provider attempt 事件附带 `attempt`；
- heartbeat 可附带安全的 `last_activity_at`，只表示 stdout/stderr 最后一次成功写入时间，不解释 Provider 语义。

允许事件为：

```text
PLAN_STARTED
PLAN_READY
FULL_REQUIRED
RECOVERY_STARTED
RECOVERY_READY
NATIVE_STARTED
NATIVE_HEARTBEAT
NATIVE_FREEZING
NATIVE_FROZEN
RESTRICTED_STARTED
RESTRICTED_HEARTBEAT
RESTRICTED_FREEZING
RESTRICTED_COMPLETED
RESTRICTED_RETRYABLE
FINALIZING
PUBLISHED
MANUAL_REQUIRED
FAILED
```

允许阶段为 `PLAN`、`RECOVERY`、`NATIVE`、`NATIVE_FREEZE`、`RESTRICTED`、`RESTRICTED_FREEZE`、`FINALIZE`。事件是一次 CLI invocation 的观测流，不进入 `review_key`、`contract_digest`、checkpoint/session digest 或 PASS/BLOCK 判定。

## 3. CLI 与兼容性

`run-codex`、`run-claude` 和 `resume-restricted` 新增：

```text
--progress-format text|jsonl
```

- 默认 `text`；v0.5.8 heartbeat 文本格式保持兼容，同时增加安全的阶段转移行。
- `jsonl` 将 schema-v1 事件逐行写到 stderr；stdout 继续只承载最终 result、plan 或 session status。
- stderr 的非事件诊断仍可能是普通文本；Service 只消费能按 schema-v1 解码的 JSON 对象，其余行作为诊断日志。
- 非法 format 在 Provider 调用前以 CLI usage error 拒绝。
- progress writer 缺失、关闭或写失败不改变审查状态、Provider 调用、checkpoint、退出码或最终结果。

v0.5.8 的 result v10、envelope v3、checkpoint/attempt 状态机、默认 Native 45 分钟和 Restricted 15 分钟 deadline、默认 heartbeat 45 秒及只读审查语义保持不变。版本升级继续遵守当前 session 的同版本恢复规则；在途 v0.5.8 session 使用 v0.5.8 binary 恢复。

## 4. 生命周期

新审查的主路径：

```text
PLAN_STARTED -> PLAN_READY
-> NATIVE_STARTED -> NATIVE_HEARTBEAT* -> NATIVE_FREEZING -> NATIVE_FROZEN
-> [RESTRICTED_STARTED -> RESTRICTED_HEARTBEAT* -> RESTRICTED_FREEZING -> RESTRICTED_COMPLETED]
-> FINALIZING -> PUBLISHED
```

没有冻结 P0/P1 时不产生 Restricted 事件。首次 Restricted 可重试失败产生 `RESTRICTED_RETRYABLE`，不产生 `PUBLISHED`。正式恢复先产生 `RECOVERY_STARTED -> RECOVERY_READY`，只运行下一次 Restricted attempt。第二次失败产生 `MANUAL_REQUIRED`。异常路径每次 invocation 最多产生一个 `FAILED`。

`FULL_REQUIRED` 与 `MANUAL_REQUIRED` 不是 PASS/BLOCK/ERROR result；进度事件不得把它们伪装成审查结论。

## 5. Module / Interface / Seam

- `internal/nativereview` 增加深 Progress Module。其小 interface 接受生命周期事实，内部拥有 sequence、时间、阶段耗时、字段最小化与编码。
- Text adapter 保持人类日志兼容；JSONL adapter 实现机器消费。事务和 Provider adapter 不自行拼接 JSON。
- 进程 capture 只记录输出活动时间，不解析 transcript、finding、token、路径或 Provider 原文。
- Company Service 负责五分钟调度、飞书/GitHub 状态更新、持久化和跨进程幂等；CLI 不发送外部消息。

## 6. 安全与隐私

事件不得包含仓库路径、session/evidence 路径、diff、prompt、finding、Provider 原文、token、凭据、环境变量或失败详情。`review_key`、scope、stage、attempt、时间和活动时间是允许公开给受控 CI 的安全字段。

`last_activity_at` 只能用于描述“最近有输出”；长时间无输出不等同于卡死，不触发自动终止或自动重跑。

## 7. 测试与验收

1. Schema contract test 固定字段、枚举和 `additionalProperties=false`。
2. Progress Module 测试 sequence、UTC 时间、阶段 elapsed、scope/identity 绑定、text heartbeat 兼容和 JSONL 解码。
3. fake Provider 测试无 Restricted、Restricted success、retryable、resume、manual、timeout/失败及 exactly-one terminal event。
4. Codex/Claude 共用同一 capture seam；输出活动只影响 `last_activity_at`。
5. progress writer 故障不会改变审查结果或退出码。
6. stdout 和 result v10/envelope v3 保持兼容；v8/v9 与 envelope v1/v2 checksum 不变。
7. 运行定向测试、`make verify-change`；形成 clean candidate 后再按 Owner 授权决定 commit、push、PR 和 release。

## 8. 非目标

- 不降低 reasoning effort，不更换模型，不并行或提前终止审查。
- 不修改 Native 或 Restricted prompt、生产下限和 finding 发布规则。
- 不把 heartbeat 解释成语义百分比、ETA 或“模型健康”。
- 不在 Code Quality 内实现五分钟定时消息、飞书卡片或公司 Service 状态库。
- 不宣称 v0.5.9 降低总审查耗时；真实提速必须依据本事件流和既有 metrics 的后续证据另行设计。
