# Jenkins 生产 CI 接入（v0.6.0）

适用于 GitHub 私有仓库、Jenkins Multibranch Pipeline 和专用 Linux Agent。审查单位是整个 PR 的 `merge-base → head`；Provider 使用 Agent 系统用户已有的 Codex 或 Claude Code 登录态。现有 `run-codex` / `run-claude` 入口通过共享协调器选择可信复用、只恢复 Restricted、一次合法 INCREMENTAL，或首次 FULL。

## 1. 准备 Agent

- 使用专用低权限系统用户、`code-quality` 标签和 1 个 executor，不保存与审查无关的凭据。
- 同一用户预装 `quality-review v0.6.0`、`bash`、`git`、`python3`、`curl`、`tar` 和一个已登录 Provider。安装升级使用发布资产校验与回滚流程，参见 [发布指南](release.md)。
- Agent 服务的 `PATH` 包含 CLI 与 Provider。不设置 `OPENAI_API_KEY` 或 `ANTHROPIC_API_KEY`，不把登录目录复制进 Workspace。
- 管理员预建 workspace 外 owner-only `0700` 持久目录，设置 `CODE_QUALITY_STATE_ROOT`。保持 PR 固定路由、持久 Git object store 和足够保留期；不能在每次构建清空目录。
- 管理员设置 `CODE_QUALITY_RUNNER_POLICY_VERSION` 和 `CODE_QUALITY_EXECUTION_FINGERPRINT`。后者绑定有效配置、工具权限、插件和环境；脚本再纳入实际 Provider `--version`。配置变化必须更新声明。未知声明不能复用。

```sh
command -v quality-review bash git python3
quality-review version          # 必须是 quality-review v0.6.0
codex login status              # Codex 二选一
claude auth status --json       # Claude Code 二选一
```

## 2. 配置 Jenkins

| 配置 | 值 |
| --- | --- |
| Jenkins 插件 | Pipeline、Multibranch Pipeline、GitHub Branch Source、SSH Agent |
| Agent label | `code-quality` |
| SCM 凭据 | 只读 SSH；示例 Credentials ID 为 `github-readonly` |
| PR Discovery | `The current pull request revision`；只发现同仓 PR，关闭 fork PR |
| Provider | `codex` 或 `claude`，一次只选一个 |
| Required check | 将该 Multibranch Job 的实际 GitHub status context 设为 required check |

审查 stage 在编译、测试等会产生文件的步骤之前运行。Jenkinsfile/Shared Library 必须来自受保护分支并受 CODEOWNERS 控制，不能直接运行未信任 PR 修改后的审查脚本。以下流程按 PR 串行排队；多个 Agent 共享同一 PR 时，调度层另需业务 CAS/分布式锁，文件锁不替代分布式所有权。

## 3. Jenkinsfile

将以下 stage 合入受保护 Pipeline；如用 Claude，改 `CODE_QUALITY_PROVIDER`。三个状态配置由管理员注入，不从 PR 文件读取。

```groovy
pipeline {
  agent none
  options {
    skipDefaultCheckout(true)
    disableConcurrentBuilds(abortPrevious: false)
    buildDiscarder(logRotator(numToKeepStr: '30'))
    timestamps()
  }
  environment { CODE_QUALITY_PROVIDER = 'codex' }
  stages {
    stage('Code Quality PR Review') {
      when { beforeAgent true; changeRequest() }
      agent { label 'code-quality' }
      options { timeout(time: 70, unit: 'MINUTES') }
      steps {
        checkout scm
        script {
          env.REVIEW_ROOT = "${pwd(tmp: true)}/code-quality-${env.BUILD_NUMBER}"
        }
        // 仅清理该 Job 导出的报告，不得删除业务文件或持久审查状态。
        sh 'rm -rf "$WORKSPACE/code-quality-artifacts"'
        sshagent(credentials: ['github-readonly']) {
          sh '''#!/usr/bin/env bash
set -euo pipefail
umask 077
test -n "${CHANGE_ID:-}"
test -n "${CHANGE_TARGET:-}"
test -z "${CHANGE_FORK:-}"
case "$CHANGE_ID" in *[!0-9]*|'') exit 2 ;; esac
git check-ref-format --branch "$CHANGE_TARGET" >/dev/null
mkdir -p "$REVIEW_ROOT"
git fetch --no-tags origin \
  "+refs/heads/${CHANGE_TARGET}:refs/remotes/origin/${CHANGE_TARGET}" \
  "+refs/pull/${CHANGE_ID}/head:refs/remotes/origin/pr/${CHANGE_ID}/head"
base_ref="refs/remotes/origin/${CHANGE_TARGET}"
head_ref="refs/remotes/origin/pr/${CHANGE_ID}/head"
git rev-parse "${base_ref}^{commit}" > "$REVIEW_ROOT/base.sha"
git rev-parse "${head_ref}^{commit}" > "$REVIEW_ROOT/head.sha"
'''
        }
        sh '''#!/usr/bin/env bash
set -euo pipefail
umask 077
test "$(quality-review version)" = 'quality-review v0.6.0'
case "${CODE_QUALITY_STATE_ROOT:-}" in /*) ;; *) exit 2 ;; esac
test -n "${CODE_QUALITY_RUNNER_POLICY_VERSION:-}"
test -n "${CODE_QUALITY_EXECUTION_FINGERPRINT:-}"
case "$CODE_QUALITY_PROVIDER" in
  codex) command=run-codex; model=gpt-5.6-sol ;;
  claude) command=run-claude; model=sonnet; export DISABLE_AUTOUPDATER=1 ;;
  *) exit 2 ;;
esac
export PROVIDER_VERSION
PROVIDER_VERSION=$("$CODE_QUALITY_PROVIDER" --version)
fingerprint=$(python3 -I -c 'import hashlib,json,os; print(hashlib.sha256(json.dumps([os.environ["PROVIDER_VERSION"],os.environ["CODE_QUALITY_EXECUTION_FINGERPRINT"]]).encode()).hexdigest())')
effort=max
review_args=(
  --repo "$WORKSPACE"
  --base-ref "refs/remotes/origin/${CHANGE_TARGET}"
  --head-ref "refs/remotes/origin/pr/${CHANGE_ID}/head"
  --diff-reason jenkins_github_pull_request
  --model "$model" --reasoning-effort "$effort"
  --execution-profile production-ci
  --state-root "$CODE_QUALITY_STATE_ROOT"
  --runner-policy-version "$CODE_QUALITY_RUNNER_POLICY_VERSION"
  --execution-fingerprint "$fingerprint"
  --total-timeout 65m --finalize-reserve 5m --progress-format jsonl
)
quality-review "$command" "${review_args[@]}" | tee "$REVIEW_ROOT/run.json"
'''
        // 只有审查退出0才到这里；失败或未知远端状态均不能发布成功。
        sshagent(credentials: ['github-readonly']) {
          sh '''#!/usr/bin/env bash
set -euo pipefail
git ls-remote --exit-code origin "refs/heads/${CHANGE_TARGET}" > "$REVIEW_ROOT/current-base.txt"
git ls-remote --exit-code origin "refs/pull/${CHANGE_ID}/head" > "$REVIEW_ROOT/current-head.txt"
python3 -I - <<'PY'
import os, pathlib
root = pathlib.Path(os.environ['REVIEW_ROOT'])
def current(name):
    lines = (root / name).read_text().splitlines()
    if len(lines) != 1:
        raise SystemExit('Ambiguous current PR revision; HOLD')
    return lines[0].split()[0]
if current('current-base.txt') != (root / 'base.sha').read_text().strip() or current('current-head.txt') != (root / 'head.sha').read_text().strip():
    raise SystemExit('SUPERSEDED: current PR base/head require new evidence; HOLD')
PY
'''
        }
      }
      post {
        always {
          sh '''#!/usr/bin/env bash
set -euo pipefail
umask 077
quality-review package-evidence \
  --run "$REVIEW_ROOT/run.json" \
  --evidence-root "$CODE_QUALITY_STATE_ROOT" \
  --destination "$WORKSPACE/code-quality-artifacts"
'''
          archiveArtifacts artifacts: 'code-quality-artifacts/*',
            allowEmptyArchive: false, fingerprint: true
        }
      }
    }
  }
}
```

总预算 65 分钟包含 Native 45 分钟、Restricted 15 分钟及 5 分钟预算余量；Jenkins 70 分钟留出归档时间。`finalize-reserve` 参与启动预算校验，队列与准备也消耗总 deadline，不保证收尾时仍有完整 5 分钟。这修复旧 30 分钟 job 提前终止的问题，不是性能提升承诺。Provider 不会收到 SSH Agent 暴露的 SCM 凭据；现有受信 checkout/known_hosts 由运维管理，脚本不降低主机密钥校验。

## 4. 结果与验收

- `PASS`：没有生产下限的 P0/P1 阻塞问题，P2/P3 advisory 仍保留；只有当前远端 base/head 与冻结目标一致、归档成功后，Job 才成功。
- `BLOCK`、`ERROR`、`FULL_REQUIRED`、`MANUAL_REQUIRED`、`SUPERSEDED`、权限失败或缺失 checkpoint 均不能标绿。缺失摘要不伪造 `review-summary.json`。
- Artifact 保存本次 `run.json`、`review-summary.md/json`（若本次真实存在）、`package.json` 与 `evidence.tar.gz`。共享打包命令直接读取 stdout 指定的 session，不搜索首个摘要，不重复打包整个共享根。
- stdout 外层 `runner.result_source` 表示 `EXECUTED` / `REUSED`。原始 schema-v10 和 summary 不改写。Jenkins 远端检查是独立发布门禁；其失败必须覆盖 stdout 中仅代表本地 refs 的 `CURRENT`。

首次上线使用可信同仓 PR 验证：

1. 同请求再次执行 Provider 调用数为 0，PASS/BLOCK 不变；并发请求串行且只有一次 Native。
2. head 严格追加仅允许 FULL 后一次 INCREMENTAL；第三轮 MANUAL_REQUIRED。base、contract、policy、Provider 版本或配置改变都不得误命中缓存。
3. 故障注入丢失 checkpoint、损坏状态、Restricted 中断以及旧 PR head：保持 HOLD，只允许合法 Restricted 恢复，不能自动 FULL 或旧证据标绿。
4. Artifact 对应本次精确 session，摘要与压缩包 digest 可追溯。中断后无摘要的包不混入旧 PASS。

## 5. 故障与升级

登录或版本故障由 Agent 服务用户修复；workspace 不干净时仅清理该 Job 自己的产物。升级先用可信 PR 验证，再推广 required check；更换 Provider 版本和有效配置时同步更新执行声明，不能通过复制旧指纹继续缓存。

状态存储缺失/损坏、目标 Git object 不在或 policy 变化时，保留日志并人工处理。不得删除索引、换空目录或循环调用 FULL 来“修复”失败；完整持久卷丢失也需要运维恢复或明确批准新 lineage。跨机器 Service 的 CAS、持久保留和 envelope-v3 见 [公司 CI 恢复合同](company-ci-review-result-envelope.md)。
