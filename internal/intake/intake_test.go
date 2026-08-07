package intake

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitBaseline(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	result, err := Discover(Options{
		RepositoryPath: repo,
		Base:           base, Target: target, DiffReason: "explicit_test",
		Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.BaseCommit != base || result.Request.TargetCommit != target {
		t.Fatalf("baseline = %s..%s", result.Request.BaseCommit, result.Request.TargetCommit)
	}
	if result.Request.DiffSelectionReason != "explicit_test" || result.DetectionSource != "explicit" {
		t.Fatalf("selection = %#v", result)
	}
	if strings.Join(result.Request.ChangedFiles, ",") != "docs/guide.md" {
		t.Fatalf("changed files = %#v", result.Request.ChangedFiles)
	}
}

func TestExplicitBaselineDerivesReasonWhenOnlyRangeIsSupplied(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	result, err := Discover(Options{
		RepositoryPath: repo,
		Base:           base,
		Target:         target,
		Environment:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.BaseCommit != base || result.Request.TargetCommit != target {
		t.Fatalf("baseline = %s..%s", result.Request.BaseCommit, result.Request.TargetCommit)
	}
	if result.Request.DiffSelectionReason != "explicit_commit_range" || result.DetectionSource != "explicit" {
		t.Fatalf("selection = %#v", result)
	}
}

func TestExplicitBaselineRequiresBothEndpoints(t *testing.T) {
	repo, base, _ := fixtureRepository(t)
	_, err := Discover(Options{RepositoryPath: repo, Base: base, Environment: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "must be provided together") {
		t.Fatalf("error = %v", err)
	}
}

func TestExplicitBaselineRejectsEmptyCommittedRange(t *testing.T) {
	repo, base, _ := fixtureRepository(t)
	_, err := Discover(Options{RepositoryPath: repo, Base: base, Target: base, Environment: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "no committed changes") {
		t.Fatalf("error = %v", err)
	}
}

func TestExplicitReasonRequiresRange(t *testing.T) {
	repo, _, _ := fixtureRepository(t)
	_, err := Discover(Options{RepositoryPath: repo, DiffReason: "manual", Environment: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "requires --base and --target") {
		t.Fatalf("error = %v", err)
	}
}

func TestLocalBaselineUsesOriginHEADAndExcludesDirtyWorktree(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	writeFile(t, filepath.Join(repo, "uncommitted.txt"), "not reviewed\n")
	result, err := Discover(Options{RepositoryPath: repo, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.BaseCommit != base || result.Request.TargetCommit != target {
		t.Fatalf("baseline = %s..%s, want %s..%s", result.Request.BaseCommit, result.Request.TargetCommit, base, target)
	}
	if result.Request.TargetBranch != "main" || result.Request.DiffSelectionReason != "local_branch_increment" {
		t.Fatalf("request = %#v", result.Request)
	}
	if !result.DirtyWorktree {
		t.Fatal("dirty worktree was not reported")
	}
	if strings.Join(result.Request.ChangedFiles, ",") != "docs/guide.md" {
		t.Fatalf("dirty file leaked into review: %#v", result.Request.ChangedFiles)
	}
}

func TestGitHubPullRequestBaseline(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	eventPath := filepath.Join(t.TempDir(), "event.json")
	event := map[string]any{
		"action":     "synchronize",
		"repository": map[string]any{"full_name": "acme/service", "private": true},
		"pull_request": map[string]any{
			"number": 17, "html_url": "https://github.example/acme/service/pull/17",
			"base": map[string]any{"sha": base, "ref": "main", "extra": true},
			"head": map[string]any{"sha": target, "ref": "feature"},
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Discover(Options{
		RepositoryPath: repo,
		Environment: map[string]string{
			"GITHUB_EVENT_NAME": "pull_request",
			"GITHUB_EVENT_PATH": eventPath,
			"GITHUB_SERVER_URL": "https://github.example",
			"GITHUB_REPOSITORY": "acme/service",
			"GITHUB_RUN_ID":     "9001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.Repository != "acme/service" || result.Request.TargetBranch != "main" || result.DetectionSource != "github" {
		t.Fatalf("result = %#v", result)
	}
	change := result.Request.Change
	if change == nil || change.Kind != "pull_request" || change.ID != "17" || change.BaseRef != "main" || change.HeadRef != "feature" || change.BaseTipCommit != base || change.RunURL != "https://github.example/acme/service/actions/runs/9001" {
		t.Fatalf("change = %#v", change)
	}
}

func TestGitHubPullRequestUsesMergeBaseWhenTargetBranchAdvances(t *testing.T) {
	repo, common, target := fixtureRepository(t)
	runGit(t, repo, "switch", "main")
	writeFile(t, filepath.Join(repo, "main-only.txt"), "target branch advanced\n")
	runGit(t, repo, "add", "main-only.txt")
	runGit(t, repo, "commit", "-m", "advance main")
	baseTip := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "switch", "feature")
	eventPath := filepath.Join(t.TempDir(), "event.json")
	writeFile(t, eventPath, `{"repository":{"full_name":"acme/service"},"pull_request":{"number":42,"base":{"sha":"`+baseTip+`","ref":"main"},"head":{"sha":"`+target+`","ref":"feature"}}}`)

	result, err := Discover(Options{RepositoryPath: repo, Environment: map[string]string{
		"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": eventPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.BaseCommit != common || result.Request.TargetCommit != target {
		t.Fatalf("range = %s..%s, want %s..%s", result.Request.BaseCommit, result.Request.TargetCommit, common, target)
	}
	if strings.Join(result.Request.ChangedFiles, ",") != "docs/guide.md" {
		t.Fatalf("PR scope contains target-branch-only files: %#v", result.Request.ChangedFiles)
	}
	if result.Request.Change == nil || result.Request.Change.BaseTipCommit != baseTip {
		t.Fatalf("change = %#v", result.Request.Change)
	}
}

func TestGitHubEventSymlinkIsRejected(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	writeFile(t, realPath, `{"repository":{"full_name":"acme/service"},"pull_request":{"number":1,"base":{"sha":"`+base+`","ref":"main"},"head":{"sha":"`+target+`","ref":"feature"}}}`)
	linkPath := filepath.Join(dir, "event.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(Options{
		RepositoryPath: repo,
		Environment:    map[string]string{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": linkPath},
	})
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestGitLabMergeRequestBaseline(t *testing.T) {
	repo, base, target := fixtureRepository(t)
	result, err := Discover(Options{
		RepositoryPath: repo,
		Environment: map[string]string{
			"CI_MERGE_REQUEST_IID":                "42",
			"CI_MERGE_REQUEST_DIFF_BASE_SHA":      base,
			"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "main",
			"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature",
			"CI_COMMIT_SHA":                       target,
			"CI_PROJECT_PATH":                     "acme/service",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Request.DiffSelectionReason != "gitlab_merge_request" || result.DetectionSource != "gitlab" {
		t.Fatalf("result = %#v", result)
	}
}

func TestMissingOriginHEADFailsClosed(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	configureGit(t, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	_, err := Discover(Options{RepositoryPath: repo, Environment: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "origin/HEAD") {
		t.Fatalf("error = %v", err)
	}
}

func fixtureRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", "-b", "main", remote)
	runGit(t, "", "init", "-b", "main", repo)
	configureGit(t, repo)
	writeFile(t, filepath.Join(repo, "README.md"), "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	base := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	runGit(t, repo, "remote", "set-head", "origin", "main")
	runGit(t, repo, "switch", "-c", "feature")
	writeFile(t, filepath.Join(repo, "docs/guide.md"), "changed\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "change")
	target := runGit(t, repo, "rev-parse", "HEAD")
	return repo, base, target
}

func configureGit(t *testing.T, repo string) {
	t.Helper()
	runGit(t, repo, "config", "user.email", "quality-test@example.com")
	runGit(t, repo, "config", "user.name", "Quality Test")
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	args := arguments
	if repo != "" {
		args = append([]string{"-C", repo}, arguments...)
	}
	command := exec.Command("git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}
