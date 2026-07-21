package intake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type Options struct {
	RepositoryPath string
	Base           string
	Target         string
	DiffReason     string
	Environment    map[string]string
}

type Result struct {
	Request         quality.ReviewRequest
	RepositoryRoot  string
	DirtyWorktree   bool
	DetectionSource string
}

type githubEvent struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		Base struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
}

func Discover(options Options) (Result, error) {
	repositoryPath := options.RepositoryPath
	if repositoryPath == "" {
		repositoryPath = "."
	}
	root, err := git(repositoryPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{}, fmt.Errorf("resolve Git repository: %w", err)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return Result{}, errors.New("Git repository root is empty")
	}

	environment := options.Environment
	if environment == nil {
		environment = currentEnvironment()
	}
	selection, err := selectBaseline(root, options, environment)
	if err != nil {
		return Result{RepositoryRoot: root}, err
	}
	base, err := resolveCommit(root, selection.base)
	if err != nil {
		return Result{RepositoryRoot: root}, fmt.Errorf("resolve base commit: %w", err)
	}
	target, err := resolveCommit(root, selection.target)
	if err != nil {
		return Result{RepositoryRoot: root}, fmt.Errorf("resolve target commit: %w", err)
	}
	if _, err := git(root, "merge-base", base, target); err != nil {
		return Result{RepositoryRoot: root}, fmt.Errorf("base and target have no merge base: %w", err)
	}
	changedFiles, err := changedFiles(root, base, target)
	if err != nil {
		return Result{RepositoryRoot: root}, err
	}
	dirty, err := dirtyWorktree(root)
	if err != nil {
		return Result{RepositoryRoot: root}, err
	}
	repository := selection.repository
	if repository == "" {
		repository = repositoryIdentity(root)
	}
	request := quality.ReviewRequest{
		Repository:          repository,
		TargetBranch:        selection.targetBranch,
		BaseCommit:          base,
		TargetCommit:        target,
		DiffSelectionReason: selection.reason,
		ChangedFiles:        changedFiles,
		AffectedEntries:     []string{},
	}
	if validationErrors := quality.ValidateRequest(request); len(validationErrors) > 0 {
		return Result{RepositoryRoot: root}, fmt.Errorf("generated review request is invalid: %s", strings.Join(validationErrors, "; "))
	}
	return Result{
		Request:         request,
		RepositoryRoot:  root,
		DirtyWorktree:   dirty,
		DetectionSource: selection.source,
	}, nil
}

type baseline struct {
	base         string
	target       string
	reason       string
	targetBranch string
	repository   string
	source       string
}

func selectBaseline(root string, options Options, environment map[string]string) (baseline, error) {
	explicitCount := 0
	for _, value := range []string{options.Base, options.Target, options.DiffReason} {
		if strings.TrimSpace(value) != "" {
			explicitCount++
		}
	}
	if explicitCount > 0 {
		if explicitCount != 3 {
			return baseline{}, errors.New("--base, --target, and --diff-reason must be provided together")
		}
		branch := currentBranch(root)
		return baseline{
			base: options.Base, target: options.Target, reason: options.DiffReason,
			targetBranch: branch, source: "explicit",
		}, nil
	}
	if eventPath := environment["GITHUB_EVENT_PATH"]; eventPath != "" && environment["GITHUB_EVENT_NAME"] == "pull_request" {
		return githubBaseline(eventPath)
	}
	if environment["CI_MERGE_REQUEST_IID"] != "" {
		base := environment["CI_MERGE_REQUEST_DIFF_BASE_SHA"]
		target := environment["CI_COMMIT_SHA"]
		branch := environment["CI_MERGE_REQUEST_TARGET_BRANCH_NAME"]
		if base == "" || target == "" || branch == "" {
			return baseline{}, errors.New("GitLab merge request environment is incomplete")
		}
		return baseline{
			base: base, target: target, reason: "gitlab_merge_request",
			targetBranch: branch, repository: environment["CI_PROJECT_PATH"], source: "gitlab",
		}, nil
	}
	return localBaseline(root)
}

func githubBaseline(path string) (baseline, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return baseline{}, fmt.Errorf("read GitHub event: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return baseline{}, errors.New("GitHub event must be a regular non-symlink file")
	}
	if info.Size() > 10<<20 {
		return baseline{}, errors.New("GitHub event exceeds 10 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return baseline{}, fmt.Errorf("read GitHub event: %w", err)
	}
	var event githubEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return baseline{}, fmt.Errorf("parse GitHub event: %w", err)
	}
	if event.PullRequest.Base.SHA == "" || event.PullRequest.Head.SHA == "" || event.PullRequest.Base.Ref == "" {
		return baseline{}, errors.New("GitHub pull request event is incomplete")
	}
	return baseline{
		base: event.PullRequest.Base.SHA, target: event.PullRequest.Head.SHA,
		reason: "github_pull_request", targetBranch: event.PullRequest.Base.Ref,
		repository: event.Repository.FullName, source: "github",
	}, nil
}

func localBaseline(root string) (baseline, error) {
	defaultRef, err := git(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return baseline{}, errors.New("cannot determine origin/HEAD; provide an explicit review baseline")
	}
	defaultRef = strings.TrimSpace(defaultRef)
	if defaultRef == "" {
		return baseline{}, errors.New("origin/HEAD is empty; provide an explicit review baseline")
	}
	base, err := git(root, "merge-base", defaultRef, "HEAD")
	if err != nil {
		return baseline{}, fmt.Errorf("resolve local merge base: %w", err)
	}
	branch := strings.TrimPrefix(defaultRef, "refs/remotes/origin/")
	return baseline{
		base: strings.TrimSpace(base), target: "HEAD", reason: "local_branch_increment",
		targetBranch: branch, source: "local_git",
	}, nil
}

func resolveCommit(root, value string) (string, error) {
	output, err := git(root, "rev-parse", "--verify", "--end-of-options", value+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func changedFiles(root, base, target string) ([]string, error) {
	command := exec.Command("git", "-C", root, "diff", "--name-only", "-z", base, target, "--")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("collect changed files: %w", err)
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		path := string(part)
		if filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("Git produced an invalid changed path %q", path)
		}
		if _, exists := seen[path]; !exists {
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func dirtyWorktree(root string) (bool, error) {
	output, err := git(root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("inspect working tree: %w", err)
	}
	return strings.TrimSpace(output) != "", nil
}

func currentBranch(root string) string {
	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		return "DETACHED"
	}
	return strings.TrimSpace(branch)
}

func repositoryIdentity(root string) string {
	remote, err := git(root, "remote", "get-url", "origin")
	if err == nil && strings.TrimSpace(remote) != "" {
		return strings.TrimSpace(remote)
	}
	return filepath.Base(root)
}

func git(root string, arguments ...string) (string, error) {
	args := append([]string{"-C", root}, arguments...)
	command := exec.Command("git", args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return string(output), nil
}

func currentEnvironment() map[string]string {
	result := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			result[key] = value
		}
	}
	return result
}
