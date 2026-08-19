package nativereview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type NativeRunSummary struct {
	quality.NativeReleaseSummary
	SummaryPath string        `json:"summary_path"`
	EvidenceDir string        `json:"evidence_dir"`
	Session     SessionStatus `json:"session"`
}

type TransactionOptions struct {
	RepositoryPath     string
	Base               string
	Target             string
	BaseRef            string
	HeadRef            string
	DiffReason         string
	ReviewScope        string
	PreviousResultPath string
	Goal               string
	Model              string
	ReasoningEffort    string
	ExecutionProfile   string
	OutputRoot         string
	Provider           Provider
	Environment        map[string]string
	// CodexBinary is retained for source compatibility. Prefer Provider.
	CodexBinary       string
	AcquireLease      func() (io.Closer, *os.File, error)
	NativeTimeout     time.Duration
	RestrictedTimeout time.Duration
	HeartbeatInterval time.Duration
	ProgressWriter    io.Writer
}

type TransactionResult struct {
	Summary       NativeRunSummary
	Plan          reviewplan.Decision
	DirtyWorktree bool
	Warnings      []string
	ExitCode      int
	Status        SessionStatus
}

type ResumeOptions struct {
	SessionDir        string
	Provider          Provider
	AcquireLease      func() (io.Closer, *os.File, error)
	RestrictedTimeout time.Duration
	HeartbeatInterval time.Duration
	ProgressWriter    io.Writer
}

type transactionError struct {
	stage    string
	exitCode int
	err      error
}

func (problem *transactionError) Error() string {
	return problem.stage + ": " + problem.err.Error()
}

func (problem *transactionError) Unwrap() error {
	return problem.err
}

func TransactionExitCode(err error) int {
	var problem *transactionError
	if errors.As(err, &problem) {
		return problem.exitCode
	}
	return 1
}

// RunTransaction owns the deterministic native review lifecycle. The lease is
// acquired before discovery and remains held until report publication and
// isolated-checkout cleanup have both completed.
func RunTransaction(ctx context.Context, options TransactionOptions) (transaction TransactionResult, returnErr error) {
	provider := options.Provider
	if provider == nil {
		provider = NewCodexProvider(options.CodexBinary)
	}
	if err := validateProvider(provider); err != nil {
		return TransactionResult{}, atStage("select native review provider", 2, err)
	}
	contract, err := ResolveContract(provider, options.Model, options.ReasoningEffort, options.ExecutionProfile, options.ReviewScope)
	if err != nil {
		return TransactionResult{}, atStage("freeze native review contract", 2, err)
	}
	parentContract, err := ResolveContract(provider, options.Model, options.ReasoningEffort, options.ExecutionProfile, quality.ReviewScopeFull)
	if err != nil {
		return TransactionResult{}, atStage("freeze parent review contract", 2, err)
	}
	acquireLease := options.AcquireLease
	if acquireLease == nil {
		acquireLease = func() (io.Closer, *os.File, error) {
			lease, err := AcquireNativeReviewLease()
			if err != nil {
				return nil, nil, err
			}
			return lease, lease.InheritedFile(), nil
		}
	}
	lease, leaseFile, err := acquireLease()
	if err != nil {
		return TransactionResult{}, atStage("acquire native review lease", 1, err)
	}
	if lease == nil {
		return TransactionResult{}, atStage("acquire native review lease", 1, errors.New("lease owner is required"))
	}
	defer func() { _ = lease.Close() }()

	plan, err := reviewplan.Build(ctx, reviewplan.Input{
		RepositoryPath:     options.RepositoryPath,
		Base:               options.Base,
		Target:             options.Target,
		BaseRef:            options.BaseRef,
		HeadRef:            options.HeadRef,
		DiffReason:         options.DiffReason,
		ReviewScope:        options.ReviewScope,
		PreviousResultPath: options.PreviousResultPath,
		ReviewGoal:         options.Goal,
		Environment:        options.Environment,
		Contract:           contract.Contract,
		ParentContract:     parentContract.Contract,
	})
	if err != nil {
		return TransactionResult{}, atStage("resolve review scope", 1, err)
	}
	if plan.Status == reviewplan.StatusFullRequired {
		return TransactionResult{Plan: plan, DirtyWorktree: plan.DirtyWorktree, Warnings: []string{}, ExitCode: 4}, nil
	}
	if plan.Status == reviewplan.StatusManualRequired {
		return TransactionResult{Plan: plan, DirtyWorktree: plan.DirtyWorktree, Warnings: []string{}, ExitCode: 5}, nil
	}
	outputRoot, err := resolveTransactionOutputRoot(options.OutputRoot, plan.RepositoryRoot())
	if err != nil {
		return TransactionResult{}, atStage("output root", 2, err)
	}
	session, err := reviewsession.PrepareNative(ctx, reviewsession.Options{
		RepositoryRoot:   plan.RepositoryRoot(),
		OutputRoot:       outputRoot,
		Host:             provider.Host(),
		Request:          plan.ProviderRequest,
		DirtyWorktree:    plan.DirtyWorktree,
		NativeSchemaName: contract.OutputSchemaName,
	})
	if err != nil {
		return TransactionResult{}, atStage("prepare native review", 1, err)
	}
	sessionLock, err := acquireSessionLock(session.Directory())
	if err != nil {
		_ = session.Cleanup()
		return TransactionResult{}, atStage("acquire review session lock", 1, err)
	}
	defer func() { _ = sessionLock.Close() }()
	defer func() {
		if cleanupErr := session.Cleanup(); cleanupErr != nil && returnErr == nil {
			transaction.Warnings = append(transaction.Warnings, cleanupErr.Error())
		}
	}()

	checkpoint, err := newSessionCheckpoint(session, plan, options.Goal)
	if err != nil {
		return TransactionResult{}, atStage("create native review checkpoint", 1, err)
	}
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		return TransactionResult{}, atStage("write planned checkpoint", 1, err)
	}
	checkpoint.State = StateNativeRunning
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		return TransactionResult{}, atStage("write native-running checkpoint", 1, err)
	}

	nativeCtx, cancelNative := withOptionalTimeout(ctx, options.NativeTimeout)
	outcome, err := runNativeSession(nativeCtx, nativeRunOptions{
		Session: session, Plan: plan, Goal: options.Goal, Model: contract.Contract.Model,
		ReasoningEffort: contract.Contract.ReasoningEffort, ExecutionProfile: contract.Contract.ExecutionProfile,
		Provider: provider, LeaseFile: leaseFile, SessionLockFile: sessionLock.inheritedFile(),
		HeartbeatInterval: options.HeartbeatInterval, ProgressWriter: options.ProgressWriter,
	})
	cancelNative()
	if err != nil {
		return TransactionResult{}, atStage("run native review", 1, err)
	}
	if err := checkpointNativeFrozen(&checkpoint, session, outcome); err != nil {
		return TransactionResult{}, atStage("freeze native checkpoint", 1, err)
	}
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		return TransactionResult{}, atStage("write native-frozen checkpoint", 1, err)
	}
	if len(outcome.BlockingFindings()) == 0 {
		return publishTransaction(session, plan, &checkpoint, outcome, 1, 0)
	}

	record := newAttemptRecord(1, false)
	checkpoint.RestrictedAttempts = append(checkpoint.RestrictedAttempts, record)
	checkpoint.State = StateRestrictedRunning
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		return TransactionResult{}, atStage("write restricted-running checkpoint", 1, err)
	}
	restrictedCtx, cancelRestricted := withOptionalTimeout(ctx, options.RestrictedTimeout)
	attempt, err := runRestrictedAttempt(restrictedCtx, restrictedRunOptions{
		Session: session, Plan: plan, Provider: provider, Model: contract.Contract.Model,
		ReasoningEffort: contract.Contract.ReasoningEffort, LeaseFile: leaseFile, SessionLockFile: sessionLock.inheritedFile(),
		Attempt: 1, Resumed: false, StartedAt: record.StartedAt,
		HeartbeatInterval: options.HeartbeatInterval, ProgressWriter: options.ProgressWriter,
	}, outcome)
	cancelRestricted()
	if err != nil {
		checkpoint.State = StateTerminalError
		checkpoint.LastFailure = &AttemptFailure{Class: FailureArtifactIntegrity, Reason: "restricted attempt evidence could not be frozen"}
		_ = writeCheckpoint(session.Directory(), &checkpoint)
		return TransactionResult{}, atStage("run restricted adjudication", 1, err)
	}
	checkpoint.RestrictedAttempts[0] = attempt.Record
	return finishRestrictedAttempt(session, plan, &checkpoint, attempt, false, 1, 1)
}

// ResumeRestricted is the only production entry point for continuing a
// verified Native-frozen session. It never runs Native review or changes scope.
func ResumeRestricted(ctx context.Context, options ResumeOptions) (transaction TransactionResult, returnErr error) {
	if !filepath.IsAbs(options.SessionDir) {
		return TransactionResult{Status: SessionStatus{SchemaVersion: 1, SessionDir: options.SessionDir}},
			atStage("resume restricted adjudication", 2, errors.New("session directory must be absolute"))
	}
	sessionDir := filepath.Clean(options.SessionDir)
	lock, err := acquireSessionLock(sessionDir)
	if err != nil {
		return TransactionResult{Status: SessionStatus{SchemaVersion: 1, SessionDir: sessionDir}}, err
	}
	defer func() { _ = lock.Close() }()
	checkpoint, err := loadAndVerifyCheckpoint(sessionDir)
	if err != nil {
		return TransactionResult{Status: SessionStatus{SchemaVersion: 1, SessionDir: sessionDir}},
			atStage("verify restricted resume checkpoint", 1, err)
	}
	terminal := statusFromCheckpoint(checkpoint, sessionDir, 0, 0)
	recoverRunning := false
	switch checkpoint.State {
	case StatePublished:
		return publishedTransactionFromCheckpoint(sessionDir, checkpoint, terminal)
	case StateManualRequired:
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 5}, nil
	case StateTerminalError:
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1}, nil
	case StateNativeFrozen, StateRestrictedRetryable:
	case StateRestrictedRunning:
		recoverRunning = true
	default:
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1},
			atStage("resume restricted adjudication", 1, fmt.Errorf("session state %s is not resumable", checkpoint.State))
	}
	if checkpoint.NativeOutcome == nil || len(checkpoint.FrozenBlockingFindings) == 0 {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1},
			atStage("resume restricted adjudication", 1, errors.New("session has no frozen Native P0/P1 outcome"))
	}
	provider := options.Provider
	if provider == nil {
		provider, err = ProviderForHost(checkpoint.Plan.Contract.ProviderHost)
		if err != nil {
			return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1}, err
		}
	}
	if err := validateProvider(provider); err != nil || provider.Host() != checkpoint.Plan.Contract.ProviderHost {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1},
			atStage("resume restricted adjudication", 1, errors.New("Provider does not match frozen contract"))
	}
	if len(checkpoint.RestrictedAttempts) >= 2 && !recoverRunning {
		checkpoint.State = StateManualRequired
		if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
			return TransactionResult{}, err
		}
		return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 5}, nil
	}
	acquireLease := options.AcquireLease
	if acquireLease == nil {
		acquireLease = func() (io.Closer, *os.File, error) {
			lease, err := AcquireNativeReviewLease()
			if err != nil {
				return nil, nil, err
			}
			return lease, lease.InheritedFile(), nil
		}
	}
	lease, leaseFile, err := acquireLease()
	if err != nil {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1}, err
	}
	if lease == nil {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1}, errors.New("lease owner is required")
	}
	defer func() { _ = lease.Close() }()
	session, err := reviewsession.ReopenNative(ctx, sessionDir)
	if err != nil {
		failureClass := FailureArtifactIntegrity
		failureReason := "retained review session could not be reopened"
		if errors.Is(err, reviewsession.ErrTargetCommitUnavailable) {
			failureClass = FailureTargetUnavailable
			failureReason = "frozen target commit is unavailable"
		}
		checkpoint.State = StateTerminalError
		checkpoint.LastFailure = &AttemptFailure{Class: failureClass, Reason: failureReason}
		_ = writeCheckpoint(sessionDir, &checkpoint)
		return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 1},
			atStage("rebuild restricted resume checkout", 1, err)
	}
	defer func() {
		if cleanupErr := session.Cleanup(); cleanupErr != nil && returnErr == nil {
			transaction.Warnings = append(transaction.Warnings, cleanupErr.Error())
		}
	}()
	if !reflect.DeepEqual(session.Request(), checkpoint.Plan.ProviderRequest) {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1},
			atStage("resume restricted adjudication", 1, errors.New("rebuilt session request does not match frozen provider scope"))
	}
	if session.SourceRepositoryRoot() != checkpoint.RepositoryRoot || session.ProviderHost() != checkpoint.Plan.Contract.ProviderHost {
		return TransactionResult{Plan: checkpoint.Plan, Status: terminal, ExitCode: 1},
			atStage("resume restricted adjudication", 1, errors.New("source repository or Provider host does not match frozen checkpoint"))
	}
	if err := session.VerifyTrustedDiff(ctx); err != nil {
		if ctx.Err() == nil {
			checkpoint.State = StateTerminalError
			checkpoint.LastFailure = &AttemptFailure{Class: FailureArtifactIntegrity, Reason: "trusted diff no longer matches the frozen Git object range"}
			_ = writeCheckpoint(sessionDir, &checkpoint)
		}
		return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 1},
			atStage("verify frozen trusted diff", 1, err)
	}
	outcome, err := revalidateFrozenNativeOutcome(session, provider, checkpoint)
	if err != nil {
		checkpoint.State = StateTerminalError
		checkpoint.LastFailure = &AttemptFailure{Class: FailureArtifactIntegrity, Reason: "frozen Native evidence no longer reproduces the checkpoint outcome"}
		_ = writeCheckpoint(sessionDir, &checkpoint)
		return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 1},
			atStage("revalidate frozen Native outcome", 1, err)
	}
	if !checkpoint.Resumed {
		resumeDigest := checkpoint.SessionDigest
		checkpoint.Resumed = true
		checkpoint.ResumedSessionDigest = &resumeDigest
	}
	if recoverRunning {
		attempt, err := recoverOrSealRunningAttempt(session, provider, &checkpoint, outcome)
		if err != nil {
			checkpoint.State = StateTerminalError
			checkpoint.LastFailure = &AttemptFailure{Class: FailureArtifactIntegrity, Reason: "interrupted restricted attempt evidence could not be verified"}
			_ = writeCheckpoint(sessionDir, &checkpoint)
			return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 1},
				atStage("seal interrupted restricted attempt", 1, err)
		}
		checkpoint.RestrictedAttempts[len(checkpoint.RestrictedAttempts)-1] = attempt.Record
		if attempt.Record.Status == "SUCCEEDED" {
			return finishRestrictedAttempt(session, checkpoint.Plan, &checkpoint, attempt, true, 0, 0)
		}
		checkpoint.LastFailure = cloneFailure(attempt.Record.Failure)
		switch {
		case len(checkpoint.RestrictedAttempts) >= 2:
			checkpoint.State = StateManualRequired
		case attempt.Record.Failure != nil && attempt.Record.Failure.Retryable:
			checkpoint.State = StateRestrictedRetryable
		default:
			checkpoint.State = StateTerminalError
		}
		if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
			return TransactionResult{}, atStage("write interrupted restricted checkpoint", 1, err)
		}
		if checkpoint.State != StateRestrictedRetryable {
			exitCode := 1
			if checkpoint.State == StateManualRequired {
				exitCode = 5
			}
			return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: exitCode}, nil
		}
	}
	if len(checkpoint.RestrictedAttempts) >= 2 {
		checkpoint.State = StateManualRequired
		if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
			return TransactionResult{}, err
		}
		return TransactionResult{Plan: checkpoint.Plan, Status: statusFromCheckpoint(checkpoint, sessionDir, 0, 0), ExitCode: 5}, nil
	}
	attemptNumber := len(checkpoint.RestrictedAttempts) + 1
	record := newAttemptRecord(attemptNumber, true)
	checkpoint.RestrictedAttempts = append(checkpoint.RestrictedAttempts, record)
	checkpoint.State = StateRestrictedRunning
	checkpoint.LastFailure = nil
	if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
		return TransactionResult{}, err
	}
	restrictedCtx, cancelRestricted := withOptionalTimeout(ctx, options.RestrictedTimeout)
	attempt, err := runRestrictedAttempt(restrictedCtx, restrictedRunOptions{
		Session: session, Plan: checkpoint.Plan, Provider: provider, Model: checkpoint.Plan.Contract.Model,
		ReasoningEffort: checkpoint.Plan.Contract.ReasoningEffort, LeaseFile: leaseFile, SessionLockFile: lock.inheritedFile(),
		Attempt: attemptNumber, Resumed: true, StartedAt: record.StartedAt,
		HeartbeatInterval: options.HeartbeatInterval, ProgressWriter: options.ProgressWriter,
	}, outcome)
	cancelRestricted()
	if err != nil {
		checkpoint.State = StateTerminalError
		checkpoint.LastFailure = &AttemptFailure{Class: FailureArtifactIntegrity, Reason: "restricted resume evidence could not be frozen"}
		_ = writeCheckpoint(sessionDir, &checkpoint)
		return TransactionResult{}, atStage("run resumed restricted adjudication", 1, err)
	}
	checkpoint.RestrictedAttempts[len(checkpoint.RestrictedAttempts)-1] = attempt.Record
	return finishRestrictedAttempt(session, checkpoint.Plan, &checkpoint, attempt, true, 0, 1)
}

func revalidateFrozenNativeOutcome(session reviewsession.NativeSession, provider Provider, checkpoint SessionCheckpoint) (quality.NativeOutcome, error) {
	frozen, err := loadFrozenNativeArtifacts(capturePathsFromSession(session), provider)
	if err != nil {
		return quality.NativeOutcome{}, err
	}
	if frozen.ProtocolError != nil {
		return quality.NativeOutcome{}, frozen.ProtocolError
	}
	rebuilt, err := quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{
		Request: checkpoint.Plan.Request, ProviderRequest: checkpoint.Plan.ProviderRequest,
		Identity: checkpoint.Plan.ReviewIdentity, PreviousBlockingFindings: checkpoint.PreviousBlockingFindings,
		ReviewGoal: checkpoint.ReviewGoal,
	}, frozen.FinalMessage, nil)
	if err != nil {
		return quality.NativeOutcome{}, err
	}
	if checkpoint.NativeOutcome == nil || !reflect.DeepEqual(rebuilt.Result(), *checkpoint.NativeOutcome) ||
		!reflect.DeepEqual(rebuilt.BlockingFindings(), checkpoint.FrozenBlockingFindings) {
		return quality.NativeOutcome{}, errors.New("frozen Native evidence does not reproduce the checkpoint outcome")
	}
	return rebuilt, nil
}

func finishRestrictedAttempt(session reviewsession.NativeSession, plan reviewplan.Decision, checkpoint *SessionCheckpoint, attempt restrictedAttemptResult, resumed bool, nativeThisRun, restrictedThisRun int) (TransactionResult, error) {
	if attempt.Record.Status == "SUCCEEDED" {
		adopted := attempt.Record.Attempt
		checkpoint.AdoptedRestrictedAttempt = &adopted
		checkpoint.LastFailure = nil
		outcome, compatible, err := outcomeForPublication(session, checkpoint, attempt.Outcome, adopted, resumed, attempt.Record.Resumed)
		if err != nil {
			return TransactionResult{}, atStage("record restricted attempt audit", 1, err)
		}
		return publishTransaction(session, plan, checkpoint, outcome, nativeThisRun, restrictedThisRun, compatible...)
	}
	checkpoint.LastFailure = cloneFailure(attempt.Record.Failure)
	if len(checkpoint.RestrictedAttempts) >= 2 {
		checkpoint.State = StateManualRequired
	} else if attempt.Record.Failure != nil && attempt.Record.Failure.Retryable {
		checkpoint.State = StateRestrictedRetryable
	} else {
		checkpoint.State = StateTerminalError
	}
	if err := writeCheckpoint(session.Directory(), checkpoint); err != nil {
		return TransactionResult{}, atStage("write restricted failure checkpoint", 1, err)
	}
	exitCode := 1
	if checkpoint.State == StateManualRequired {
		exitCode = 5
	}
	return TransactionResult{
		Plan: plan, Status: statusFromCheckpoint(*checkpoint, session.Directory(), nativeThisRun, restrictedThisRun),
		DirtyWorktree: session.DirtyWorktree(), Warnings: []string{}, ExitCode: exitCode,
	}, nil
}

func outcomeForPublication(session reviewsession.NativeSession, checkpoint *SessionCheckpoint, base quality.NativeOutcome, adopted int, resumed, attemptResumed bool) (quality.NativeOutcome, []quality.NativeOutcome, error) {
	expected, err := base.WithAttemptAudit(len(checkpoint.RestrictedAttempts), adopted, resumed, checkpoint.ResumedSessionDigest)
	if err != nil {
		return quality.NativeOutcome{}, nil, err
	}
	compatible := []quality.NativeOutcome{}
	if resumed && !attemptResumed {
		unresumed, err := base.WithAttemptAudit(len(checkpoint.RestrictedAttempts), adopted, false, nil)
		if err != nil {
			return quality.NativeOutcome{}, nil, err
		}
		compatible = append(compatible, unresumed)
	}
	resultPath := session.Artifacts().ResultPath()
	raw, err := reviewsession.ReadRegularFile(resultPath, 16<<20)
	if errors.Is(err, os.ErrNotExist) {
		return expected, compatible, nil
	}
	if err != nil {
		return quality.NativeOutcome{}, nil, fmt.Errorf("read partial publication result: %w", err)
	}
	existingResult, err := quality.DecodeStrict[quality.NativeReviewResult](bytes.NewReader(raw))
	if err != nil {
		return quality.NativeOutcome{}, nil, fmt.Errorf("decode partial publication result: %w", err)
	}
	if reflect.DeepEqual(existingResult, expected.Result()) {
		return expected, compatible, nil
	}
	for _, candidate := range compatible {
		if reflect.DeepEqual(existingResult, candidate.Result()) {
			return expected, compatible, nil
		}
	}
	return quality.NativeOutcome{}, nil, errors.New("partial publication result does not match the completed restricted attempt")
}

func publishTransaction(session reviewsession.NativeSession, plan reviewplan.Decision, checkpoint *SessionCheckpoint, outcome quality.NativeOutcome, nativeThisRun, restrictedThisRun int, compatible ...quality.NativeOutcome) (TransactionResult, error) {
	if err := publishNativeOutcome(session, outcome, compatible...); err != nil {
		return TransactionResult{}, atStage("publish native review", 1, err)
	}
	if err := checkpointPublication(checkpoint, session); err != nil {
		return TransactionResult{}, atStage("bind native review publication", 1, err)
	}
	if err := writeCheckpoint(session.Directory(), checkpoint); err != nil {
		return TransactionResult{}, atStage("write published checkpoint", 1, err)
	}
	exitCode := outcomeExitCode(outcome.SemanticResult())
	artifacts := session.Artifacts()
	status := statusFromCheckpoint(*checkpoint, session.Directory(), nativeThisRun, restrictedThisRun)
	return TransactionResult{
		Plan:          plan,
		Summary:       NativeRunSummary{NativeReleaseSummary: outcome.Summary(), SummaryPath: artifacts.SummaryMarkdownPath(), EvidenceDir: session.Directory(), Session: status},
		Status:        status,
		DirtyWorktree: session.DirtyWorktree(), Warnings: []string{}, ExitCode: exitCode,
	}, nil
}

func publishedTransactionFromCheckpoint(sessionDir string, checkpoint SessionCheckpoint, status SessionStatus) (TransactionResult, error) {
	raw, err := reviewsession.ReadRegularFile(filepath.Join(sessionDir, "output", "review-result.json"), 16<<20)
	if err != nil {
		return TransactionResult{Plan: checkpoint.Plan, Status: status, ExitCode: 1}, err
	}
	result, err := quality.DecodeStrict[quality.NativeReviewResult](bytes.NewReader(raw))
	if err != nil {
		return TransactionResult{Plan: checkpoint.Plan, Status: status, ExitCode: 1}, err
	}
	outcome, err := quality.RestoreNativeOutcome(result)
	if err != nil {
		return TransactionResult{Plan: checkpoint.Plan, Status: status, ExitCode: 1}, err
	}
	return TransactionResult{
		Plan: checkpoint.Plan, Status: status, ExitCode: outcomeExitCode(outcome.SemanticResult()),
		Summary:  NativeRunSummary{NativeReleaseSummary: outcome.Summary(), SummaryPath: filepath.Join(sessionDir, "output", "review-summary.md"), EvidenceDir: sessionDir, Session: status},
		Warnings: []string{},
	}, nil
}

func outcomeExitCode(result string) int {
	switch result {
	case quality.ResultBlock:
		return 3
	case quality.ResultError:
		return 1
	default:
		return 0
	}
}

func withOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func atStage(stage string, exitCode int, err error) error {
	return &transactionError{stage: stage, exitCode: exitCode, err: err}
}

func resolveTransactionOutputRoot(value, repositoryRoot string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return DefaultOutputRoot(repositoryRoot)
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("output root must be an absolute path outside the reviewed repository")
	}
	canonicalRoot, err := canonicalPath(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize output root: %w", err)
	}
	if err := rejectOutputRootInsideRepository(canonicalRoot, repositoryRoot); err != nil {
		return "", err
	}
	return canonicalRoot, nil
}

// DefaultOutputRoot creates a private root in the sandbox-writable system temp
// area and rejects any root that would fall inside the reviewed repository.
func DefaultOutputRoot(repositoryRoot string) (string, error) {
	temporaryDirectory, err := canonicalPath(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("resolve system temporary directory for output: %w", err)
	}
	root, err := os.MkdirTemp(temporaryDirectory, fmt.Sprintf("code-quality-%d-", os.Getuid()))
	if err != nil {
		return "", fmt.Errorf("create private output root: %w", err)
	}
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		_ = os.Remove(root)
		return "", fmt.Errorf("canonicalize private output root: %w", err)
	}
	if err := rejectOutputRootInsideRepository(canonicalRoot, repositoryRoot); err != nil {
		_ = os.Remove(root)
		return "", err
	}
	return canonicalRoot, nil
}

func rejectOutputRootInsideRepository(canonicalRoot, repositoryRoot string) error {
	canonicalRepository, err := canonicalPath(repositoryRoot)
	if err != nil {
		return fmt.Errorf("canonicalize reviewed repository for output: %w", err)
	}
	inside, err := pathWithin(canonicalRepository, canonicalRoot)
	if err != nil {
		return err
	}
	if inside {
		return errors.New("output root would be inside the reviewed repository; provide an absolute --output-root outside the repository")
	}
	return nil
}

func pathWithin(parent, candidate string) (bool, error) {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false, fmt.Errorf("compare output root with reviewed repository: %w", err)
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}
