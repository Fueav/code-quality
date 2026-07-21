# 公司 AI 代码检查 Rubric V1.1 文档同步实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将已批准的 Rubric V1.1 判定模型、机器合同和资源约束同步到 Rubric HTML、公司总方案 Markdown 与总览 HTML，并确保三份文档保持试点状态且不存在旧阻断公式残留。

**Architecture:** 以 V1.1 优化设计稿为唯一变更依据，先修改内容最完整的 Rubric HTML，再同步总方案 Markdown 和总览 HTML。最后用文本抽取和搜索完成跨文档一致性验收；不实现 CI、Skill、JSON Schema 校验器或编排器。

**Tech Stack:** Markdown、静态 HTML、内嵌 CSS、Shell 文本检查、Python 标准库 HTML 解码/文本抽取。

## Global Constraints

- 四个维度和 20 条规则总量保持不变。
- V1.1 继续标注为团队评审/试点稿，不得描述为固定公司标准。
- 自动阻断完整条件为 `S3 + T3 + E2/E3 + 本次变更归因 + 具体触发条件 + 完整因果链 + 非风格偏好`。
- `S3 + T2 + E2/E3` 必须为 `MANUAL_REVIEW`。
- 基线不明确、强制字段缺失或检查未可靠完成时结果为 `INCOMPLETE`，不得视为 `PASS`。
- `CHG-001` 是包含公共协议/API、配置语义、持久化身份与所有权三个强制子类的兼容性规则族；`CHG-002` 保持迁移与滚动发布安全。
- 依赖外部协议的结论只有具备可核验标准、契约、框架行为或测试依据时才能达到 `E2`。
- 默认 Agent 总数不超过 3；没有候选时不得启动验证 Agent；超限需要 CI 配置或人工显式允许并记录原因。
- 不增加质量总分、风格门禁、全面覆盖率判定或新的 CI 实现。
- 三份文档必须使用完全一致的等级名、状态名和阻断公式。

---

### Task 1: 更新 Rubric HTML 为 V1.1 权威内容

**Files:**
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-v1-rubric.html:7-1691`
- Reference: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-15-company-ai-code-review-v1.1-optimization-design.md`

**Interfaces:**
- Consumes: V1.1 优化设计中的判定模型、20 条规则结构、单条发现合同、报告级合同和资源限制。
- Produces: 面向团队评审的完整 V1.1 Rubric；Task 2 和 Task 3 从其术语与公式同步摘要。

- [ ] **Step 1: 建立修改前内容断言**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
p = Path('/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-v1-rubric.html')
text = unescape(p.read_text())
assert '公司 AI 代码检查底线规则 V1.0' in text
assert 'I3 且 E2/E3' in text
assert '20 条高影响规则' in text
assert 'DES-001' in text and 'CHG-002' in text
print('PASS rubric-v1 baseline')
PY
```

预期：输出 `PASS rubric-v1 baseline`。

- [ ] **Step 2: 更新版本、摘要卡片和目录**

将标题、页脚和元信息从 V1.0 更新为 V1.1，状态保持“团队评审中/试点稿”。摘要卡片必须改为：

```text
检查范围：4 个底线维度
规则规模：20 条底线规则
阻断门槛：S3 + T3 + E2/E3
默认资源：最多 3 个 Agent
```

目录增加“审查基线”“机器合同”“验证矩阵”，保留规则、判定、Agent 工作流和报告章节。

- [ ] **Step 3: 用双轴模型替换旧影响等级**

在判定章节加入两张表：

```text
后果严重度：S3 / S2 / S1
触发确定性：T3 / T2 / T1
证据等级：E3 / E2 / E1
```

明确：

```text
S3 + T2 + E2/E3 = MANUAL_REVIEW
S3 + T3 + E1 = MANUAL_REVIEW
S2 不得自动阻断
```

- [ ] **Step 4: 更新唯一阻断公式与结果状态**

使用以下完整公式：

```text
BLOCK =
    severity == S3
    AND trigger_confidence == T3
    AND evidence_level IN (E2, E3)
    AND introduced_or_worsened_by_change == true
    AND trigger_condition_is_concrete == true
    AND causal_chain_is_complete == true
    AND finding_is_not_style_preference == true
```

结果状态必须包含 `PASS`、`MANUAL_REVIEW`、`BLOCK`、`INCOMPLETE`，并说明总体优先级：

```text
INCOMPLETE > BLOCK > MANUAL_REVIEW > PASS
```

- [ ] **Step 5: 保持 20 条规则并重写 CHG-001**

D1、D2、D3 和 SEC-001～SEC-003 保持原有规则边界。将 `CHG-001` 的规则文字改为：

```text
不得在未声明和未处理兼容性的情况下破坏现有调用方、已有配置或稳定身份与所有权映射。
```

在同一规则单元中强制列出三个子类：

```text
A. 公共协议与接口兼容性
B. 配置语义兼容性
C. 持久化身份与所有权兼容性
```

其中 C 必须明确覆盖用户、租户、资源所有权、幂等、缓存和外部账户稳定标识的派生方式。`CHG-002` 继续负责数据迁移和新旧版本共存。

- [ ] **Step 6: 增加协议证据和审查基线章节**

加入协议证据边界：只凭模型记忆最高 `E1`；仓库契约、官方标准、框架行为或既有测试可支持 `E2`；最小复现或实际测试可支持 `E3`。

加入报告级基线字段：

```yaml
repository:
target_branch:
base_commit:
target_commit:
diff_selection_reason:
changed_files:
affected_entries:
```

明确基线不完整时不得 `BLOCK`。

- [ ] **Step 7: 更新单条发现和报告级机器合同**

单条发现展示完整字段：

```yaml
rule_id:
verdict:
severity:
trigger_confidence:
evidence_level:
introduced_or_worsened_by_change:
code_locations:
affected_call_path:
trigger_condition:
causal_chain:
production_impact:
verification_performed:
minimal_fix:
uncertainties:
```

报告级内容加入激活规则族、未激活原因、资源使用、失败重试、未检查范围和总体 `INCOMPLETE` 状态。

- [ ] **Step 8: 更新 Agent 工作流与硬资源限制**

工作流固定为：主 Agent 确认基线和真实路径 → 只激活相关规则 → 形成候选并去重 → 每个候选最多一个验证 Agent → 普通程序计算 verdict。

加入硬限制：

```text
默认总 Agent 数不超过 3（包括主 Agent）
没有候选时不得启动验证 Agent
不得按规则、文件或角度一对一扇出
超限必须显式批准并记录计划数量、实际数量和原因
```

- [ ] **Step 9: 增加规则验证矩阵**

明确每条规则自动阻断前至少有：一个应命中正例、一个不得阻断反例、一个应人工复核的证据不足案例；严重案例重复运行三次并记录稳定性、人工结论、Agent 数、token 与耗时。

- [ ] **Step 10: 验证 Rubric HTML 内容**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
p = Path('/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-v1-rubric.html')
text = unescape(p.read_text())
required = [
    'V1.1', '20 条底线规则', 'S3', 'T3', 'E2', 'E3',
    'INCOMPLETE', 'trigger_confidence', 'diff_selection_reason',
    '持久化身份与所有权兼容性', '默认总 Agent 数不超过 3',
    'MANUAL_REVIEW',
]
for item in required:
    assert item in text, item
assert 'I3 且 E2/E3' not in text
assert text.count('DES-001') >= 1 and text.count('CHG-002') >= 1
print('PASS rubric-v1.1 content')
PY
```

预期：输出 `PASS rubric-v1.1 content`。

### Task 2: 同步公司总方案 Markdown

**Files:**
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md:95-145`
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md:166-197`
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md:233-251`
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md:285-324`
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md:354-374`

**Interfaces:**
- Consumes: Task 1 的 V1.1 术语、阻断公式和报告字段。
- Produces: 与 Rubric 一致的公司级架构与试点方案。

- [ ] **Step 1: 更新检查输入和真实路径工作流**

在第 5～6 节明确 Prompt/CI 必须提供 `base_commit`、`target_commit`、`target_branch` 和 `diff_selection_reason`。说明基线不明确时不能判定本次变更归因，也不能 `BLOCK`。

在检查步骤中加入“只激活与改动相关的规则族”和“相同根因先去重再验证”。

- [ ] **Step 2: 重写第 8 节判定模型**

将原本六条自然语言阻断条件替换为：

- `S3` 后果严重度；
- `T3` 触发确定性；
- `E2/E3` 证据；
- 本次变更归因；
- 具体触发条件；
- 完整因果链；
- 非风格偏好。

增加 `INCOMPLETE`，并明确 `S3 + T2` 为人工确认。

- [ ] **Step 3: 更新第 11 节报告合同**

报告摘要加入基线字段、规则/schema 版本和资源使用。每条问题加入 `severity`、`trigger_confidence`、`evidence_level`、`affected_call_path`、`verification_performed` 和 `uncertainties`。

明确普通程序验证 schema 并计算最终 verdict，模型不能仅凭文本直接阻断。

- [ ] **Step 4: 更新执行资源限制**

在专用检查机器或 Codex 检查章节加入：默认总 Agent 数不超过 3；候选最多一个验证 Agent；无候选不启动验证；超限需显式允许并记录。

- [ ] **Step 5: 更新试点样本和启用条件**

第 14 节要求每条拟自动阻断规则具备正例、反例和证据不足案例。保留现有 30 个历史改动和 80% 指标，同时增加按规则记录触发等级稳定性、重复根因、平均 Agent 数、token 与耗时。

- [ ] **Step 6: 更新已确定与待决定事项**

第 17 节记录 V1.1 已确定：双轴判定、机器合同、默认 3 Agent 上限、超限审批和 report-only 校准。待决定事项保留专用机器全局并发、月度费用和各项目启用时间；不要把单任务 Agent 上限重新列为未决定。

- [ ] **Step 7: 验证总方案 Markdown**

运行：

```bash
python3 - <<'PY'
from pathlib import Path
p = Path('/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md')
text = p.read_text()
required = [
    'base_commit', 'target_commit', 'diff_selection_reason',
    'S3', 'T3', 'E2', 'E3', 'INCOMPLETE',
    'trigger_confidence', '默认总 Agent 数不超过 3',
    '正例', '反例', '证据不足',
]
for item in required:
    assert item in text, item
print('PASS overview markdown v1.1 sync')
PY
```

预期：输出 `PASS overview markdown v1.1 sync`。

### Task 3: 同步总览 HTML

**Files:**
- Modify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-overview.html`

**Interfaces:**
- Consumes: Task 1 的权威术语和 Task 2 的公司流程说明。
- Produces: 面向管理者、开发者和评审人的一致摘要页面。

- [ ] **Step 1: 定位总览中的旧判定与报告内容**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
p = Path('/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-overview.html')
text = unescape(p.read_text())
for needle in ['严重', '证据', '报告', 'Agent', '试点']:
    print(needle, text.count(needle))
PY
```

记录需要同步的摘要卡片、流程、合并规则、报告和试点段落。

- [ ] **Step 2: 更新摘要与决策流程**

摘要必须明确：

```text
S3：后果严重
T3：当前目标确定可触发
E2/E3：代码证实或已验证
本次变更引入或加重
```

同时展示：

```text
S3 + T2 → MANUAL_REVIEW
检查失败或报告不完整 → INCOMPLETE，不得当作 PASS
```

- [ ] **Step 3: 更新报告与资源约束摘要**

加入 base/target commit、触发确定性、证据等级和结构化报告合同。明确默认总 Agent 数不超过 3，超限需批准；不要展示实现层 token 细节之外的内部推理。

- [ ] **Step 4: 更新试点与规则验证摘要**

说明每条自动阻断规则需通过正例、反例和证据不足案例，并继续采用 report-only 阶段和人工标注。

- [ ] **Step 5: 验证总览 HTML**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
p = Path('/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-overview.html')
text = unescape(p.read_text())
required = [
    'S3', 'T3', 'E2/E3', 'MANUAL_REVIEW', 'INCOMPLETE',
    'base_commit', 'target_commit', '默认总 Agent 数不超过 3',
    '正例', '反例', '证据不足',
]
for item in required:
    assert item in text, item
assert 'I3 + E2/E3' not in text
print('PASS overview html v1.1 sync')
PY
```

预期：输出 `PASS overview html v1.1 sync`。

### Task 4: 跨文档一致性验收

**Files:**
- Verify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-v1-rubric.html`
- Verify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-design.md`
- Verify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-14-company-ai-code-review-overview.html`
- Verify: `/Users/chris/AiProject/company-ai-code-review-design/2026-07-15-company-ai-code-review-v1.1-optimization-design.md`

**Interfaces:**
- Consumes: Tasks 1～3 完成后的三个同步文档。
- Produces: 可交付团队评审的 V1.1 文档集和一致性证据。

- [ ] **Step 1: 检查旧公式残留**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
root = Path('/Users/chris/AiProject/company-ai-code-review-design')
paths = [
    root / '2026-07-14-company-ai-code-review-v1-rubric.html',
    root / '2026-07-14-company-ai-code-review-design.md',
    root / '2026-07-14-company-ai-code-review-overview.html',
]
for p in paths:
    text = unescape(p.read_text())
    forbidden = ['I3 且 E2/E3', 'I3 + E2/E3']
    hits = [s for s in forbidden if s in text]
    assert not hits, (p.name, hits)
print('PASS no obsolete blocking formula')
PY
```

预期：输出 `PASS no obsolete blocking formula`。

- [ ] **Step 2: 检查核心合同在三份文档中一致存在**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
root = Path('/Users/chris/AiProject/company-ai-code-review-design')
paths = [
    root / '2026-07-14-company-ai-code-review-v1-rubric.html',
    root / '2026-07-14-company-ai-code-review-design.md',
    root / '2026-07-14-company-ai-code-review-overview.html',
]
required = [
    'S3', 'T3', 'E2', 'E3', 'MANUAL_REVIEW', 'INCOMPLETE',
    'base_commit', 'target_commit', 'diff_selection_reason',
    '默认总 Agent 数不超过 3',
]
for p in paths:
    text = unescape(p.read_text())
    missing = [s for s in required if s not in text]
    assert not missing, (p.name, missing)
print('PASS shared contract present')
PY
```

预期：输出 `PASS shared contract present`。

- [ ] **Step 3: 检查试点状态与非目标**

运行：

```bash
python3 - <<'PY'
from html import unescape
from pathlib import Path
root = Path('/Users/chris/AiProject/company-ai-code-review-design')
paths = [
    root / '2026-07-14-company-ai-code-review-v1-rubric.html',
    root / '2026-07-14-company-ai-code-review-design.md',
    root / '2026-07-14-company-ai-code-review-overview.html',
]
for p in paths:
    text = unescape(p.read_text())
    assert '试点' in text or '评审' in text, p.name
    assert '质量总分' not in text or '不' in text, p.name
print('PASS pilot status retained')
PY
```

预期：输出 `PASS pilot status retained`。

- [ ] **Step 4: 人工抽样复核四个关键场景**

逐一确认三份文档对以下场景给出相同结果：

```text
S3 + T3 + E2 + 本次引入 + 完整因果链 → BLOCK
S3 + T2 + E3 → MANUAL_REVIEW
S2 + T3 + E3 → MANUAL_REVIEW
报告字段缺失或基线不明确 → INCOMPLETE
```

预期：不存在任一文档把后三种场景描述为 `BLOCK` 或 `PASS`。

- [ ] **Step 5: 生成变更摘要供团队评审**

最终交付摘要必须列出：

```text
1. 为什么从 I3 单轴改成 S/T 双轴
2. CHG-001 如何覆盖本次实战发现
3. 为什么默认限制为 3 个 Agent
4. 哪些内容仍处于试点和待实现状态
5. 三份文档一致性检查结果
```

不提交或发布文档，除非用户另行要求。
