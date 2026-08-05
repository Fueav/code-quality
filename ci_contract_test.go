package bundle

import (
	"os"
	"strings"
	"testing"
)

func TestReusableCIWorkflowKeepsNativeReviewReportOnlyAndDualProvider(t *testing.T) {
	raw, err := os.ReadFile(".github/workflows/code-quality-reusable.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"workflow_call:",
		"provider_api_key:",
		"base_sha:",
		"target_sha:",
		"reasoning_effort:",
		"default: low",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"github.event.pull_request.user.login != 'dependabot[bot]'",
		"permissions:\n      contents: read",
		"persist-credentials: false",
		"fetch-depth: 0",
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
		"@openai/codex@0.145.0",
		"@anthropic-ai/claude-code@2.1.220",
		"releases/download/v0.5.1/install.sh",
		"sh -s -- v0.5.1",
		"codex login --with-api-key",
		"ANTHROPIC_API_KEY:",
		"doctor --host codex",
		"doctor --host claude-code",
		"run-codex",
		"run-claude",
		"--output-root \"$QUALITY_REVIEW_OUTPUT_ROOT\"",
		"continue-on-error: true",
		"GITHUB_STEP_SUMMARY",
		"retention-days:",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("reusable CI workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request_target",
		"permissions: write-all",
		"contents: write",
		"pull-requests: write",
		"persist-credentials: true",
		"secrets: inherit",
		"bootstrap.sh",
		"actions/checkout@v",
		"actions/setup-node@v",
		"actions/upload-artifact@v",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("reusable CI workflow must not contain %q", forbidden)
		}
	}
}

func TestReadmeSeparatesPersonalAndCompanyCIOnboarding(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	personal := strings.Index(readme, "## 个人开发者：一句话安装并审查")
	company := strings.Index(readme, "## 公司 CI：集中配置、仓库最小接入")
	if personal < 0 || company < 0 || personal >= company {
		t.Fatalf("README onboarding order is invalid: personal=%d company=%d", personal, company)
	}
	for _, required := range []string{
		"请为当前仓库安装并运行 Fueav code-quality v0.5.1",
		"bootstrap.sh | sh -s -- v0.5.1 codex",
		"bootstrap.sh | sh -s -- v0.5.1 claude",
		"quality-review doctor --host codex",
		"quality-review doctor --host claude-code",
		"先提交要审查的改动",
		"CI 不需要安装 Codex 或 Claude Code 插件",
		".github/workflows/code-quality-reusable.yml",
		"uses: Fueav/code-quality/.github/workflows/code-quality-reusable.yml@v0.5.1",
		"provider_api_key",
		"base_sha",
		"target_sha",
		"reasoning_effort: low",
		"report_only",
		"MANUAL_REVIEW",
		"INCOMPLETE",
		"fork PR",
		"Dependabot PR",
		"pull_request_target",
		"persist-credentials: false",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README is missing CI/personal onboarding contract %q", required)
		}
	}
}
