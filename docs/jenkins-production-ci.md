# Jenkins 生产 CI 接入

适用于 GitHub 私有仓库、Jenkins Multibranch Pipeline 和专用 Linux Agent。审查单位是整个 PR 的 `merge-base → head`；Provider 使用 Agent 系统用户已有的 Codex 或 Claude Code 登录态，不配置 Provider API Key。

## 1. 准备 Agent

Agent 要求：

- 标签为 `code-quality`，每个系统用户只设 1 个 executor。
- 使用专用低权限用户，不保存与审查无关的凭据。
- 同一用户预装 `quality-review v0.5.2` 和一个已登录的 Provider。

```sh
curl -fsSL https://github.com/Fueav/code-quality/releases/download/v0.5.2/install.sh |
  INSTALL_DIR="$HOME/.local/bin" sh -s -- v0.5.2

quality-review version          # 必须是 quality-review v0.5.2
codex login status              # Codex 二选一
claude auth status --json       # Claude Code 二选一
```

不安装交互式插件，不设置 `OPENAI_API_KEY` 或 `ANTHROPIC_API_KEY`。

## 2. 配置 Jenkins

- 安装 Pipeline、Multibranch Pipeline、GitHub Branch Source 和 SSH Agent 插件。
- 只发现源仓库内部 PR，关闭 fork PR 构建。
- SCM 使用只读 SSH 凭据，下面示例的 Credentials ID 为 `github-readonly`。
- PR Discovery 优先选择 `The current pull request revision`。
- 将 Jenkins PR 状态配置为仓库 required check。
- 此审查 stage 必须在编译、测试等产生文件的步骤之前运行。

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
test "$(quality-review version)" = 'quality-review v0.5.2'

case "$CODE_QUALITY_PROVIDER" in
  codex) host=codex; command=run-codex; model=gpt-5.6-sol ;;
  claude) host=claude-code; command=run-claude; model=sonnet; export DISABLE_AUTOUPDATER=1 ;;
  *) exit 2 ;;
esac

quality-review doctor --host "$host" --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA" \
  --execution-profile production-ci | tee "$REVIEW_ROOT/doctor.json"

quality-review "$command" --repo "$WORKSPACE" \
  --base "$BASE_SHA" --target "$TARGET_SHA" \
  --diff-reason jenkins_github_pull_request \
  --execution-profile production-ci \
  --model "$model" --reasoning-effort high \
  --output-root "$REVIEW_ROOT/sessions" | tee "$REVIEW_ROOT/run.json"
'''
      }

      post {
        always {
          sh '''#!/usr/bin/env bash
set -eu
rm -rf "$WORKSPACE/code-quality-artifacts"
mkdir -p "$WORKSPACE/code-quality-artifacts"
if [ -n "${REVIEW_ROOT:-}" ] && [ -d "$REVIEW_ROOT" ]; then
  cp -R "$REVIEW_ROOT"/. "$WORKSPACE/code-quality-artifacts/"
fi
'''
          archiveArtifacts artifacts: 'code-quality-artifacts/**/*',
            allowEmptyArchive: true, fingerprint: true
        }
      }
    }
  }
}
```

## 4. 结果与验收

- `PASS`、`MANUAL_REVIEW`：执行成功并归档报告；发现保持 `report_only`，不自动阻止合并。
- `BLOCKED`、`INCOMPLETE`：Jenkins stage 失败，Provider 环境或执行需要修复。
- 首次上线用一个可信同仓 PR 验证：doctor 为 `READY`、Artifacts 含 `doctor.json`、`run.json` 和完整 session；再次推送 PR 时旧构建被取消；fork PR 不运行。

安全边界：Provider 登录用户不得保存其他生产凭据；只在 `sshagent` 块内暴露只读 SCM 凭据；Jenkinsfile 或共享库必须由运维/CODEOWNERS 保护，不能让未信任 PR 修改后直接执行。
