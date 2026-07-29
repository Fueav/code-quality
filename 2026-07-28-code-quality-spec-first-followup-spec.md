# spec-first 实验追加规格(交 Codex)

日期:2026-07-28。前置:`2026-07-28-code-quality-spec-first-experiment-spec.md` 判 FAIL;裁决复核发现 fd679bef 是无效运行(非干净回归)、d28dd36f 无法排除运行方差。本追加分三件事,按序执行。

## A. 引擎修复(第一类性质,不需评测背书)

证据:fd679bef 运行中模型写坏 `inspected_context[7]`,finalize 直接终局 `INCOMPLETE`、findings 清零、清理 worktree;且 `quality-review validate` 拒绝 finalize 自产的该 INCOMPLETE 工件("INCOMPLETE result must not contain partial review output")。

1. finalize 遇到 main review / rereview **可修复的校验错误**(JSON 字段级非法,而非文件缺失)时,不再立即写终局 INCOMPLETE:返回新状态 `REVIEW_INVALID`,带 `validation_errors` 与需重写的路径,保留会话与 checkout;模型修复后再次 finalize。最多允许一次修复,第二次仍非法才落终局 INCOMPLETE。
2. 修复 finalize 产出的 INCOMPLETE 工件无法通过自家 `validate` 的不一致(二选一:INCOMPLETE 工件不含被拒字段,或 validate 放行 finalize 的合法 INCOMPLETE 形态;取语义上正确的一侧并写测试锁定)。
3. `policy/**/workflow.md` 与 SKILL.md 同步一句"收到 REVIEW_INVALID 按 validation_errors 修复后重新 finalize";净行数不增。
4. RED→GREEN 测试:字段级非法 → REVIEW_INVALID → 修复 → COMPLETE;二次非法 → INCOMPLETE;INCOMPLETE 工件通过 validate。

## B. 方差测量(裁决争议样本,先于任何新实验)

目的:确定单次运行噪声底,裁决 spec-first 的 FAIL 里有多少是方差。

- `fd679bef`:exp/spec-first 分支重跑 ×3(A 完成后跑,坏输出可自修复)。
- `d28dd36f`:main 基线 ×3 + exp/spec-first ×3。
- 隔离克隆与 lane prompt 同前一规格;另外**禁用宿主 memory**(为 codex exec 子进程使用隔离的 `CODEX_HOME` 或对应关闭 memories 的 config 开关,报告中注明实际用的机制),消除上次报告提到的污染通道。
- 产出:每目标每轮对冻结缺陷的 hit/miss 矩阵。判读规则:
  - fd679bef ≥2/3 命中 → 上次"丢失"判定撤销,记方差/坏运行;
  - d28dd36f Bearer 在 main ×3 中命中 <3 次 → 该缺陷本身高方差,不能作为 spec-first 回归证据;
  - 结合 781ee3be 已确认的新命中,重新给 spec-first 实验下结论(通过标准沿用前一规格,但只计有效运行)。

## C. 结论落盘

`reports/2026-07-28-spec-first-experiment/results.md` 追加"复核与方差"一节:fd679bef 无效运行说明、B 的矩阵、修订后结论(spec-first 进 main / 再改一版 / 放弃,三选一给出依据)。不改已归档原件。
