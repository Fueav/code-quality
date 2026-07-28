# Code Quality 第一类优化实施报告

日期：2026-07-28。实施基线：`main @ 656be6c`（`v0.2.0`）。实现提交范围：`8be510b..0b2b565`。

## 1. 实施结果

本轮按规格的 1 → 2 → 3 → 4 顺序完成，四项分别独立提交：

1. `8be510b fix(prepare): add shared clone fallback (change 1)`
2. `6c3f815 feat(policy): remove finding cap in v1.2 (change 2)`
3. `c75f17c feat(review): rereview uncovered dimensions (change 3)`
4. `0b2b565 perf(review): reduce fixed input overhead (change 4)`

### 改动 1：prepare shared-clone 回退

- `Prepare` 仍优先执行 detached worktree；失败后改用 `git clone --shared --no-checkout` 与 detached checkout。
- `Prepared` 和 `session-metadata.json` 新增 `checkout_mode`，值限定为 `worktree` 或 `clone`；metadata schema 仍为 1。
- prepare 失败 defer 与 finalize 都按模式清理：worktree 继续调用 `git worktree remove --force`，clone 直接删除 checkout。
- clone 删除前验证 `RepositoryDir` 严格位于 `SessionDir` 之下；越界路径返回错误且不删除。
- 只读 `.git` 集成测试证明 fallback checkout 命中 target commit，主仓库 Git metadata 条目不变；finalize 后只删除 clone，保留 session 和两份报告。

### 改动 2：取消三条发现上限

- review lens、rubric 5.1、rubric 6.1 与 workflow 统一为：每个满足门槛的独立根因单独报告、按影响排序、不设固定上限；只合并同根因表现，不捆绑不同根因。
- policy 从 `1.1.1` 升为 `1.2.0`，目录迁移至 `policy/v1.2/`，manifest、embed、当前运行代码、Skill、评测 manifest 与测试引用同步。
- 五条不同 rule ID、覆盖四维的独立发现通过 finalize，五条均进入最终 JSON。
- 早期 V1.1 设计稿与旧实施报告中的版本描述作为历史事实保留；当前运行面不存在 `policy/v1.1` 或 `V1.1` 引用。

### 改动 3：对未激活维度复审

- `rereview_scope` 由 policy manifest 的 rule→dimension 映射与第一轮有效 findings 计算。
- 第一轮只有 D2 finding 时返回 `REREVIEW_REQUIRED` 与 `rereview_scope=["D1","D3","D4"]`；第二轮新增 D3 finding 后合并为两条最终 finding，`completed_review_rounds=2`、`retry_count=1`。
- 第一轮四维都有 finding 时一轮 `COMPLETE`，`completed_review_rounds=1`、`retry_count=0`。
- 第一轮零 finding 时 scope 自然为 D1–D4；两轮零 finding 的裁决原因和最多两轮状态机保持不变。
- Skill 与 workflow 明确第二轮只对 scope 内维度做反证检查、只报新增 finding、允许为空且不重复第一轮维度。

成本边界：复审从固定全量四维收窄为第一轮未激活维度；每次最多增加一轮，增量范围由缺失维度集合限制，并由改动 4 的固定输入省量部分抵消。

### 改动 4：压缩固定输入成本

- trusted diff 从 `--unified=40` 收敛为 `--unified=6`。固定 100 行 fixture 中，产物小于 unified=40 版本的一半，并验证正文只保留变更前后各 6 行。
- `Prepared` 新增 `evidence_present`；没有 accepted/rejected Harness evidence 时为 `false`，workflow 要求此时跳过 `evidence-context.json`。
- workflow 内联最小合法 review JSON，`model-review.schema.json` 改为不确定时再读；schema 文件仍写入 session，finalize 校验未变。
- 防腐测试直接从 workflow 提取内联 JSON，并用生产 `quality.DecodeModelReview` 成功解析。
- workflow 仍为 10 行，Skill 仍为 15 行，prompt/文档净行数未增加。

## 2. RED → GREEN 证据

| 改动 | RED | GREEN |
| --- | --- | --- |
| 1 | `go test ./cmd/quality-review ./internal/session` 因 `Prepared.CheckoutMode`、`CheckoutModeClone`、`cleanupCheckout` 未定义而编译失败 | shared clone、metadata、finalize 清理与越界拒绝测试通过 |
| 2 | bundle 测试报告 policy 仍为 `1.1.1`/`policy/v1.1`，lens 缺少“不设固定数量上限”，workflow 缺少 `no fixed limit` | policy 1.2.0、embed、五 findings 与完整 Go 测试通过 |
| 3 | CLI 测试因 `Finalized.RereviewScope` 未定义而编译失败，plugin contract 报 Skill 缺少 `rereview_scope` | D2-only、全四维、零 findings、两轮合并与既有复审测试通过 |
| 4 | CLI 测试因 `Prepared.EvidencePresent` 未定义而编译失败，bundle 测试报 workflow 缺少最小示例 | unified=6、空 evidence 与文档示例解析测试通过 |

## 3. 逐文件增删行数

统计口径：`git diff --numstat --find-renames=20% 656be6c..0b2b565`。

| 文件 | + | - | 说明 |
| --- | ---: | ---: | --- |
| `2026-07-14-company-ai-code-review-v1-rubric.md` | 1 | 1 | 历史稿的“当前权威规则”指针更新至 v1.2 |
| `bundle.go` | 4 | 4 | embed/read 路径迁移到 v1.2 |
| `bundle_test.go` | 27 | 6 | policy、workflow 与内联示例防腐测试 |
| `cmd/quality-review/main_test.go` | 252 | 1 | 四项端到端契约测试与 fixtures |
| `evals/cases.json` | 1 | 1 | policy version 同步 |
| `internal/eval/eval.go` | 1 | 1 | 当前 policy 文案同步 |
| `internal/eval/eval_test.go` | 1 | 1 | 测试 policy 同步 |
| `internal/eval/replay_test.go` | 6 | 6 | replay 测试 policy 同步 |
| `internal/session/finalize.go` | 64 | 34 | 模式清理、rereview scope 与状态分支 |
| `internal/session/session.go` | 100 | 22 | shared clone、metadata、evidence flag、unified=6 |
| `internal/session/session_test.go` | 11 | 0 | clone 越界清理防护 |
| `plugin_contract_test.go` | 2 | 0 | Skill shared-clone/rereview 契约 |
| `plugins/code-quality/skills/code-quality/SKILL.md` | 3 | 3 | checkout 与复审语义，净行数 0 |
| `policy/manifest.json` | 2 | 2 | policy 1.2.0 与 rubric 路径 |
| `policy/v1.1/review-lens.md → policy/v1.2/review-lens.md` | 2 | 2 | 版本与发现数量语义 |
| `policy/v1.1/rubric.md → policy/v1.2/rubric.md` | 6 | 6 | 版本、日期、5.1 与 6.1 |
| `policy/v1.1/workflow.md → policy/v1.2/workflow.md` | 4 | 4 | 数量、复审与读取成本，净行数 0 |
| `quality/adjudicate_test.go` | 5 | 5 | policy 1.2 测试合同 |
| `quality/render.go` | 1 | 1 | 报告 policy 文案同步 |
| `quality/validate.go` | 1 | 1 | rule contract 文案同步 |
| `2026-07-28-code-quality-first-category-optimization-report.md` | 134 | 0 | 本实施、验证与审查报告 |

## 4. 最终验证

格式门禁：

```text
$ gofmt -l internal cmd quality *.go
<no output>
```

静态检查：

```text
$ go vet ./...
exit 0
```

非缓存 Go 测试：

```text
$ go test -count=1 ./...
exit 0
ok github.com/Fueav/code-quality
ok github.com/Fueav/code-quality/cmd/quality-review
ok github.com/Fueav/code-quality/internal/eval
ok github.com/Fueav/code-quality/internal/intake
ok github.com/Fueav/code-quality/internal/session
ok github.com/Fueav/code-quality/quality
```

Python 工具测试：

```text
$ python3 -m unittest discover -s pilot -p 'test_*.py'
Ran 17 tests in 6.179s
OK
```

补充门禁：`git diff --check 656be6c..0b2b565` 通过；当前运行面旧 policy 引用与三条 findings 上限精确文本 grep 无命中；Skill/workflow 行数与基线相等。

## 5. 规格对照代码审查

实施完成后按规格逐条复审了运行代码、policy/Skill、测试、四个提交与范围边界，未发现需要修复的正确性、契约或越界问题：

- prepare 首选/回退、target checkout、metadata、两种 cleanup 与越界保护均有对应代码和测试。
- findings 上限只改 policy 语义；schema 未添加数量限制，五条独立 finding 全量保留。
- rereview scope 三个状态分支、两轮合并与 retry 统计符合规格。
- unified=6、evidence skip、schema 按需读取与内联示例解析均有机械验收。
- `SkillVersion`、plugin descriptors 与 README 安装版本保持 `0.2.0`；未修改 lens 质量口径、compare/评测方法、多 session 采样，也未 push、tag、release 或部署。
- 用户已有未跟踪规格文件与 `reports/` 保持未修改、未纳入提交。

结论：四项改动准确实现规格，验收与代码审查通过，无阻塞项。
