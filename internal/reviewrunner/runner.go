// Package reviewrunner coordinates durable review requests without changing the
// immutable native result or weakening the native transaction's checks.
package reviewrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

const (
	Executed   = "EXECUTED"
	Reused     = "REUSED"
	Current    = "CURRENT"
	Superseded = "SUPERSEDED"
)

// Executor is the existing native transaction seam, also used by offline tests.
// Verify must never invoke a Provider, including when the checkpoint changed.
type Executor interface {
	Plan(context.Context, nativereview.TransactionOptions) (reviewplan.Decision, error)
	Run(context.Context, nativereview.TransactionOptions) (nativereview.TransactionResult, error)
	Resume(context.Context, nativereview.ResumeOptions) (nativereview.TransactionResult, error)
	Verify(context.Context, nativereview.ResumeOptions) (nativereview.TransactionResult, error)
}

type Options struct {
	Transaction         nativereview.TransactionOptions
	StateRoot           string
	RunnerPolicyVersion string
	// ExecutionFingerprint is a trusted caller attestation of the effective
	// Provider binary/model/configuration. Unknown personal context is inadmissible.
	ExecutionFingerprint string
	// Current must observe the authority whose result the caller will publish.
	// A local-ref observer cannot attest a remote PR. CI separately rechecks its
	// remote PR immediately before accepting the required check.
	Current  func(context.Context) (baseSHA, headSHA string, err error)
	Executor Executor
}

type Result struct {
	Transaction     nativereview.TransactionResult
	ResultSource    string `json:"result_source"`
	LifecycleStatus string `json:"lifecycle_status"`
	ExitCode        int    `json:"exit_code"`
}

// Run automatically chooses one action. It never falls back from a failed or
// unknown prior attempt to a new Native invocation.
func Run(ctx context.Context, options Options) (Result, error) {
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	executor := options.Executor
	if executor == nil {
		executor = nativeExecutor{}
	}
	transaction := options.Transaction
	transaction.ReviewScope = quality.ReviewScopeFull
	transaction.PreviousResultPath = ""
	full, err := executor.Plan(ctx, transaction)
	if err != nil {
		return Result{}, err
	}
	if full.Status != reviewplan.StatusReady {
		return terminal(full), nil
	}
	if full.DetectionSource == "explicit_commits" {
		return Result{}, errors.New("durable coordination requires stable change refs; a legacy commit pair cannot identify a review lineage")
	}
	repositoryRoot := full.RepositoryRoot()
	if repositoryRoot == "" {
		repositoryRoot = transaction.RepositoryPath
	} // injected executors in offline tests
	root, err := openStore(options.StateRoot, repositoryRoot)
	if err != nil {
		return Result{}, err
	}
	family := familyKey(full)
	store, err := openFamily(ctx, root, family, transaction.ProgressWriter)
	if err != nil {
		return Result{}, err
	}
	defer store.Close()
	// Re-resolve refs after waiting for the family lock; an older queued request
	// must not silently execute against newly moved local refs.
	checked, err := executor.Plan(ctx, transaction)
	if err != nil {
		return Result{}, err
	}
	if checked.ReviewKey != full.ReviewKey {
		return Result{}, errors.New("review refs changed while waiting; submit a fresh request")
	}
	key := requestKey(full.ReviewKey, options.RunnerPolicyVersion, options.ExecutionFingerprint)
	record, found, err := store.readRecord(key)
	if err != nil {
		return Result{}, err
	}
	if found {
		if err := record.matches(full, options); err != nil {
			return Result{}, err
		}
		return continueRequest(ctx, options, executor, store, record)
	}
	latest, hasLatest, err := store.latest()
	if err != nil {
		return Result{}, err
	}
	plan := full
	parentResult := ""
	if hasLatest {
		if latest.RunnerPolicyVersion != options.RunnerPolicyVersion || latest.ExecutionFingerprint != options.ExecutionFingerprint {
			return terminal(required(full, "execution_environment_changed")), nil
		}
		if latest.State != string(nativereview.StatePublished) {
			return Result{}, errors.New("previous review is incomplete; resume that exact request or obtain a new human-approved review")
		}
		prior, err := executor.Verify(ctx, resumeOptions(options, latest.SessionDir))
		if err != nil {
			return Result{}, fmt.Errorf("verify previous review: %w", err)
		}
		if err := verifyResult(prior, latest); err != nil {
			return Result{}, err
		}
		if prior.Summary.Result == quality.ResultError {
			return Result{}, errors.New("previous review failed; cannot start another automatic Native review")
		}
		parentResult = filepath.Join(latest.SessionDir, "output", "review-result.json")
		transaction.ReviewScope = quality.ReviewScopeIncremental
		transaction.PreviousResultPath = parentResult
		plan, err = executor.Plan(ctx, transaction)
		if err != nil {
			return Result{}, err
		}
		if plan.Status != reviewplan.StatusReady {
			return terminal(plan), nil
		}
	}
	// These directories and the durable STARTED record precede the native call.
	// An interrupted attempt is therefore never mistaken for a cache miss.
	runRoot, err := store.newRunRoot(key)
	if err != nil {
		return Result{}, err
	}
	transaction.OutputRoot = runRoot
	record = requestRecord{
		SchemaVersion: 1, Key: key, FullReviewKey: full.ReviewKey, ReviewKey: plan.ReviewKey,
		BaseTip: full.BaseTipCommit, Head: full.CurrentHead,
		RunnerPolicyVersion: options.RunnerPolicyVersion, ExecutionFingerprint: options.ExecutionFingerprint,
		RunRoot: runRoot, State: "STARTED", ParentResult: parentResult,
	}
	if err := store.save(record, true); err != nil {
		return Result{}, err
	}
	result, runErr := executor.Run(ctx, transaction)
	if result.Status.SessionDir != "" {
		if err := insidePrivateDirectory(runRoot, result.Status.SessionDir); err != nil {
			return Result{}, err
		}
		record.SessionDir = result.Status.SessionDir
		record.State = string(result.Status.State)
	}
	if err := store.save(record, false); err != nil {
		return Result{}, err
	}
	if runErr != nil {
		return Result{}, runErr
	}
	if result.Plan.Status != reviewplan.StatusReady {
		return terminal(result.Plan), nil
	}
	if result.Plan.ReviewKey != plan.ReviewKey {
		return Result{}, errors.New("native transaction changed the planned identity")
	}
	return finish(ctx, options, result, Executed)
}

func continueRequest(ctx context.Context, options Options, executor Executor, store *familyStore, record requestRecord) (Result, error) {
	if record.SessionDir == "" {
		session, err := discoverSession(record.RunRoot, record.ReviewKey)
		if err != nil {
			return Result{}, err
		}
		record.SessionDir = session
	}
	if err := insidePrivateDirectory(record.RunRoot, record.SessionDir); err != nil {
		return Result{}, err
	}
	var transaction nativereview.TransactionResult
	var err error
	source := Executed
	if record.State == string(nativereview.StatePublished) {
		transaction, err = executor.Verify(ctx, resumeOptions(options, record.SessionDir))
		source = Reused
	} else {
		// Resume verifies the full checkpoint and admits only its bounded states.
		// It never invokes Native, including for a STARTED/crashed request.
		transaction, err = executor.Resume(ctx, resumeOptions(options, record.SessionDir))
	}
	if err != nil {
		return Result{}, err
	}
	if transaction.Plan.ReviewKey != record.ReviewKey {
		return Result{}, errors.New("retained session does not match the indexed review")
	}
	if source == Reused {
		if err := verifyResult(transaction, record); err != nil {
			return Result{}, err
		}
	}
	record.State = string(transaction.Status.State)
	if err := store.save(record, false); err != nil {
		return Result{}, err
	}
	return finish(ctx, options, transaction, source)
}

func validateOptions(options Options) error {
	if options.Transaction.ExecutionProfile != quality.ExecutionProfileProductionCI {
		return errors.New("durable coordination requires production-ci; unbound personal context cannot be reused")
	}
	for _, value := range []string{options.RunnerPolicyVersion, options.ExecutionFingerprint} {
		if strings.TrimSpace(value) == "" || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("runner policy and execution fingerprint must be nonempty, bounded single-line attestations")
		}
	}
	if options.Current == nil {
		return errors.New("coordinated publication requires a current base/head observer")
	}
	if options.Transaction.PreviousResultPath != "" || (options.Transaction.ReviewScope != "" && !strings.EqualFold(options.Transaction.ReviewScope, quality.ReviewScopeFull)) {
		return errors.New("durable coordination selects scope and parent; do not supply an incremental scope or previous result")
	}
	return nil
}

func verifyResult(result nativereview.TransactionResult, record requestRecord) error {
	if result.Status.State != nativereview.StatePublished || result.Plan.ReviewKey != record.ReviewKey ||
		result.Plan.BaseTipCommit != record.BaseTip || result.Plan.CurrentHead != record.Head ||
		result.Status.ProviderInvocationsThisRun != 0 {
		return errors.New("cached publication is not a verified zero-invocation result for this request")
	}
	return nil
}

func resumeOptions(options Options, session string) nativereview.ResumeOptions {
	t := options.Transaction
	return nativereview.ResumeOptions{SessionDir: session, Provider: t.Provider, AcquireLease: t.AcquireLease,
		RestrictedTimeout: t.RestrictedTimeout, HeartbeatInterval: t.HeartbeatInterval,
		ProgressWriter: t.ProgressWriter, ProgressFormat: t.ProgressFormat}
}

func finish(ctx context.Context, options Options, transaction nativereview.TransactionResult, source string) (Result, error) {
	if transaction.ExitCode == 0 && (transaction.Status.State != nativereview.StatePublished || transaction.Summary.Result != quality.ResultPass) {
		return Result{}, errors.New("successful exit requires a published PASS; incomplete or failed evidence cannot be green")
	}
	result := Result{Transaction: transaction, ResultSource: source, LifecycleStatus: Current, ExitCode: transaction.ExitCode}
	base, head, err := options.Current(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("observe current review target: %w", err)
	}
	if base != transaction.Plan.BaseTipCommit || head != transaction.Plan.CurrentHead {
		result.LifecycleStatus = Superseded
		result.ExitCode = 1
	}
	return result, nil
}

func required(plan reviewplan.Decision, reason string) reviewplan.Decision {
	plan.Status = reviewplan.StatusFullRequired
	plan.FullRequiredReasons = []string{reason}
	plan.ProviderInvocations = 0
	return plan
}

func terminal(plan reviewplan.Decision) Result {
	code := 4
	if plan.Status == reviewplan.StatusManualRequired {
		code = 5
	}
	transaction := nativereview.TransactionResult{Plan: plan, ExitCode: code}
	return Result{Transaction: transaction, ExitCode: code}
}

// ValidateBudget rejects configurations that the outer job would interrupt
// before evidence finalization. It does not silently shorten a review stage.
func ValidateBudget(total, native, restricted, finalize time.Duration) error {
	if total <= 0 || native <= 0 || restricted <= 0 || finalize <= 0 {
		return errors.New("all review budget durations must be positive")
	}
	if native > total || restricted > total-native || finalize > total-native-restricted {
		return errors.New("job budget must cover Native, Restricted and finalization budgets")
	}
	return nil
}

type nativeExecutor struct{}

func (nativeExecutor) Plan(ctx context.Context, options nativereview.TransactionOptions) (reviewplan.Decision, error) {
	provider := options.Provider
	if provider == nil {
		provider = nativereview.NewCodexProvider(options.CodexBinary)
	}
	contract, err := nativereview.ResolveContract(provider, options.Model, options.ReasoningEffort, options.ExecutionProfile, options.ReviewScope)
	if err != nil {
		return reviewplan.Decision{}, err
	}
	parent := contract
	if strings.EqualFold(options.ReviewScope, quality.ReviewScopeIncremental) {
		parent, err = nativereview.ResolveContract(provider, options.Model, options.ReasoningEffort, options.ExecutionProfile, quality.ReviewScopeFull)
		if err != nil {
			return reviewplan.Decision{}, err
		}
	}
	return reviewplan.Build(ctx, reviewplan.Input{RepositoryPath: options.RepositoryPath, Base: options.Base, Target: options.Target,
		BaseRef: options.BaseRef, HeadRef: options.HeadRef, DiffReason: options.DiffReason,
		ReviewScope: options.ReviewScope, PreviousResultPath: options.PreviousResultPath, ReviewGoal: options.Goal,
		Environment: options.Environment, Contract: contract.Contract, ParentContract: parent.Contract})
}

func (nativeExecutor) Run(ctx context.Context, options nativereview.TransactionOptions) (nativereview.TransactionResult, error) {
	return nativereview.RunTransaction(ctx, options)
}

func (nativeExecutor) Resume(ctx context.Context, options nativereview.ResumeOptions) (nativereview.TransactionResult, error) {
	return nativereview.ResumeRestricted(ctx, options)
}

func (nativeExecutor) Verify(ctx context.Context, options nativereview.ResumeOptions) (nativereview.TransactionResult, error) {
	options.AcquireLease = func() (io.Closer, *os.File, error) {
		return nil, nil, errors.New("a cached publication cannot invoke a Provider")
	}
	return nativereview.ResumeRestricted(ctx, options)
}
