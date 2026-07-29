# 第二类首个实验规格:spec-first 审查步骤(交 Codex 实施与执行)

日期:2026-07-28。基线:main@0b2b565(第一类四项改动后)。背景与基线数据:`reports/2026-07-28-route-b-historical-mining-eval.md`。

## 假设

产品 lane 在难度 3 契约类缺陷上 1:3 输给 builtin,失守样本(2e87ffc1、781ee3be)均为"实现违反仓库已声明契约/规范",而 builtin 自由通读 spec 后命中。假设:在 workflow 中加入"先通读契约再进 lens"步骤,可以补上这类漏报,且不丢失既有命中、不增加噪音。

## 改动(唯一变量,一次只改这一处)

`policy/v1.2/workflow.md` 第 3 步(diff-first review)前插入一步,同时压缩其他步骤措辞保持**净行数不增**:

> 在进入 lens 搜索前,先定位并通读与 changed_files 相关的权威契约:仓库 spec 文档、openapi/接口定义、协议与配置约定、README 中的行为承诺。将本次变更与这些已声明契约逐一对照,契约违反本身就是候选发现(通常归 COR-001/CHG-001)。

不改 `review-lens.md`、不改 rubric、不改 Go 代码、不升 policy 版本号(实验分支性质,结论确认后再定版)。改动放在独立分支 `exp/spec-first`,不合入 main、不 push。

## 实验协议

### 回归集(10 个目标,SHA 与已知缺陷描述见 `reports/2026-07-28-route-b-historical-mining-eval/evidence/targets.json`)

- 错位组(4):2e87ffc1、781ee3be、b21dbb39(builtin 命中/product 漏)、fd679bef(product 命中,验证不丢失)
- 双漏组(4):e23f25e1、8f85af1e、6349a866、d28dd36f
- 对照组(2):da79c064、b7f64515(product 已命中,验证无回归)

### 每目标执行(product lane only;builtin 基线沿用今日报告,不重跑)

1. 隔离克隆(修复提交不可见):
   ```sh
   git clone --shared --no-checkout /Users/chris/AiProject/<repo> <workdir>/repo
   git -C <workdir>/repo checkout --detach <introducing_commit>
   git -C <workdir>/repo for-each-ref --format='%(refname)' | while read r; do git -C <workdir>/repo update-ref -d "$r"; done
   git -C <workdir>/repo remote remove origin
   ```
2. 在 `exp/spec-first` 分支构建 `quality-review`(spec-first workflow 已 embed)。
3. `codex exec -s workspace-write -C <workdir>/repo --ephemeral`,prompt 固定为:
   ```
   你在一个历史回放评审环境中。执行以下流程,不要偏离:
   1. 运行:<QR> prepare --host codex --base <introducing_commit 的第一父提交> --target <introducing_commit> --diff-reason historical-replay,解析其 JSON 输出。
   2. 严格按 workflow_path 指向的文件执行审查,把主审查 JSON 写到 main_review_path。
   3. 运行:<QR> finalize --session <session_dir>;若返回 REREVIEW_REQUIRED,按 rereview_scope 对未覆盖维度做反证式复审,写 next_review_path 后再次 finalize。
   4. 最后完整打印 review-result.json 的内容。
   ```

### 裁决(Codex 自判,供人工抽查)

对照 `targets.json` 中逐条 `defects[].defect`(挖掘期冻结的缺陷描述),给每条已知缺陷标 `hit` / `miss` + 一行依据(引用 finding ID 与 code_location)。同时记录每目标发现总数,与基线(报告中的 product 列)对比。

## 通过标准

1. 错位组 2e87ffc1、781ee3be、b21dbb39 中**至少新命中 2 条**;
2. fd679bef、对照组 da79c064、b7f64515(SEC-003)原有命中**零丢失**;
3. 单目标发现数不出现无依据膨胀(新增发现必须仍满足五条件门槛;>6 条时逐条说明)。

双漏组(难度 4 缺失型)命中不作为通过条件,如有新命中单独标注——那是超预期信号。

## 产出

`reports/2026-07-28-spec-first-experiment/`:
- `results.md`:逐目标 hit/miss 表(新旧对照)、通过标准逐条判定、workflow.md 的完整 diff;
- `evidence/`:10 份 review-result.json 原件。

不修改今日基线报告与 evidence;不 push、不 tag、不动 main。
