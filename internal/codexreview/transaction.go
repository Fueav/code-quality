package codexreview

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fueav/code-quality/internal/intake"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type NativeRunSummary struct {
	SchemaVersion  int    `json:"schema_version"`
	Status         string `json:"status"`
	SemanticResult string `json:"semantic_result"`
	SessionDir     string `json:"session_dir"`
	ResultPath     string `json:"result_path"`
	MarkdownPath   string `json:"markdown_path"`
	FreezePath     string `json:"freeze_path"`
	MetricsPath    string `json:"metrics_path"`
	ModelCalls     int    `json:"model_calls"`
}

type TransactionOptions struct {
	RepositoryPath  string
	Base            string
	Target          string
	DiffReason      string
	Goal            string
	Model           string
	ReasoningEffort string
	OutputRoot      string
	CodexBinary     string
	AcquireLease    func() (io.Closer, *os.File, error)
}

type TransactionResult struct {
	Summary       NativeRunSummary
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

	discovered, err := intake.Discover(intake.Options{
		RepositoryPath: options.RepositoryPath,
		Base:           options.Base,
		Target:         options.Target,
		DiffReason:     options.DiffReason,
	})
	if err != nil {
		return TransactionResult{}, atStage("resolve review scope", 1, err)
	}
	outputRoot, err := resolveTransactionOutputRoot(options.OutputRoot, discovered.RepositoryRoot)
	if err != nil {
		return TransactionResult{}, atStage("output root", 2, err)
	}
	session, err := reviewsession.PrepareNative(ctx, reviewsession.Options{
		RepositoryRoot: discovered.RepositoryRoot,
		OutputRoot:     outputRoot,
		Host:           "codex",
		Request:        discovered.Request,
		DirtyWorktree:  discovered.DirtyWorktree,
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
		Session: session, Goal: options.Goal, Model: options.Model,
		ReasoningEffort: options.ReasoningEffort, CodexBinary: options.CodexBinary, LeaseFile: leaseFile,
	})
	if err != nil {
		return TransactionResult{}, atStage("run native review", 1, err)
	}
	if err := publishNativeOutcome(session, outcome); err != nil {
		return TransactionResult{}, atStage("publish native review", 1, err)
	}

	status := "COMPLETE"
	exitCode := 0
	if outcome.SemanticResult() == quality.ResultIncomplete {
		status = "INCOMPLETE"
		exitCode = 1
	}
	artifacts := session.Artifacts()
	transaction = TransactionResult{
		Summary: NativeRunSummary{
			SchemaVersion: 3, Status: status, SemanticResult: outcome.SemanticResult(),
			SessionDir: session.Directory(), ResultPath: artifacts.ResultPath(), MarkdownPath: artifacts.MarkdownPath(),
			FreezePath: artifacts.FreezeManifestPath(), MetricsPath: artifacts.MetricsPath(), ModelCalls: outcome.ModelCalls(),
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
		value = ".code-quality"
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	if strings.TrimSpace(repositoryRoot) == "" {
		return "", errors.New("repository root is required for a relative path")
	}
	return filepath.Join(repositoryRoot, filepath.Clean(value)), nil
}
