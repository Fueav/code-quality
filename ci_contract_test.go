package bundle

import (
	"os"
	"strings"
	"testing"
)

func TestReusableCIWorkflowPublishesConciseReleaseGateForBothProviders(t *testing.T) {
	raw, err := os.ReadFile(".github/workflows/code-quality-reusable.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"workflow_call:",
		"reasoning_effort:",
		"default: max",
		"group: code-quality",
		"labels: [self-hosted, linux, code-quality]",
		"github.event_name == 'pull_request'",
		"github.event.repository.private == true",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"github.event.pull_request.user.login != 'dependabot[bot]'",
		"permissions:\n      contents: read",
		"persist-credentials: false",
		"fetch-depth: 0",
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
		"command -v quality-review",
		"quality-review v0.5.8",
		"plan --host \"$QUALITY_REVIEW_HOST\"",
		"doctor --host \"$QUALITY_REVIEW_HOST\"",
		"--execution-profile \"$QUALITY_REVIEW_EXECUTION_PROFILE\"",
		"--model \"$QUALITY_REVIEW_MODEL\"",
		"--reasoning-effort \"$QUALITY_REVIEW_REASONING_EFFORT\"",
		"run-codex",
		"run-claude",
		"--output-root \"$QUALITY_REVIEW_OUTPUT_ROOT\"",
		"continue-on-error: true",
		"GITHUB_STEP_SUMMARY",
		"review-summary.md",
		"review-summary.json",
		"advisory_issues",
		"evidence.tar.gz",
		"code-quality-artifact",
		"retention-days:",
		"Verify checkout remained unchanged",
		"github.event.pull_request.number",
		"github.event.pull_request.head.sha",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("reusable CI workflow is missing %q", required)
		}
	}
	if count := strings.Count(workflow, `--model "$QUALITY_REVIEW_MODEL"`); count != 3 {
		t.Errorf("reusable CI must pass one resolved model to plan/doctor/run, got %d occurrences", count)
	}
	if count := strings.Count(workflow, `--reasoning-effort "$QUALITY_REVIEW_REASONING_EFFORT"`); count != 3 {
		t.Errorf("reusable CI must pass one resolved reasoning effort to plan/doctor/run, got %d occurrences", count)
	}
	if count := strings.Count(workflow, `--execution-profile "$QUALITY_REVIEW_EXECUTION_PROFILE"`); count != 3 {
		t.Errorf("reusable CI must pass one execution profile to plan/doctor/run, got %d occurrences", count)
	}
	for _, forbidden := range []string{
		"pull_request_target",
		"permissions: write-all",
		"contents: write",
		"pull-requests: write",
		"persist-credentials: true",
		"secrets: inherit",
		"provider_api_key",
		"base_sha:",
		"target_sha:",
		"inputs.base_sha",
		"inputs.target_sha",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"codex login",
		"npm install",
		"ubuntu-latest",
		"actions/setup-node",
		"curl ",
		"install.sh",
		"bootstrap.sh",
		"actions/checkout@v",
		"actions/setup-node@v",
		"actions/upload-artifact@v",
		"default: low",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("reusable CI workflow must not contain %q", forbidden)
		}
	}
}

func TestReadmeSeparatesPersonalAndLinuxCIOnboarding(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	personal := strings.Index(readme, "## 个人开发者：一句话安装并审查")
	linuxCI := strings.Index(readme, "## Linux CI：复用自托管 Runner 的原生登录态")
	if personal < 0 || linuxCI < 0 || personal >= linuxCI {
		t.Fatalf("README onboarding order is invalid: personal=%d linux_ci=%d", personal, linuxCI)
	}
	for _, required := range []string{
		"请为当前仓库安装并运行 Fueav code-quality v0.5.8",
		"bootstrap.sh | sh -s -- v0.5.8 codex",
		"bootstrap.sh | sh -s -- v0.5.8 claude",
		"quality-review doctor --host codex",
		"quality-review doctor --host claude-code",
		"先提交要审查的改动",
		"self-hosted Linux runner",
		"运行 GitHub Actions Runner 的同一个系统用户",
		"codex exec",
		"不接收 Provider API key",
		".github/workflows/code-quality-reusable.yml",
		"uses: Fueav/code-quality/.github/workflows/code-quality-reusable.yml@v0.5.8",
		"PR 为审查单元",
		"merge-base",
		"schema v10",
		"--base-ref origin/production",
		"--head-ref origin/deploy/dockerhost-dev",
		"--review-scope incremental",
		"FULL_REQUIRED",
		"MANUAL_REQUIRED",
		"REUSED",
		"SUPERSEDED",
		"P2/P3-only",
		"production-ci",
		"runner group",
		"reasoning_effort: max",
		"review-summary.md",
		"review-summary.json",
		"evidence.tar.gz",
		"BLOCK",
		"ERROR",
		"fork PR",
		"Dependabot PR",
		"pull_request_target",
		"persist-credentials: false",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README is missing CI/personal onboarding contract %q", required)
		}
	}
	for _, forbidden := range []string{"## 公司 CI", "CODE_QUALITY_OPENAI_API_KEY", "CODE_QUALITY_CLAUDE_API_KEY", "provider_api_key", "base_sha: ${{", "target_sha: ${{"} {
		if strings.Contains(readme, forbidden) {
			t.Errorf("README must not contain obsolete CI contract %q", forbidden)
		}
	}
}

func TestJenkinsUsesOneMaxEffortContractForPlanDoctorAndRun(t *testing.T) {
	raw, err := os.ReadFile("docs/jenkins-production-ci.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, required := range []string{
		"Jenkins 生产 CI 接入（v0.5.8）",
		"effort=max",
		"review_args=(",
		"quality-review plan --host \"$host\" \"${review_args[@]}\"",
		"quality-review doctor --host \"$host\" \"${review_args[@]}\"",
		"quality-review \"$command\" \"${review_args[@]}\"",
		"plan.json",
	} {
		if !strings.Contains(document, required) {
			t.Errorf("Jenkins contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{"--reasoning-effort high", "quality-review v0.5.5"} {
		if strings.Contains(document, forbidden) {
			t.Errorf("Jenkins contract retains %q", forbidden)
		}
	}
}

func TestCompanyCIVersionsExternalRunnerPolicyOutsideCLIResults(t *testing.T) {
	raw, err := os.ReadFile("docs/company-ci-review-result-envelope.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, required := range []string{
		"runner_policy_version",
		"(repository, review_key, contract_digest, runner_policy_version)",
		"强制 FULL",
		"本地代码读取",
		"ERROR/HOLD",
		"不得写入原始 schema-v10 结果或 envelope-v3",
		"resume-restricted --session",
		"NATIVE_FROZEN",
		"RESTRICTED_RUNNING",
		"RESTRICTED_RETRYABLE",
		"不得自动 clone 并重跑 FULL",
		"不能按 `FULL_REQUIRED` 路径自动回退成 FULL",
	} {
		if !strings.Contains(document, required) {
			t.Errorf("company CI runner-policy contract is missing %q", required)
		}
	}
	schema, err := os.ReadFile("schemas/review-result-envelope-v2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schema), "runner_policy_version") {
		t.Fatal("runner policy leaked into the immutable envelope-v2 schema")
	}
}
