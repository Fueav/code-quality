# Jenkins 生产 CI 接入（v0.5.4）

适用于 GitHub 私有仓库、Jenkins Multibranch Pipeline 和专用 Linux Agent。审查单位是整个 PR 的 `merge-base → head`；Provider 使用 Agent 系统用户已有的 Codex 或 Claude Code 登录态，不配置 Provider API Key。

运维只需完成四件事：准备一个专用 Agent、以 Agent 服务用户安装并登录 Provider、创建 Multibranch Pipeline、把下面的 Jenkinsfile 合入受保护分支。

## 1. 准备 Agent

先确认实际运行 Jenkins Agent 的 Linux 系统用户，并始终以这个用户执行安装和登录。Agent 要求：

- 标签为 `code-quality`，每个系统用户只设 1 个 executor。
- 使用专用低权限用户，不保存与审查无关的凭据。
- 已安装 `bash`、`git`、`curl` 和 `tar`，能够访问 GitHub 与所选 Provider。
- 同一用户预装 `quality-review v0.5.4` 和一个已登录的 Provider。

```sh
command -v bash git curl tar

curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.4/install.sh |
  INSTALL_DIR="$HOME/.local/bin" sh -s -- v0.5.4

command -v quality-review
quality-review version          # 必须是 quality-review v0.5.4
codex login status              # Codex 二选一
claude auth status --json       # Claude Code 二选一
```

确保 Jenkins Agent 服务的 `PATH` 包含 `$HOME/.local/bin`。不安装交互式插件，不设置 `OPENAI_API_KEY` 或 `ANTHROPIC_API_KEY`，也不要把登录目录复制进 Workspace。

## 2. 配置 Jenkins

固定配置如下：

| 配置 | 值 |
| --- | --- |
| Jenkins 插件 | Pipeline、Multibranch Pipeline、GitHub Branch Source、SSH Agent |
| Agent label | `code-quality` |
| SCM 凭据 | 只读 SSH；示例 Credentials ID 为 `github-readonly` |
| PR Discovery | `The current pull request revision`；只发现同仓 PR，关闭 fork PR |
| Provider | `codex` 或 `claude`，一次只选一个 |
| Required check | 将该 Multibranch Job 的实际 GitHub status context 设为 required check |

此审查 stage 必须在编译、测试等会产生文件的步骤之前运行。

## 3. Jenkinsfile

将以下 stage 合入受保护的 Jenkins Pipeline；如使用 Claude，把 `CODE_QUALITY_PROVIDER` 改为 `claude`。

```groovy
pipeline {
  agent none

  options {
    skipDefaultCheckout(true)
    disableConcurrentBuilds(abortPrevious: true)
    buildDiscarder(logRotator(numToKeepStr: '30'))
    timestamps()
  }

  environment {
    CODE_QUALITY_PROVIDER = 'codex' // codex 或 claude
  }

  stages {
    stage('Code Quality PR Review') {
      when {
        beforeAgent true
        changeRequest()
      }
      agent { label 'code-quality' }
      options { timeout(time: 30, unit: 'MINUTES') }

      steps {
        checkout scm

        // 清理上一次构建导出的未跟踪报告，保证 doctor 看到干净工作区。
        sh 'rm -rf "$WORKSPACE/code-quality-artifacts"'

        script {
          env.REVIEW_ROOT = "${pwd(tmp: true)}/code-quality-${env.BUILD_NUMBER}"
        }

        sshagent(credentials: ['github-readonly']) {
          sh '''#!/usr/bin/env bash
set -euo pipefail
test -n "${CHANGE_ID:-}"
test -n "${CHANGE_TARGET:-}"
test -z "${CHANGE_FORK:-}"
case "$CHANGE_ID" in *[!0-9]*|'') exit 2 ;; esac
git check-ref-format --branch "$CHANGE_TARGET" >/dev/null

mkdir -p "$REVIEW_ROOT"
git fetch --no-tags origin \
  "+refs/heads/${CHANGE_TARGET}:refs/remotes/origin/${CHANGE_TARGET}" \
  "+refs/pull/${CHANGE_ID}/head:refs/remotes/origin/pr/${CHANGE_ID}/head"

base_tip=$(git rev-parse "refs/remotes/origin/${CHANGE_TARGET}^{commit}")
target=$(git rev-parse "refs/remotes/origin/pr/${CHANGE_ID}/head^{commit}")
base=$(git merge-base "$base_tip" "$target")
printf 'BASE_SHA=%s\nTARGET_SHA=%s\n' "$base" "$target" > "$REVIEW_ROOT/range.env"
'''
        }

        sh '''#!/usr/bin/env bash
set -euo pipefail
. "$REVIEW_ROOT/range.env"
test "$(quality-review version)" = 'quality-review v0.5.4'

case "$CODE_QUALITY_PROVIDER" in
  codex) host=codex; command=run-codex; model=gpt-5.6-sol ;;
  claude) host=claude-code; command=run-claude; model=sonnet; export DISABLE_AUTOUPDATER=1 ;;
  *) exit 2 ;;
esac

quality-review doctor --host "$host" --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA" \
  --execution-profile production-ci | tee "$REVIEW_ROOT/doctor.json"

set +e
quality-review "$command" --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA" \
  --diff-reason jenkins_github_pull_request \
  --execution-profile production-ci \
  --model "$model" --reasoning-effort high \
  --output-root "$REVIEW_ROOT/sessions" | tee "$REVIEW_ROOT/run.json"
review_exit=${PIPESTATUS[0]}
set -e
summary=$(find "$REVIEW_ROOT/sessions" -path '*/output/review-summary.md' -type f -print -quit)
if [ -n "$summary" ]; then cat "$summary"; fi
exit "$review_exit"
'''
      }

      post {
        always {
          sh '''#!/usr/bin/env bash
set -eu
artifact_root="$WORKSPACE/code-quality-artifacts"
rm -rf "$artifact_root"
mkdir -p "$artifact_root"
if [ -n "${REVIEW_ROOT:-}" ] && [ -d "$REVIEW_ROOT" ]; then
  for name in doctor.json run.json; do
    if [ -f "$REVIEW_ROOT/$name" ]; then cp "$REVIEW_ROOT/$name" "$artifact_root/$name"; fi
  done
  for name in review-summary.md review-summary.json; do
    candidate=$(find "$REVIEW_ROOT/sessions" -path "*/output/$name" -type f -print -quit 2>/dev/null || true)
    if [ -n "$candidate" ]; then cp "$candidate" "$artifact_root/$name"; fi
  done
  tar -czf "$artifact_root/evidence.tar.gz" -C "$REVIEW_ROOT" .
fi
if [ ! -f "$artifact_root/review-summary.md" ]; then
  printf '%s\n' '# ⚠️ AI Code Review: ERROR' '' 'Release: `HOLD`' '' 'The review did not run. Inspect doctor.json.' > "$artifact_root/review-summary.md"
  printf '%s\n' '{"schema_version":2,"result":"ERROR","release":"HOLD","blocking_issues":0,"advisory_issues":0,"issues":[]}' > "$artifact_root/review-summary.json"
fi
cat "$artifact_root/review-summary.md"
'''
          archiveArtifacts artifacts: 'code-quality-artifacts/*',
            allowEmptyArchive: true, fingerprint: true
        }
      }
    }
  }
}
```

## 4. 结果与验收

- `PASS`：没有 P0/P1 阻塞问题，可以继续发布流程；P2/P3 advisory 仍会展示，Jenkins stage 成功。
- `BLOCK`：存在至少一个 P0/P1 必须修复的问题；`ERROR`：扫描不可信或未完成。两者都使 Jenkins stage 失败。
- 开发者只看 `review-summary.md`；机器读取 `review-summary.json`；完整原始证据统一放在 `evidence.tar.gz`。

Jenkins 控制台和 Artifact 中的主结论类似：

```text
# ✅ AI Code Review: PASS
Release: CONTINUE
Blocking issues: 0
Advisory issues: 0
```

首次上线必须用一个可信同仓 PR 验证：

1. `doctor.json` 的状态为 `READY`。
2. 控制台能直接看到 `PASS`、`BLOCK` 或 `ERROR`；`BLOCK/ERROR` 会让 Job 失败。
3. Artifact 的 `code-quality-artifacts/` 目录包含 `doctor.json`、`run.json`、`review-summary.md`、`review-summary.json` 和 `evidence.tar.gz`。
4. 再次推送同一 PR 时旧构建被取消；fork PR 不运行。

## 5. 常见故障与升级

- `quality-review` 找不到：把 `$HOME/.local/bin` 加入 Jenkins Agent 服务的 `PATH`，然后重启 Agent。
- `doctor` 返回 `BLOCKED`：以 Agent 服务用户重新执行 `codex login` 或 `claude auth login`，再运行对应的 status 命令。
- 版本不匹配：重新运行第 1 节的固定版本安装命令，并同步修改 Jenkinsfile 中的版本断言。
- Workspace 不干净：确认审查 stage 位于构建步骤之前，并清理该 Job 自己生成的文件；不要删除业务仓库内容。
- 升级新版本时先在一条可信 PR 验证，再将 required check 应用于全部受保护分支。

安全边界：Provider 登录用户不得保存其他生产凭据；只在 `sshagent` 块内暴露只读 SCM 凭据；Jenkinsfile 或共享库必须由运维/CODEOWNERS 保护，不能让未信任 PR 修改后直接执行。
