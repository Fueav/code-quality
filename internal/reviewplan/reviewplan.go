package reviewplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

const (
	StatusReady        = "READY"
	StatusFullRequired = "FULL_REQUIRED"
	maxPreviousResult  = int64(8 << 20)
)

type Input struct {
	RepositoryPath     string
	Base               string
	Target             string
	BaseRef            string
	HeadRef            string
	DiffReason         string
	ReviewScope        string
	PreviousResultPath string
	ReviewGoal         string
	Environment        map[string]string
	Contract           quality.NativeReviewContract
	ParentContract     quality.NativeReviewContract
}

type Decision struct {
	quality.ReviewIdentity
	SchemaVersion       int                   `json:"schema_version"`
	Status              string                `json:"status"`
	FullRequiredReasons []string              `json:"full_required_reasons"`
	Request             quality.ReviewRequest `json:"request"`
	ProviderRequest     quality.ReviewRequest `json:"provider_request"`
	ProviderInvocations int                   `json:"provider_invocations"`
	DirtyWorktree       bool                  `json:"dirty_worktree"`
	DetectionSource     string                `json:"detection_source"`
	repositoryRoot      string
	previousResult      *quality.NativeReviewResult
	previousBlockers    []quality.NativeFinding
}

func (decision Decision) RepositoryRoot() string { return decision.repositoryRoot }

func (decision Decision) PreviousResult() *quality.NativeReviewResult {
	if decision.previousResult == nil {
		return nil
	}
	copy := cloneNativeResult(*decision.previousResult)
	return &copy
}

func (decision Decision) PreviousBlockingFindings() []quality.NativeFinding {
	return append([]quality.NativeFinding{}, decision.previousBlockers...)
}

type githubEvent struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Base    struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
}

type baseline struct {
	baseSpec             string
	headSpec             string
	baseRef              string
	headRef              string
	reason               string
	targetBranch         string
	repository           string
	source               string
	normalizeMergeBase   bool
	stableChangeIdentity bool
	change               *quality.ChangeContext
}

func Build(ctx context.Context, input Input) (Decision, error) {
	root, environment, scope, selection, err := initializeBuild(ctx, input)
	if err != nil {
		return Decision{}, err
	}
	_ = environment
	baseTip, err := resolveCommit(ctx, root, selection.baseSpec)
	if err != nil {
		return Decision{}, fmt.Errorf("resolve base commit: %w", err)
	}
	currentHead, err := resolveCommit(ctx, root, selection.headSpec)
	if err != nil {
		return Decision{}, fmt.Errorf("resolve head commit: %w", err)
	}
	mergeBase, err := gitOutput(ctx, root, "merge-base", baseTip, currentHead)
	if err != nil {
		return Decision{}, fmt.Errorf("base and head have no merge base: %w", err)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	fullBase := baseTip
	if selection.normalizeMergeBase {
		fullBase = mergeBase
		if selection.change != nil {
			selection.change.BaseTipCommit = baseTip
		}
	}
	fullFiles, err := changedFiles(ctx, root, fullBase, currentHead)
	if err != nil {
		return Decision{}, err
	}
	if len(fullFiles) == 0 {
		return Decision{}, errors.New("no committed changes found between base and head")
	}
	dirty, err := dirtyWorktree(ctx, root)
	if err != nil {
		return Decision{}, err
	}
	repository := selection.repository
	if repository == "" {
		repository = repositoryIdentity(ctx, root)
	}
	request := quality.ReviewRequest{
		Repository: repository, TargetBranch: selection.targetBranch,
		BaseCommit: fullBase, TargetCommit: currentHead, DiffSelectionReason: selection.reason,
		ChangedFiles: fullFiles, AffectedEntries: []string{}, Change: selection.change,
	}
	if problems := quality.ValidateRequest(request); len(problems) > 0 {
		return Decision{}, fmt.Errorf("generated review request is invalid: %s", strings.Join(problems, "; "))
	}
	common := Decision{
		SchemaVersion: 1, Status: StatusReady, FullRequiredReasons: []string{},
		Request: request, ProviderRequest: cloneRequest(request), ProviderInvocations: 0,
		DirtyWorktree: dirty, DetectionSource: selection.source, repositoryRoot: root,
	}
	if scope == quality.ReviewScopeFull {
		identity, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
			Contract: input.Contract, Request: request, ReviewGoal: input.ReviewGoal,
			ReviewScope: scope, BaseRef: selection.baseRef, HeadRef: selection.headRef,
			BaseTipCommit: baseTip, MergeBase: mergeBase, CurrentHead: currentHead,
			DeltaChangedFiles: []string{},
		})
		if err != nil {
			return Decision{}, fmt.Errorf("build FULL review identity: %w", err)
		}
		common.ReviewIdentity = identity
		common.previousBlockers = []quality.NativeFinding{}
		return common, nil
	}
	return buildIncrementalDecision(ctx, input, common, selection, baseTip, mergeBase, currentHead)
}

func initializeBuild(ctx context.Context, input Input) (string, map[string]string, string, baseline, error) {
	repositoryPath := strings.TrimSpace(input.RepositoryPath)
	if repositoryPath == "" {
		repositoryPath = "."
	}
	root, err := gitOutput(ctx, repositoryPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, "", baseline{}, fmt.Errorf("resolve Git repository: %w", err)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil, "", baseline{}, errors.New("Git repository root is empty")
	}
	scope := strings.ToUpper(strings.TrimSpace(input.ReviewScope))
	if scope == "" {
		scope = quality.ReviewScopeFull
	}
	if scope != quality.ReviewScopeFull && scope != quality.ReviewScopeIncremental {
		return "", nil, "", baseline{}, fmt.Errorf("unsupported review scope %q; use full or incremental", input.ReviewScope)
	}
	previousProvided := strings.TrimSpace(input.PreviousResultPath) != ""
	if scope == quality.ReviewScopeIncremental && !previousProvided {
		return "", nil, "", baseline{}, errors.New("--review-scope incremental requires --previous-result")
	}
	if scope == quality.ReviewScopeFull && previousProvided {
		return "", nil, "", baseline{}, errors.New("--previous-result is only valid with --review-scope incremental")
	}
	environment := input.Environment
	if environment == nil {
		environment = currentEnvironment()
	}
	selection, err := selectBaseline(ctx, root, input, environment, scope)
	if err != nil {
		return "", nil, "", baseline{}, err
	}
	return root, environment, scope, selection, nil
}

func buildIncrementalDecision(ctx context.Context, input Input, decision Decision, selection baseline, baseTip, mergeBase, currentHead string) (Decision, error) {
	currentContractDigest, err := quality.BuildNativeReviewContractDigest(input.Contract)
	if err != nil {
		return Decision{}, fmt.Errorf("build current contract digest: %w", err)
	}
	decision.ReviewIdentity = quality.ReviewIdentity{
		ContractDigest: currentContractDigest, ReviewScope: quality.ReviewScopeIncremental,
		BaseRef: selection.baseRef, HeadRef: selection.headRef, BaseTipCommit: baseTip,
		MergeBase: mergeBase, CurrentHead: currentHead, DeltaChangedFiles: []string{}, Contract: input.Contract,
	}
	if !selection.stableChangeIdentity {
		return fullRequired(decision, "stable_change_identity_required"), nil
	}
	previous, err := readPreviousResult(input.PreviousResultPath)
	if err != nil {
		return fullRequired(decision, "previous_result_invalid"), nil
	}
	parentContract := input.Contract
	if previous.ReviewScope == quality.ReviewScopeFull {
		parentContract = input.ParentContract
		if parentContract.ToolVersion == "" {
			parentContract = input.Contract
		}
	}
	parentContractDigest, err := quality.BuildNativeReviewContractDigest(parentContract)
	if err != nil {
		return Decision{}, fmt.Errorf("build parent contract digest: %w", err)
	}
	reasons := []string{}
	if previous.Adjudication.SemanticResult == quality.ResultError {
		reasons = append(reasons, "previous_result_not_reviewable")
	}
	if previous.Request.Repository != decision.Request.Repository {
		reasons = append(reasons, "repository_changed")
	}
	if previous.BaseRef != selection.baseRef {
		reasons = append(reasons, "base_ref_changed")
	}
	if previous.HeadRef != selection.headRef {
		reasons = append(reasons, "head_ref_changed")
	}
	if previous.BaseTipCommit != baseTip {
		reasons = append(reasons, "base_tip_changed")
	}
	if strings.TrimSpace(previous.ReviewGoal) != strings.TrimSpace(input.ReviewGoal) {
		reasons = append(reasons, "review_goal_changed")
	}
	if previous.ContractDigest != parentContractDigest {
		reasons = append(reasons, "review_contract_changed")
	}
	if previous.CurrentHead == currentHead {
		reasons = append(reasons, "delta_empty")
	} else {
		ancestor, ancestorErr := isAncestor(ctx, decision.repositoryRoot, previous.CurrentHead, currentHead)
		if ancestorErr != nil || !ancestor {
			reasons = append(reasons, "previous_head_not_ancestor")
		}
	}
	var deltaFiles []string
	if len(reasons) == 0 {
		deltaFiles, err = changedFiles(ctx, decision.repositoryRoot, previous.CurrentHead, currentHead)
		if err != nil {
			return Decision{}, fmt.Errorf("collect incremental changed files: %w", err)
		}
		if len(deltaFiles) == 0 {
			reasons = append(reasons, "delta_empty")
		}
	}
	if len(reasons) > 0 {
		return fullRequired(decision, reasons...), nil
	}
	parentKey := previous.ReviewKey
	previousHead := previous.CurrentHead
	identity, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
		Contract: input.Contract, Request: decision.Request, ReviewGoal: input.ReviewGoal,
		ReviewScope: quality.ReviewScopeIncremental, BaseRef: selection.baseRef, HeadRef: selection.headRef,
		BaseTipCommit: baseTip, MergeBase: mergeBase, ParentReviewKey: &parentKey,
		PreviousHead: &previousHead, CurrentHead: currentHead, DeltaChangedFiles: deltaFiles,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("build INCREMENTAL review identity: %w", err)
	}
	providerRequest := cloneRequest(decision.Request)
	providerRequest.BaseCommit = previousHead
	providerRequest.DiffSelectionReason = "incremental_from_previous_result"
	providerRequest.ChangedFiles = append([]string(nil), deltaFiles...)
	decision.ReviewIdentity = identity
	decision.ProviderRequest = providerRequest
	decision.previousResult = &previous
	decision.previousBlockers = blockingFindings(previous.Findings)
	return decision, nil
}

func fullRequired(decision Decision, reasons ...string) Decision {
	decision.Status = StatusFullRequired
	decision.ProviderInvocations = 0
	decision.FullRequiredReasons = sortedUnique(reasons)
	decision.previousBlockers = []quality.NativeFinding{}
	return decision
}

func selectBaseline(ctx context.Context, root string, input Input, environment map[string]string, scope string) (baseline, error) {
	baseProvided := strings.TrimSpace(input.Base) != ""
	targetProvided := strings.TrimSpace(input.Target) != ""
	baseRefProvided := strings.TrimSpace(input.BaseRef) != ""
	headRefProvided := strings.TrimSpace(input.HeadRef) != ""
	reason := strings.TrimSpace(input.DiffReason)
	if baseProvided != targetProvided {
		return baseline{}, errors.New("--base and --target must be provided together")
	}
	if baseRefProvided != headRefProvided {
		return baseline{}, errors.New("--base-ref and --head-ref must be provided together")
	}
	if baseProvided && baseRefProvided {
		return baseline{}, errors.New("--base/--target cannot be mixed with --base-ref/--head-ref")
	}
	if !baseProvided && !baseRefProvided && reason != "" {
		return baseline{}, errors.New("--diff-reason requires an explicit commit or ref range")
	}
	if baseProvided {
		if scope == quality.ReviewScopeIncremental {
			return baseline{}, errors.New("legacy --base/--target ranges support FULL reviews only")
		}
		if reason == "" {
			reason = "explicit_commit_range"
		}
		return baseline{
			baseSpec: input.Base, headSpec: input.Target, baseRef: strings.TrimSpace(input.Base), headRef: strings.TrimSpace(input.Target),
			reason: reason, targetBranch: currentBranch(ctx, root), source: "explicit_commits",
		}, nil
	}
	if baseRefProvided {
		if reason == "" {
			reason = "explicit_ref_range"
		}
		return baseline{
			baseSpec: input.BaseRef, headSpec: input.HeadRef,
			baseRef: canonicalRef(input.BaseRef), headRef: canonicalRef(input.HeadRef),
			reason: reason, targetBranch: canonicalRef(input.BaseRef), source: "explicit_refs",
			normalizeMergeBase: true, stableChangeIdentity: true,
		}, nil
	}
	if eventPath := environment["GITHUB_EVENT_PATH"]; eventPath != "" && environment["GITHUB_EVENT_NAME"] == "pull_request" {
		return githubBaseline(eventPath, environment)
	}
	if environment["CI_MERGE_REQUEST_IID"] != "" {
		base := environment["CI_MERGE_REQUEST_DIFF_BASE_SHA"]
		head := environment["CI_COMMIT_SHA"]
		baseBranch := environment["CI_MERGE_REQUEST_TARGET_BRANCH_NAME"]
		headBranch := environment["CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"]
		if base == "" || head == "" || baseBranch == "" || headBranch == "" {
			return baseline{}, errors.New("GitLab merge request environment is incomplete")
		}
		return baseline{
			baseSpec: base, headSpec: head, baseRef: canonicalRef(baseBranch), headRef: canonicalRef(headBranch),
			reason: "gitlab_merge_request", targetBranch: canonicalRef(baseBranch),
			repository: normalizeRepositoryIdentity(environment["CI_PROJECT_PATH"]), source: "gitlab",
			normalizeMergeBase: true, stableChangeIdentity: true,
			change: &quality.ChangeContext{
				Kind: "merge_request", ID: environment["CI_MERGE_REQUEST_IID"],
				BaseRef: canonicalRef(baseBranch), HeadRef: canonicalRef(headBranch),
				URL: environment["CI_MERGE_REQUEST_PROJECT_URL"], RunURL: environment["CI_JOB_URL"],
			},
		}, nil
	}
	return localBaseline(ctx, root)
}

func githubBaseline(path string, environment map[string]string) (baseline, error) {
	raw, err := readRegularFile(path, 10<<20)
	if err != nil {
		return baseline{}, fmt.Errorf("read GitHub event: %w", err)
	}
	var event githubEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return baseline{}, fmt.Errorf("parse GitHub event: %w", err)
	}
	if event.PullRequest.Number <= 0 || event.PullRequest.Base.SHA == "" || event.PullRequest.Head.SHA == "" || event.PullRequest.Base.Ref == "" || event.PullRequest.Head.Ref == "" {
		return baseline{}, errors.New("GitHub pull request event is incomplete")
	}
	runURL := ""
	if environment["GITHUB_SERVER_URL"] != "" && environment["GITHUB_REPOSITORY"] != "" && environment["GITHUB_RUN_ID"] != "" {
		runURL = strings.TrimRight(environment["GITHUB_SERVER_URL"], "/") + "/" + environment["GITHUB_REPOSITORY"] + "/actions/runs/" + environment["GITHUB_RUN_ID"]
	}
	baseRef := canonicalRef(event.PullRequest.Base.Ref)
	headRef := canonicalRef(event.PullRequest.Head.Ref)
	return baseline{
		baseSpec: event.PullRequest.Base.SHA, headSpec: event.PullRequest.Head.SHA,
		baseRef: baseRef, headRef: headRef, reason: "github_pull_request", targetBranch: baseRef,
		repository: normalizeRepositoryIdentity(event.Repository.FullName), source: "github",
		normalizeMergeBase: true, stableChangeIdentity: true,
		change: &quality.ChangeContext{
			Kind: "pull_request", ID: strconv.Itoa(event.PullRequest.Number), BaseRef: baseRef, HeadRef: headRef,
			URL: event.PullRequest.HTMLURL, RunURL: runURL,
		},
	}, nil
}

func localBaseline(ctx context.Context, root string) (baseline, error) {
	defaultRef, err := gitOutput(ctx, root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return baseline{}, errors.New("cannot determine origin/HEAD; provide an explicit review baseline")
	}
	defaultRef = strings.TrimSpace(defaultRef)
	if defaultRef == "" {
		return baseline{}, errors.New("origin/HEAD is empty; provide an explicit review baseline")
	}
	baseRef := canonicalRef(defaultRef)
	headRef := currentBranch(ctx, root)
	return baseline{
		baseSpec: defaultRef, headSpec: "HEAD", baseRef: baseRef, headRef: canonicalRef(headRef),
		reason: "local_branch_increment", targetBranch: baseRef, source: "local_git", normalizeMergeBase: true,
	}, nil
}

func readPreviousResult(path string) (quality.NativeReviewResult, error) {
	raw, err := readRegularFile(path, maxPreviousResult)
	if err != nil {
		return quality.NativeReviewResult{}, err
	}
	result, err := quality.DecodeStrict[quality.NativeReviewResult](bytes.NewReader(raw))
	if err != nil {
		return quality.NativeReviewResult{}, err
	}
	if problems := quality.ValidateNativeResult(result); len(problems) > 0 {
		return quality.NativeReviewResult{}, errors.New(strings.Join(problems, "; "))
	}
	return result, nil
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input must be a regular non-symlink file")
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > limit {
		return nil, errors.New("input changed while it was being read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != after.Size() || int64(len(raw)) > limit {
		return nil, errors.New("input changed while it was being read")
	}
	return raw, nil
}

func resolveCommit(ctx context.Context, root, value string) (string, error) {
	output, err := gitOutput(ctx, root, "rev-parse", "--verify", "--end-of-options", value+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func changedFiles(ctx context.Context, root, base, head string) ([]string, error) {
	command := exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", "-z", base, head, "--")
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
		if filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
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

func dirtyWorktree(ctx context.Context, root string) (bool, error) {
	output, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("inspect working tree: %w", err)
	}
	return strings.TrimSpace(output) != "", nil
}

func currentBranch(ctx context.Context, root string) string {
	branch, err := gitOutput(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		return "DETACHED"
	}
	return strings.TrimSpace(branch)
}

func repositoryIdentity(ctx context.Context, root string) string {
	remote, err := gitOutput(ctx, root, "remote", "get-url", "origin")
	if err == nil && strings.TrimSpace(remote) != "" {
		return normalizeRepositoryIdentity(remote)
	}
	return filepath.Base(root)
}

func normalizeRepositoryIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		if parsed.Path != "" {
			value = parsed.Path
		}
	} else if at := strings.LastIndex(value, "@"); at >= 0 {
		if colon := strings.Index(value[at+1:], ":"); colon >= 0 {
			value = value[at+1+colon+1:]
		}
	}
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	if strings.Contains(value, string(filepath.Separator)) {
		cleaned := filepath.ToSlash(filepath.Clean(value))
		if filepath.IsAbs(value) {
			return filepath.Base(cleaned)
		}
		return cleaned
	}
	return value
}

func canonicalRef(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "refs/heads/")
	if strings.HasPrefix(value, "refs/remotes/") {
		value = strings.TrimPrefix(value, "refs/remotes/")
		if _, remainder, found := strings.Cut(value, "/"); found {
			value = remainder
		}
	}
	value = strings.TrimPrefix(value, "origin/")
	return value
}

func isAncestor(ctx context.Context, root, ancestor, descendant string) (bool, error) {
	command := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", ancestor, descendant)
	err := command.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func gitOutput(ctx context.Context, root string, arguments ...string) (string, error) {
	args := append([]string{"-C", root}, arguments...)
	command := exec.CommandContext(ctx, "git", args...)
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

func blockingFindings(findings []quality.NativeFinding) []quality.NativeFinding {
	result := []quality.NativeFinding{}
	for _, finding := range findings {
		if finding.Priority == 0 || finding.Priority == 1 {
			result = append(result, finding)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sortedUnique(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cloneRequest(request quality.ReviewRequest) quality.ReviewRequest {
	request.ChangedFiles = append([]string{}, request.ChangedFiles...)
	request.AffectedEntries = append([]string{}, request.AffectedEntries...)
	if request.Change != nil {
		change := *request.Change
		request.Change = &change
	}
	return request
}

func cloneNativeResult(result quality.NativeReviewResult) quality.NativeReviewResult {
	if result.ParentReviewKey != nil {
		value := *result.ParentReviewKey
		result.ParentReviewKey = &value
	}
	if result.PreviousHead != nil {
		value := *result.PreviousHead
		result.PreviousHead = &value
	}
	result.Request = cloneRequest(result.Request)
	result.DeltaChangedFiles = append([]string{}, result.DeltaChangedFiles...)
	result.Findings = append([]quality.NativeFinding{}, result.Findings...)
	result.PreviousBlockingFindings = append([]quality.NativeFinding{}, result.PreviousBlockingFindings...)
	result.NewFindings = append([]quality.NativeFinding{}, result.NewFindings...)
	result.PreviousFindingResolutions = append([]quality.PreviousFindingResolution{}, result.PreviousFindingResolutions...)
	for index := range result.PreviousFindingResolutions {
		if result.PreviousFindingResolutions[index].CurrentFinding != nil {
			finding := *result.PreviousFindingResolutions[index].CurrentFinding
			result.PreviousFindingResolutions[index].CurrentFinding = &finding
		}
	}
	result.Execution.AdapterDrops = append([]quality.AdapterDrop{}, result.Execution.AdapterDrops...)
	result.Adjudication.Reasons = append([]string(nil), result.Adjudication.Reasons...)
	return result
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
