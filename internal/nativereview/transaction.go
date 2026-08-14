package nativereview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type NativeRunSummary struct {
	quality.NativeReleaseSummary
	SummaryPath string `json:"summary_path"`
	EvidenceDir string `json:"evidence_dir"`
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
	CodexBinary  string
	AcquireLease func() (io.Closer, *os.File, error)
}

type TransactionResult struct {
	Summary       NativeRunSummary
	Plan          reviewplan.Decision
	DirtyWorktree bool
	Warnings      []string
	ExitCode      int
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
	defer func() {
		if cleanupErr := session.Cleanup(); cleanupErr != nil && returnErr == nil {
			transaction.Warnings = append(transaction.Warnings, cleanupErr.Error())
		}
	}()

	outcome, err := runNativeSession(ctx, nativeRunOptions{
		Session: session, Plan: plan, Goal: options.Goal, Model: contract.Contract.Model,
		ReasoningEffort: contract.Contract.ReasoningEffort, ExecutionProfile: contract.Contract.ExecutionProfile,
		Provider: provider, LeaseFile: leaseFile,
	})
	if err != nil {
		return TransactionResult{}, atStage("run native review", 1, err)
	}
	outcome, err = runRestrictedAdjudication(ctx, restrictedRunOptions{
		Session: session, Plan: plan, Provider: provider, Model: contract.Contract.Model,
		ReasoningEffort: contract.Contract.ReasoningEffort, LeaseFile: leaseFile,
	}, outcome)
	if err != nil {
		return TransactionResult{}, atStage("run restricted adjudication", 1, err)
	}
	if err := publishNativeOutcome(session, outcome); err != nil {
		return TransactionResult{}, atStage("publish native review", 1, err)
	}

	exitCode := 0
	switch outcome.SemanticResult() {
	case quality.ResultBlock:
		exitCode = 3
	case quality.ResultError:
		exitCode = 1
	}
	artifacts := session.Artifacts()
	transaction = TransactionResult{
		Plan: plan,
		Summary: NativeRunSummary{
			NativeReleaseSummary: outcome.Summary(),
			SummaryPath:          artifacts.SummaryMarkdownPath(), EvidenceDir: session.Directory(),
		},
		DirtyWorktree: session.DirtyWorktree(),
		Warnings:      []string{},
		ExitCode:      exitCode,
	}
	return transaction, nil
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
