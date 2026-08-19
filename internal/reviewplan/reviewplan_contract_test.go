package reviewplan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestExplicitDeployToProductionBuildsMergeBaseFullPlan(t *testing.T) {
	repo := reviewPlanRepository(t)
	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, BaseRef: "refs/heads/production", HeadRef: "refs/heads/deploy",
		ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusReady || decision.ProviderInvocations != 0 {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.BaseRef != "production" || decision.HeadRef != "deploy" || decision.BaseTipCommit != repo.Production || decision.CurrentHead != repo.Deploy || decision.MergeBase != repo.Common {
		t.Fatalf("frozen refs = %#v", decision)
	}
	if decision.Request.BaseCommit != repo.Common || decision.Request.TargetCommit != repo.Deploy || decision.ProviderRequest.BaseCommit != repo.Common {
		t.Fatalf("requests = %#v / %#v", decision.Request, decision.ProviderRequest)
	}
	if strings.Join(decision.Request.ChangedFiles, ",") != "deploy.txt" || len(decision.DeltaChangedFiles) != 0 {
		t.Fatalf("files = %#v / %#v", decision.Request.ChangedFiles, decision.DeltaChangedFiles)
	}
	if !strings.HasPrefix(decision.ReviewKey, "review-v1:sha256:") || !strings.HasPrefix(decision.ContractDigest, "contract-v1:sha256:") {
		t.Fatalf("identity = %q / %q", decision.ReviewKey, decision.ContractDigest)
	}
}

func TestEquivalentExplicitRefSpellingsProduceSameReviewKey(t *testing.T) {
	repo := reviewPlanRepository(t)
	runReviewPlanGit(t, repo.Path, "update-ref", "refs/remotes/origin/production", repo.Production)
	runReviewPlanGit(t, repo.Path, "update-ref", "refs/remotes/origin/deploy", repo.Deploy)
	build := func(baseRef, headRef string) Decision {
		decision, err := Build(context.Background(), Input{
			RepositoryPath: repo.Path, BaseRef: baseRef, HeadRef: headRef,
			ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(), Environment: map[string]string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}
	direct := build("refs/heads/production", "refs/heads/deploy")
	remote := build("origin/production", "origin/deploy")
	if direct.ReviewKey != remote.ReviewKey {
		t.Fatalf("equivalent refs changed key: %q != %q", direct.ReviewKey, remote.ReviewKey)
	}
}

func TestIncrementalPlanUsesPreviousHeadDeltaAndPreviousP0P1Only(t *testing.T) {
	repo := reviewPlanRepository(t)
	previousPath := writePreviousReviewResult(t, repo, reviewPlanContract())
	runReviewPlanGit(t, repo.Path, "switch", "deploy")
	writeReviewPlanFile(t, filepath.Join(repo.Path, "delta.go"), "package delta\n")
	runReviewPlanGit(t, repo.Path, "add", "delta.go")
	runReviewPlanGit(t, repo.Path, "commit", "-m", "incremental fix")
	current := runReviewPlanGit(t, repo.Path, "rev-parse", "HEAD")

	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousPath,
		ReviewGoal: "protect behavior", Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusReady || decision.CurrentHead != current || decision.PreviousHead == nil || *decision.PreviousHead != repo.Deploy {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.ProviderRequest.BaseCommit != repo.Deploy || decision.ProviderRequest.TargetCommit != current || strings.Join(decision.ProviderRequest.ChangedFiles, ",") != "delta.go" {
		t.Fatalf("provider request = %#v", decision.ProviderRequest)
	}
	if strings.Join(decision.DeltaChangedFiles, ",") != "delta.go" || len(decision.PreviousBlockingFindings()) != 1 || decision.PreviousBlockingFindings()[0].Priority != 1 {
		t.Fatalf("delta/blockers = %#v / %#v", decision.DeltaChangedFiles, decision.PreviousBlockingFindings())
	}
}

func TestIncrementalPlanStopsBeforeThirdAutomaticReview(t *testing.T) {
	repo := reviewPlanRepository(t)
	fullContract := reviewPlanContract()
	incrementalContract := fullContract
	incrementalContract.ProviderOutputSchema = "sha256:" + strings.Repeat("4", 64)
	previousFullPath := writePreviousReviewResult(t, repo, fullContract)

	runReviewPlanGit(t, repo.Path, "switch", "deploy")
	writeReviewPlanFile(t, filepath.Join(repo.Path, "first-delta.go"), "package firstdelta\n")
	runReviewPlanGit(t, repo.Path, "add", "first-delta.go")
	runReviewPlanGit(t, repo.Path, "commit", "-m", "first incremental fix")
	firstDecision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousFullPath,
		ReviewGoal: "protect behavior", Contract: incrementalContract, ParentContract: fullContract,
		Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	previousBlockers := firstDecision.PreviousBlockingFindings()
	if firstDecision.Status != StatusReady || len(previousBlockers) != 1 {
		t.Fatalf("first incremental decision = %#v", firstDecision)
	}
	providerResult := map[string]any{
		"previous_finding_resolutions": []any{map[string]any{
			"finding_id": previousBlockers[0].ID, "status": quality.ResolutionResolved,
			"reason": "The first delta removes the broken path.", "current_finding": nil,
		}},
		"new_findings": []any{},
	}
	providerJSON := mustMarshalReviewPlan(t, providerResult)
	firstOutcome, err := quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request: firstDecision.Request, ProviderRequest: firstDecision.ProviderRequest,
		Identity: firstDecision.ReviewIdentity, PreviousBlockingFindings: previousBlockers,
		ReviewGoal: "protect behavior",
	}, providerJSON, nil)
	if err != nil {
		t.Fatal(err)
	}
	previousIncrementalPath := writeReviewPlanOutcome(t, firstOutcome)

	writeReviewPlanFile(t, filepath.Join(repo.Path, "second-delta.go"), "package seconddelta\n")
	runReviewPlanGit(t, repo.Path, "add", "second-delta.go")
	runReviewPlanGit(t, repo.Path, "commit", "-m", "second incremental fix")
	secondDecision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousIncrementalPath,
		ReviewGoal: "protect behavior", Contract: incrementalContract, ParentContract: fullContract,
		Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondDecision.Status != StatusManualRequired || secondDecision.ProviderInvocations != 0 ||
		strings.Join(secondDecision.ManualRequiredReasons, ",") != "automatic_review_round_limit_reached" {
		t.Fatalf("second incremental decision = %#v", secondDecision)
	}
}

func TestIncrementalPlanReturnsFullRequiredBeforeProviderWork(t *testing.T) {
	cases := map[string]struct {
		addDelta bool
		mutate   func(*Input)
	}{
		"empty delta": {mutate: func(input *Input) {}},
		"contract changed": {addDelta: true, mutate: func(input *Input) {
			input.Contract.Model = "different-model"
		}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			repo := reviewPlanRepository(t)
			previousPath := writePreviousReviewResult(t, repo, reviewPlanContract())
			if testCase.addDelta {
				runReviewPlanGit(t, repo.Path, "switch", "deploy")
				writeReviewPlanFile(t, filepath.Join(repo.Path, "delta.go"), "package delta\n")
				runReviewPlanGit(t, repo.Path, "add", "delta.go")
				runReviewPlanGit(t, repo.Path, "commit", "-m", "incremental change")
			}
			input := Input{
				RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
				ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousPath,
				ReviewGoal: "protect behavior", Contract: reviewPlanContract(), Environment: map[string]string{},
			}
			testCase.mutate(&input)
			decision, err := Build(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != StatusFullRequired || decision.ProviderInvocations != 0 || len(decision.FullRequiredReasons) == 0 {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestIncrementalPlanRejectsPreviousErrorResult(t *testing.T) {
	repo := reviewPlanRepository(t)
	previousPath := writePreviousErrorResult(t, repo, reviewPlanContract())
	runReviewPlanGit(t, repo.Path, "switch", "deploy")
	writeReviewPlanFile(t, filepath.Join(repo.Path, "delta.go"), "package delta\n")
	runReviewPlanGit(t, repo.Path, "add", "delta.go")
	runReviewPlanGit(t, repo.Path, "commit", "-m", "incremental change")

	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousPath,
		ReviewGoal: "protect behavior", Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusFullRequired || !strings.Contains(strings.Join(decision.FullRequiredReasons, ","), "previous_result_not_reviewable") || decision.ProviderInvocations != 0 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestLegacyExactCommitRangeIsFullOnly(t *testing.T) {
	repo := reviewPlanRepository(t)
	_, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, Base: repo.Common, Target: repo.Deploy,
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: filepath.Join(t.TempDir(), "previous.json"),
		Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "legacy --base/--target") {
		t.Fatalf("error = %v", err)
	}
}

func TestIncrementalBaseAdvanceAndRebaseRequireFull(t *testing.T) {
	tests := map[string]struct {
		mutate func(*testing.T, reviewPlanRepo)
		want   string
	}{
		"base advance": {
			mutate: func(t *testing.T, repo reviewPlanRepo) {
				runReviewPlanGit(t, repo.Path, "switch", "production")
				writeReviewPlanFile(t, filepath.Join(repo.Path, "base-advance.txt"), "advanced\n")
				runReviewPlanGit(t, repo.Path, "add", "base-advance.txt")
				runReviewPlanGit(t, repo.Path, "commit", "-m", "advance production again")
			},
			want: "base_tip_changed",
		},
		"rebase": {
			mutate: func(t *testing.T, repo reviewPlanRepo) {
				runReviewPlanGit(t, repo.Path, "switch", "-C", "deploy", "production")
				writeReviewPlanFile(t, filepath.Join(repo.Path, "rebased.txt"), "replacement\n")
				runReviewPlanGit(t, repo.Path, "add", "rebased.txt")
				runReviewPlanGit(t, repo.Path, "commit", "-m", "replace deploy history")
			},
			want: "previous_head_not_ancestor",
		},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			repo := reviewPlanRepository(t)
			previousPath := writePreviousReviewResult(t, repo, reviewPlanContract())
			testCase.mutate(t, repo)
			decision, err := Build(context.Background(), Input{
				RepositoryPath: repo.Path, BaseRef: "production", HeadRef: "deploy",
				ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousPath,
				ReviewGoal: "protect behavior", Contract: reviewPlanContract(), Environment: map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != StatusFullRequired || !strings.Contains(strings.Join(decision.FullRequiredReasons, ","), testCase.want) || decision.ProviderInvocations != 0 {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestIncrementalRefIdentityChangeRequiresFull(t *testing.T) {
	tests := map[string]struct {
		baseRef string
		headRef string
		alias   string
		from    string
		want    string
	}{
		"base ref": {baseRef: "release", headRef: "deploy", alias: "release", from: "production", want: "base_ref_changed"},
		"head ref": {baseRef: "production", headRef: "delivery", alias: "delivery", from: "deploy", want: "head_ref_changed"},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			repo := reviewPlanRepository(t)
			previousPath := writePreviousReviewResult(t, repo, reviewPlanContract())
			runReviewPlanGit(t, repo.Path, "switch", "deploy")
			writeReviewPlanFile(t, filepath.Join(repo.Path, "delta.go"), "package delta\n")
			runReviewPlanGit(t, repo.Path, "add", "delta.go")
			runReviewPlanGit(t, repo.Path, "commit", "-m", "incremental change")
			runReviewPlanGit(t, repo.Path, "branch", testCase.alias, testCase.from)

			decision, err := Build(context.Background(), Input{
				RepositoryPath: repo.Path, BaseRef: testCase.baseRef, HeadRef: testCase.headRef,
				ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousPath,
				ReviewGoal: "protect behavior", Contract: reviewPlanContract(), Environment: map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != StatusFullRequired || !strings.Contains(strings.Join(decision.FullRequiredReasons, ","), testCase.want) {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

type reviewPlanRepo struct {
	Path       string
	Common     string
	Production string
	Deploy     string
}

func reviewPlanRepository(t *testing.T) reviewPlanRepo {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	runReviewPlanGit(t, "", "init", "-b", "production", repo)
	runReviewPlanGit(t, repo, "config", "user.email", "quality-test@example.com")
	runReviewPlanGit(t, repo, "config", "user.name", "Quality Test")
	writeReviewPlanFile(t, filepath.Join(repo, "README.md"), "common\n")
	runReviewPlanGit(t, repo, "add", "README.md")
	runReviewPlanGit(t, repo, "commit", "-m", "common")
	common := runReviewPlanGit(t, repo, "rev-parse", "HEAD")
	runReviewPlanGit(t, repo, "switch", "-c", "deploy")
	writeReviewPlanFile(t, filepath.Join(repo, "deploy.txt"), "deploy\n")
	runReviewPlanGit(t, repo, "add", "deploy.txt")
	runReviewPlanGit(t, repo, "commit", "-m", "deploy change")
	deploy := runReviewPlanGit(t, repo, "rev-parse", "HEAD")
	runReviewPlanGit(t, repo, "switch", "production")
	writeReviewPlanFile(t, filepath.Join(repo, "production.txt"), "production\n")
	runReviewPlanGit(t, repo, "add", "production.txt")
	runReviewPlanGit(t, repo, "commit", "-m", "production advance")
	production := runReviewPlanGit(t, repo, "rev-parse", "HEAD")
	return reviewPlanRepo{Path: repo, Common: common, Production: production, Deploy: deploy}
}

func reviewPlanContract() quality.NativeReviewContract {
	return quality.NativeReviewContract{
		ToolVersion: quality.SkillVersion, ResultSchemaVersion: quality.NativeResultSchemaVersion,
		ProviderOutputSchema:  "sha256:" + strings.Repeat("3", 64),
		PromptContractVersion: "3", EvaluationRubricVersion: quality.EvaluationRubricVersion,
		EvaluationRubricDigest: "sha256:" + strings.Repeat("4", 64),
		RestrictedPolicyDigest: "sha256:" + strings.Repeat("5", 64),
		RestrictedSchemaDigest: "sha256:" + strings.Repeat("6", 64),
		ProviderHost:           "codex", Model: "gpt-5.6-sol", ReasoningEffort: "max",
		ExecutionProfile: quality.ExecutionProfileProductionCI,
	}
}

func writePreviousReviewResult(t *testing.T, repo reviewPlanRepo, contract quality.NativeReviewContract) string {
	t.Helper()
	request := quality.ReviewRequest{
		Repository: filepath.Base(repo.Path), TargetBranch: "production",
		BaseCommit: repo.Common, TargetCommit: repo.Deploy, DiffSelectionReason: "explicit_ref_range",
		ChangedFiles: []string{"deploy.txt"}, AffectedEntries: []string{},
	}
	identity, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
		Contract: contract, Request: request, ReviewGoal: "protect behavior", ReviewScope: quality.ReviewScopeFull,
		BaseRef: "production", HeadRef: "deploy", BaseTipCommit: repo.Production, MergeBase: repo.Common, CurrentHead: repo.Deploy,
		DeltaChangedFiles: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"findings":[` + string(mustMarshalReviewPlan(t, quality.NativeFinding{
		Title: "Blocking defect", Priority: 1,
		CodeLocation: quality.NativeCodeLocation{Path: "deploy.txt", StartLine: 1, EndLine: 1},
		Reason:       "A reachable production path is wrong.", Suggestion: "Correct the path.",
	})) + `,` + string(mustMarshalReviewPlan(t, quality.NativeFinding{
		Title: "Advisory defect", Priority: 2,
		CodeLocation: quality.NativeCodeLocation{Path: "deploy.txt", StartLine: 1, EndLine: 1},
		Reason:       "A contained behavior is wrong.", Suggestion: "Correct the contained behavior.",
	})) + `]}`
	outcome, err := quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request: request, ProviderRequest: request, Identity: identity, ReviewGoal: "protect behavior",
	}, []byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return writeReviewPlanOutcome(t, outcome)
}

func writePreviousErrorResult(t *testing.T, repo reviewPlanRepo, contract quality.NativeReviewContract) string {
	t.Helper()
	request := quality.ReviewRequest{
		Repository: filepath.Base(repo.Path), TargetBranch: "production",
		BaseCommit: repo.Common, TargetCommit: repo.Deploy, DiffSelectionReason: "explicit_ref_range",
		ChangedFiles: []string{"deploy.txt"}, AffectedEntries: []string{},
	}
	identity, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
		Contract: contract, Request: request, ReviewGoal: "protect behavior", ReviewScope: quality.ReviewScopeFull,
		BaseRef: "production", HeadRef: "deploy", BaseTipCommit: repo.Production, MergeBase: repo.Common, CurrentHead: repo.Deploy,
		DeltaChangedFiles: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request: request, ProviderRequest: request, Identity: identity, ReviewGoal: "protect behavior",
	}, nil, errors.New("provider failed"))
	if err != nil {
		t.Fatal(err)
	}
	return writeReviewPlanOutcome(t, outcome)
}

func writeReviewPlanOutcome(t *testing.T, outcome quality.NativeOutcome) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "previous-result.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := outcome.EncodeJSON(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustMarshalReviewPlan(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeReviewPlanFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runReviewPlanGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	args := append([]string(nil), arguments...)
	if repo != "" {
		args = append([]string{"-C", repo}, args...)
	}
	command := exec.Command("git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
