package reviewplan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestLegacyExactCommitDiscoveryRemainsFullCompatible(t *testing.T) {
	repo := reviewPlanRepository(t)
	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, Base: repo.Common, Target: repo.Deploy, DiffReason: "explicit_test",
		ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Request.BaseCommit != repo.Common || decision.Request.TargetCommit != repo.Deploy || decision.DetectionSource != "explicit_commits" {
		t.Fatalf("decision = %#v", decision)
	}
	if strings.Join(decision.Request.ChangedFiles, ",") != "deploy.txt" {
		t.Fatalf("changed files = %#v", decision.Request.ChangedFiles)
	}
}

func TestExplicitRangePairingAndMixingFailClosed(t *testing.T) {
	repo := reviewPlanRepository(t)
	cases := map[string]Input{
		"commit pair": {RepositoryPath: repo.Path, Base: repo.Common},
		"ref pair":    {RepositoryPath: repo.Path, BaseRef: "production"},
		"mixed":       {RepositoryPath: repo.Path, Base: repo.Common, Target: repo.Deploy, BaseRef: "production", HeadRef: "deploy"},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			input.ReviewScope = quality.ReviewScopeFull
			input.Contract = reviewPlanContract()
			input.Environment = map[string]string{}
			if _, err := Build(context.Background(), input); err == nil {
				t.Fatal("invalid range was accepted")
			}
		})
	}
}

func TestGitHubPullRequestUsesMergeBaseAndFreezesDirection(t *testing.T) {
	repo := reviewPlanRepository(t)
	eventPath := filepath.Join(t.TempDir(), "event.json")
	writeReviewPlanFile(t, eventPath, `{"repository":{"full_name":"acme/service"},"pull_request":{"number":42,"html_url":"https://github.example/acme/service/pull/42","base":{"sha":"`+repo.Production+`","ref":"production"},"head":{"sha":"`+repo.Deploy+`","ref":"deploy"}}}`)
	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(),
		Environment: map[string]string{
			"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": eventPath,
			"GITHUB_SERVER_URL": "https://github.example", "GITHUB_REPOSITORY": "acme/service", "GITHUB_RUN_ID": "9001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Request.Repository != "acme/service" || decision.BaseRef != "production" || decision.HeadRef != "deploy" || decision.BaseTipCommit != repo.Production || decision.MergeBase != repo.Common {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Request.Change == nil || decision.Request.Change.ID != "42" || decision.Request.Change.RunURL != "https://github.example/acme/service/actions/runs/9001" {
		t.Fatalf("change = %#v", decision.Request.Change)
	}
	if strings.Join(decision.Request.ChangedFiles, ",") != "deploy.txt" {
		t.Fatalf("target-branch-only file leaked into PR scope: %#v", decision.Request.ChangedFiles)
	}
}

func TestGitHubEventSymlinkIsRejected(t *testing.T) {
	repo := reviewPlanRepository(t)
	directory := t.TempDir()
	realPath := filepath.Join(directory, "real.json")
	writeReviewPlanFile(t, realPath, `{"repository":{"full_name":"acme/service"},"pull_request":{"number":1,"base":{"sha":"`+repo.Production+`","ref":"production"},"head":{"sha":"`+repo.Deploy+`","ref":"deploy"}}}`)
	linkPath := filepath.Join(directory, "event.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(),
		Environment: map[string]string{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": linkPath},
	})
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestGitLabMergeRequestDiscoveryRemainsCompatible(t *testing.T) {
	repo := reviewPlanRepository(t)
	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(),
		Environment: map[string]string{
			"CI_MERGE_REQUEST_IID": "77", "CI_MERGE_REQUEST_DIFF_BASE_SHA": repo.Production,
			"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "production", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "deploy",
			"CI_COMMIT_SHA": repo.Deploy, "CI_PROJECT_PATH": "acme/service",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.DetectionSource != "gitlab" || decision.Request.DiffSelectionReason != "gitlab_merge_request" || decision.MergeBase != repo.Common {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestLocalDiscoveryUsesOriginHEADAndExcludesDirtyWorktree(t *testing.T) {
	repo := reviewPlanRepository(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runReviewPlanGit(t, "", "init", "--bare", "-b", "production", remote)
	runReviewPlanGit(t, repo.Path, "remote", "add", "origin", remote)
	runReviewPlanGit(t, repo.Path, "push", "origin", "production", "deploy")
	runReviewPlanGit(t, repo.Path, "remote", "set-head", "origin", "production")
	runReviewPlanGit(t, repo.Path, "switch", "deploy")
	writeReviewPlanFile(t, filepath.Join(repo.Path, "uncommitted.txt"), "not reviewed\n")

	decision, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.BaseRef != "production" || decision.HeadRef != "deploy" || !decision.DirtyWorktree || strings.Join(decision.Request.ChangedFiles, ",") != "deploy.txt" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestMissingOriginHEADStillFailsClosed(t *testing.T) {
	repo := reviewPlanRepository(t)
	_, err := Build(context.Background(), Input{
		RepositoryPath: repo.Path, ReviewScope: quality.ReviewScopeFull, Contract: reviewPlanContract(), Environment: map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "origin/HEAD") {
		t.Fatalf("error = %v", err)
	}
}
