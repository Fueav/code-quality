package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type nativeRunOptions struct {
	Session           reviewsession.NativeSession
	Plan              reviewplan.Decision
	Goal              string
	Model             string
	ReasoningEffort   string
	ExecutionProfile  string
	Provider          Provider
	LeaseFile         *os.File
	SessionLockFile   *os.File
	OutputSchema      []byte
	HeartbeatInterval time.Duration
	ProgressWriter    io.Writer
}

func runNativeSession(ctx context.Context, options nativeRunOptions) (quality.NativeOutcome, error) {
	if err := normalizeRunOptions(&options); err != nil {
		return quality.NativeOutcome{}, err
	}
	captured, err := captureNativeEvidence(ctx, options)
	if err != nil {
		return quality.NativeOutcome{}, err
	}
	return quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request: options.Plan.Request, ProviderRequest: options.Plan.ProviderRequest,
		Identity: options.Plan.ReviewIdentity, PreviousBlockingFindings: options.Plan.PreviousBlockingFindings(),
		ReviewGoal: options.Goal,
	}, captured.finalMessage, captured.processErr)
}

func normalizeRunOptions(options *nativeRunOptions) error {
	request := options.Session.Request()
	if problems := quality.ValidateRequest(request); len(problems) > 0 {
		return fmt.Errorf("review request is invalid: %s", strings.Join(problems, "; "))
	}
	artifacts := options.Session.Artifacts()
	for name, value := range map[string]string{
		"session":           options.Session.Directory(),
		"repository":        options.Session.RepositoryDirectory(),
		"native review log": artifacts.FinalMessagePath(),
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}
	options.Goal = strings.TrimSpace(options.Goal)
	if len(options.Goal) > 4000 {
		return errors.New("review goal exceeds 4000 bytes")
	}
	if options.Provider == nil {
		options.Provider = NewCodexProvider("")
	}
	if err := validateProvider(options.Provider); err != nil {
		return err
	}
	if strings.TrimSpace(options.Model) == "" {
		options.Model = options.Provider.defaultModel()
	}
	if strings.TrimSpace(options.ReasoningEffort) == "" {
		options.ReasoningEffort = options.Provider.defaultReasoningEffort()
	}
	if strings.TrimSpace(options.ExecutionProfile) == "" {
		options.ExecutionProfile = quality.ExecutionProfilePersonal
	}
	if options.ExecutionProfile != quality.ExecutionProfilePersonal && options.ExecutionProfile != quality.ExecutionProfileProductionCI {
		return fmt.Errorf("unsupported execution profile %q", options.ExecutionProfile)
	}
	schema, err := reviewsession.ReadRegularFile(options.Session.OutputSchemaPath(), maxNativeOutputBytes)
	if err != nil {
		return fmt.Errorf("read native review output schema: %w", err)
	}
	options.OutputSchema = schema
	if err := options.Provider.validateReasoningEffort(options.ReasoningEffort); err != nil {
		return err
	}
	if options.Plan.Status == "" {
		contract, err := ResolveContract(options.Provider, options.Model, options.ReasoningEffort, options.ExecutionProfile, quality.ReviewScopeFull)
		if err != nil {
			return err
		}
		contract.Contract.ProviderOutputSchema = quality.SHA256Digest(schema)
		request := options.Session.Request()
		baseRef, headRef, baseTip := request.TargetBranch, request.TargetCommit, request.BaseCommit
		if request.Change != nil {
			baseRef, headRef, baseTip = request.Change.BaseRef, request.Change.HeadRef, request.Change.BaseTipCommit
		}
		identity, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
			Contract: contract.Contract, Request: request, ReviewGoal: options.Goal, ReviewScope: quality.ReviewScopeFull,
			BaseRef: baseRef, HeadRef: headRef, BaseTipCommit: baseTip, MergeBase: request.BaseCommit,
			CurrentHead: request.TargetCommit, DeltaChangedFiles: []string{},
		})
		if err != nil {
			return err
		}
		options.Plan = reviewplan.Decision{
			ReviewIdentity: identity, SchemaVersion: 1, Status: reviewplan.StatusReady,
			FullRequiredReasons: []string{}, ManualRequiredReasons: []string{}, Request: request, ProviderRequest: request, ProviderInvocations: 0,
		}
	}
	if options.Plan.Status != reviewplan.StatusReady {
		return errors.New("native provider requires a READY review plan")
	}
	if !reflect.DeepEqual(options.Session.Request(), options.Plan.ProviderRequest) {
		return errors.New("native session request does not match the provider plan")
	}
	contract := options.Plan.Contract
	if contract.ProviderHost != options.Provider.Host() || contract.Model != options.Model || contract.ReasoningEffort != options.ReasoningEffort || contract.ExecutionProfile != options.ExecutionProfile {
		return errors.New("native run options do not match the frozen review contract")
	}
	if contract.ProviderOutputSchema != quality.SHA256Digest(schema) {
		return errors.New("native output schema does not match the frozen review contract")
	}
	return nil
}

func buildReviewInvocation(options nativeRunOptions) reviewInvocation {
	invocation := options.Provider.buildInvocation(providerInvocationOptions{
		Session: options.Session, Plan: options.Plan, Goal: options.Goal, Model: options.Model,
		ReasoningEffort: options.ReasoningEffort, ExecutionProfile: options.ExecutionProfile,
		LeaseFile:       options.LeaseFile,
		SessionLockFile: options.SessionLockFile,
		OutputSchema:    options.OutputSchema,
	})
	invocation.stage = string(StateNativeRunning)
	invocation.attempt = 1
	invocation.heartbeatInterval = options.HeartbeatInterval
	invocation.progress = options.ProgressWriter
	return invocation
}

func buildReviewPrompt(request quality.ReviewRequest, goal string, reportOnlyBoundary bool) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the changes introduced by %s relative to %s for concrete defects introduced or worsened by this change.\n", request.TargetCommit, request.BaseCommit)
	if goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(goal))
	}
	if reportOnlyBoundary {
		prompt.WriteString("Report actionable findings only. Do not modify files, commit, push, deploy, or change external state.\n")
	}
	prompt.WriteString("Use the priority definitions embedded in the configured JSON Schema. Exclude pure style, naming, preference, ordinary maintainability, and scale speculation without evidence.\nReturn only the structured findings document required by the configured JSON Schema. Use repository-relative changed-file paths and the smallest useful line range. If no concrete defect exists, return an empty findings array.\n")
	return prompt.String()
}

func buildPlanReviewPrompt(plan reviewplan.Decision, goal string, reportOnlyBoundary bool) string {
	return buildPlanReviewPromptWithPrevious(plan, goal, reportOnlyBoundary, plan.PreviousBlockingFindings())
}

func buildPlanReviewPromptWithPrevious(plan reviewplan.Decision, goal string, reportOnlyBoundary bool, previous []quality.NativeFinding) string {
	if plan.ReviewScope != quality.ReviewScopeIncremental {
		return buildReviewPrompt(plan.ProviderRequest, goal, reportOnlyBoundary)
	}
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review only the committed incremental delta %s..%s inside the full pull-request scope %s..%s.\n", plan.ProviderRequest.BaseCommit, plan.ProviderRequest.TargetCommit, plan.Request.BaseCommit, plan.Request.TargetCommit)
	if goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(goal))
	}
	if reportOnlyBoundary {
		prompt.WriteString("Report actionable findings only. Do not modify files, commit, push, deploy, or change external state.\n")
	}
	previous = append([]quality.NativeFinding{}, previous...)
	sort.Slice(previous, func(i, j int) bool { return previous[i].ID < previous[j].ID })
	encoded, _ := json.Marshal(previous)
	fmt.Fprintf(&prompt, "Re-evaluate every previous P0/P1 finding below against the current head and return exactly one RESOLVED or UNRESOLVED resolution for each finding_id:\n%s\n", encoded)
	prompt.WriteString("Report new findings only when they are introduced or worsened by the incremental delta. New finding locations must be in the incremental changed files. An unresolved previous finding may point anywhere in the full pull-request changed files.\n")
	prompt.WriteString("Use the priority definitions embedded in the configured JSON Schema. Exclude pure style, naming, preference, ordinary maintainability, and scale speculation without evidence.\nReturn only the structured incremental document required by the configured JSON Schema. Use repository-relative paths and the smallest useful line range.\n")
	return prompt.String()
}

type publicationArtifact struct {
	path     string
	contents []byte
}

func publishNativeOutcome(session reviewsession.NativeSession, outcome quality.NativeOutcome, compatible ...quality.NativeOutcome) error {
	if err := outcome.ValidatePublication(); err != nil {
		return err
	}
	expected, err := renderPublicationArtifacts(session, outcome)
	if err != nil {
		return err
	}
	allowed := make([][]publicationArtifact, 0, len(compatible))
	for _, candidate := range compatible {
		if err := candidate.ValidatePublication(); err != nil {
			return err
		}
		artifacts, err := renderPublicationArtifacts(session, candidate)
		if err != nil {
			return err
		}
		allowed = append(allowed, artifacts)
	}
	for index, artifact := range expected {
		previous := make([][]byte, 0, len(allowed))
		for _, candidate := range allowed {
			previous = append(previous, candidate[index].contents)
		}
		if err := writeOrReplacePublicationFile(artifact.path, artifact.contents, previous...); err != nil {
			return fmt.Errorf("write %s: %w", filepath.Base(artifact.path), err)
		}
	}
	return nil
}

func renderPublicationArtifacts(session reviewsession.NativeSession, outcome quality.NativeOutcome) ([]publicationArtifact, error) {
	var resultJSON bytes.Buffer
	if err := outcome.EncodeJSON(&resultJSON); err != nil {
		return nil, err
	}
	var summaryJSON bytes.Buffer
	if err := quality.EncodeJSON(&summaryJSON, outcome.Summary()); err != nil {
		return nil, err
	}
	artifacts := session.Artifacts()
	markdown := []byte(outcome.Markdown())
	return []publicationArtifact{
		{path: artifacts.ResultPath(), contents: resultJSON.Bytes()},
		{path: artifacts.MarkdownPath(), contents: markdown},
		{path: artifacts.SummaryJSONPath(), contents: summaryJSON.Bytes()},
		{path: artifacts.SummaryMarkdownPath(), contents: markdown},
	}, nil
}

func writeOrReplacePublicationFile(path string, expected []byte, compatible ...[]byte) error {
	if err := writeExclusiveFile(path, expected); err == nil {
		return syncDirectory(filepath.Dir(path))
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validatePrivateRegularFile(info); err != nil || info.Mode().Perm() != 0o600 {
		if err != nil {
			return err
		}
		return errors.New("existing publication artifact is not owner-only mode 0600")
	}
	raw, err := reviewsession.ReadRegularFile(path, 16<<20)
	if err != nil {
		return err
	}
	if bytes.Equal(raw, expected) {
		return nil
	}
	matched := false
	for _, previous := range compatible {
		if bytes.Equal(raw, previous) {
			matched = true
			break
		}
	}
	if !matched {
		return errors.New("existing publication artifact does not match the recovered result")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".publication-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		_ = temporary.Close()
		if !installed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(expected); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	installed = true
	return syncDirectory(filepath.Dir(path))
}

func writeExclusiveFile(path string, contents []byte) error {
	return writeExclusiveEncoded(path, func(writer io.Writer) error {
		_, err := writer.Write(contents)
		return err
	})
}

func writeExclusiveJSON(path string, value any) error {
	return writeExclusiveEncoded(path, func(writer io.Writer) error {
		return quality.EncodeJSON(writer, value)
	})
}

func writeExclusiveEncoded(path string, encode func(io.Writer) error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := encode(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
