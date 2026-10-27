package reviewrunner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

type fakeExecutor struct {
	mu                            sync.Mutex
	head, base, contract, verdict string
	baseRef, headRef, changeID    string
	runs, resumes, verifies       int
	results                       map[string]nativereview.TransactionResult
	entered                       chan struct{}
	release                       chan struct{}
	retryable, broken             bool
	runErr                        error
}

func newFake() *fakeExecutor {
	return &fakeExecutor{head: "head-1", base: "base", baseRef: "main", headRef: "topic", contract: "contract-1", verdict: quality.ResultPass, results: map[string]nativereview.TransactionResult{}}
}

func (fake *fakeExecutor) Plan(_ context.Context, options nativereview.TransactionOptions) (reviewplan.Decision, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	plan := reviewplan.Decision{SchemaVersion: 1, Status: reviewplan.StatusReady,
		ReviewIdentity: quality.ReviewIdentity{ReviewKey: "full:" + fake.head + ":" + fake.base + ":" + fake.contract + ":" + fake.baseRef + ":" + fake.headRef, ContractDigest: fake.contract,
			ReviewScope: quality.ReviewScopeFull, BaseRef: fake.baseRef, HeadRef: fake.headRef, BaseTipCommit: fake.base, CurrentHead: fake.head},
		Request: quality.ReviewRequest{Repository: "owner/repo"}, FullRequiredReasons: []string{}, ManualRequiredReasons: []string{}}
	if fake.changeID != "" {
		plan.Request.Change = &quality.ChangeContext{Kind: "pull_request", ID: fake.changeID}
	}
	if options.ReviewScope == quality.ReviewScopeIncremental {
		previous, exists := fake.results[filepath.Dir(filepath.Dir(options.PreviousResultPath))]
		if !exists {
			return required(plan, "previous_result_invalid"), nil
		}
		if previous.Plan.ReviewScope == quality.ReviewScopeIncremental {
			plan.Status = reviewplan.StatusManualRequired
			plan.ManualRequiredReasons = []string{"automatic_review_round_limit_reached"}
			return plan, nil
		}
		if previous.Plan.BaseTipCommit != fake.base || previous.Plan.ContractDigest != fake.contract || previous.Plan.BaseRef != fake.baseRef || previous.Plan.HeadRef != fake.headRef {
			return required(plan, "review_contract_or_base_changed"), nil
		}
		plan.ReviewScope = quality.ReviewScopeIncremental
		plan.ReviewKey = "incremental:" + plan.ReviewKey
	}
	return plan, nil
}

func (fake *fakeExecutor) Run(ctx context.Context, options nativereview.TransactionOptions) (nativereview.TransactionResult, error) {
	plan, err := fake.Plan(ctx, options)
	if err != nil {
		return nativereview.TransactionResult{}, err
	}
	fake.mu.Lock()
	fake.runs++
	entered, release := fake.entered, fake.release
	fake.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nativereview.TransactionResult{}, ctx.Err()
		}
	}
	session := filepath.Join(options.OutputRoot, "review-session")
	if err := os.Mkdir(session, 0o700); err != nil {
		return nativereview.TransactionResult{}, err
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	state := nativereview.StatePublished
	if fake.retryable {
		state = nativereview.StateRestrictedRetryable
	}
	if fake.runErr != nil {
		state = nativereview.StateTerminalError
	}
	result := nativereview.TransactionResult{Plan: plan, Status: nativereview.SessionStatus{State: state, SessionDir: session, ReviewKey: plan.ReviewKey, NativeInvocationsThisRun: 1, ProviderInvocationsThisRun: 1},
		Summary: nativereview.NativeRunSummary{NativeReleaseSummary: quality.NativeReleaseSummary{Result: fake.verdict}}}
	if fake.verdict == quality.ResultBlock {
		result.ExitCode = 3
	}
	if fake.verdict == quality.ResultError || fake.retryable || fake.runErr != nil {
		result.ExitCode = 1
	}
	fake.results[session] = result
	raw, _ := json.Marshal(nativereview.SessionCheckpoint{Plan: plan, State: state})
	if err := os.WriteFile(filepath.Join(session, "checkpoint.json"), raw, 0o600); err != nil {
		return result, err
	}
	return result, fake.runErr
}

func (fake *fakeExecutor) Resume(_ context.Context, options nativereview.ResumeOptions) (nativereview.TransactionResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.resumes++
	result, ok := fake.results[options.SessionDir]
	if !ok || fake.broken {
		return result, errors.New("invalid retained evidence")
	}
	result.Status.NativeInvocationsThisRun = 0
	result.Status.RestrictedInvocationsThisRun = 1
	result.Status.ProviderInvocationsThisRun = 1
	if fake.retryable {
		result.Status.State = nativereview.StateManualRequired
		result.ExitCode = 5
	} else {
		result.Status.State = nativereview.StatePublished
		result.ExitCode = 0
	}
	fake.results[options.SessionDir] = result
	return result, nil
}

func (fake *fakeExecutor) Verify(_ context.Context, options nativereview.ResumeOptions) (nativereview.TransactionResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.verifies++
	result, ok := fake.results[options.SessionDir]
	if !ok || fake.broken {
		return result, errors.New("invalid retained evidence")
	}
	result.Status.ProviderInvocationsThisRun = 0
	result.Status.NativeInvocationsThisRun = 0
	result.Status.RestrictedInvocationsThisRun = 0
	return result, nil
}

func fakeOptions(t *testing.T, fake *fakeExecutor) Options {
	t.Helper()
	return Options{Transaction: nativereview.TransactionOptions{RepositoryPath: t.TempDir(), ExecutionProfile: quality.ExecutionProfileProductionCI},
		StateRoot: filepath.Join(t.TempDir(), "state"), RunnerPolicyVersion: "policy-1", ExecutionFingerprint: "host-model-config-1", Executor: fake,
		Current: func(context.Context) (string, string, error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			return fake.base, fake.head, nil
		}}
}

func TestRepeatedPublishedRequestReusesPassAndBlockWithoutProvider(t *testing.T) {
	for _, verdict := range []string{quality.ResultPass, quality.ResultBlock, quality.ResultError} {
		t.Run(verdict, func(t *testing.T) {
			fake := newFake()
			fake.verdict = verdict
			options := fakeOptions(t, fake)
			first, err := Run(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Run(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if fake.runs != 1 || fake.resumes != 0 || second.ResultSource != Reused || second.ExitCode != first.ExitCode || second.Transaction.Summary.Result != verdict {
				t.Fatalf("unexpected reuse: %#v runs=%d resumes=%d", second, fake.runs, fake.resumes)
			}
		})
	}
}

func TestChangingRefsCannotResetKnownChangeLineage(t *testing.T) {
	for _, kind := range []string{"explicit_base", "pr_base", "pr_head"} {
		t.Run(kind, func(t *testing.T) {
			fake := newFake()
			if kind != "explicit_base" {
				fake.changeID = "42"
			}
			options := fakeOptions(t, fake)
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			if kind == "pr_head" {
				fake.headRef = "renamed-topic"
			} else {
				fake.baseRef = "release"
			}
			result, err := Run(context.Background(), options)
			if err != nil || result.ExitCode != 4 || fake.runs != 1 {
				t.Fatalf("changed refs reset the lineage: %#v, runs=%d, err=%v", result, fake.runs, err)
			}
		})
	}
}

func TestIncompleteOrFailedEvidenceCannotExitSuccessfully(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	for _, value := range []nativereview.TransactionResult{
		{},
		{Status: nativereview.SessionStatus{State: nativereview.StateRestrictedRetryable}},
		{Status: nativereview.SessionStatus{State: nativereview.StatePublished}, Summary: nativereview.NativeRunSummary{NativeReleaseSummary: quality.NativeReleaseSummary{Result: quality.ResultError}}},
	} {
		if _, err := finish(context.Background(), options, value, Executed); err == nil {
			t.Fatal("invalid zero exit was accepted")
		}
	}
}

func TestUnpublishedFailureRetainsStatusAfterDeadlineWithoutPublicationObservation(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	options.Current = func(context.Context) (string, string, error) {
		t.Fatal("unpublished failure attempted publication observation")
		return "", "", nil
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	transaction := nativereview.TransactionResult{
		Status: nativereview.SessionStatus{State: nativereview.StateTerminalError, SessionDir: "/private/session"}, ExitCode: 1,
	}
	result, err := finish(ctx, options, transaction, Executed)
	if err != nil || result.ExitCode != 1 || result.Transaction.Status.SessionDir != transaction.Status.SessionDir || result.LifecycleStatus != "" {
		t.Fatalf("failed session status was lost or became current: %#v, %v", result, err)
	}
}

func TestRunErrorRetainsActualSessionStatus(t *testing.T) {
	fake := newFake()
	fake.runErr = context.DeadlineExceeded
	options := fakeOptions(t, fake)
	result, err := Run(context.Background(), options)
	if !errors.Is(err, context.DeadlineExceeded) || result.ExitCode != 1 || result.Transaction.Status.State != nativereview.StateTerminalError || result.Transaction.Status.SessionDir == "" || result.LifecycleStatus != "" || fake.runs != 1 {
		t.Fatalf("Native failure lost its actual session status: %#v, %v", result, err)
	}
}

func TestResumeErrorRetainsActualIndexedSessionStatus(t *testing.T) {
	fake := newFake()
	fake.retryable = true
	options := fakeOptions(t, fake)
	first, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	fake.broken = true
	result, err := Run(context.Background(), options)
	if err == nil || result.ExitCode != 1 || result.Transaction.Status.SessionDir != first.Transaction.Status.SessionDir || result.LifecycleStatus != "" || fake.runs != 1 || fake.resumes != 1 {
		t.Fatalf("recovery failure lost its actual session status or repeated Native: %#v, %v", result, err)
	}
}

func TestExpiredPublicationUsesOnlyBoundedObservationAndKeepsFailureStatus(t *testing.T) {
	for _, mode := range []string{"expired_before", "deadline_before", "expired_during", "observation_error"} {
		t.Run(mode, func(t *testing.T) {
			fake := newFake()
			options := fakeOptions(t, fake)
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline_before" {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			if mode == "expired_before" {
				cancel()
			}
			observations := 0
			options.Current = func(observation context.Context) (string, string, error) {
				observations++
				if mode == "expired_before" || mode == "deadline_before" {
					deadline, bounded := observation.Deadline()
					if observation.Err() != nil || !bounded || time.Until(deadline) > 5*time.Second || time.Until(deadline) <= 0 {
						t.Fatal("cleanup observation did not receive a live bounded context")
					}
				}
				if mode == "expired_during" {
					cancel()
				}
				if mode == "observation_error" {
					return "", "", errors.New("target unavailable")
				}
				return fake.base, fake.head, nil
			}
			plan, err := fake.Plan(context.Background(), options.Transaction)
			if err != nil {
				t.Fatal(err)
			}
			transaction := nativereview.TransactionResult{
				Plan: plan, Status: nativereview.SessionStatus{State: nativereview.StatePublished, SessionDir: "/private/session"},
				Summary: nativereview.NativeRunSummary{NativeReleaseSummary: quality.NativeReleaseSummary{Result: quality.ResultPass}},
			}
			result, err := finish(ctx, options, transaction, Executed)
			if err == nil || result.ExitCode != 1 || result.Transaction.Status.SessionDir != transaction.Status.SessionDir || result.Transaction.Status.State != nativereview.StatePublished || observations != 1 || fake.runs != 0 || fake.resumes != 0 {
				t.Fatalf("publication observation lost status or became successful: %#v, %v", result, err)
			}
			if mode == "observation_error" && result.LifecycleStatus != "" {
				t.Fatal("unobserved target claimed a lifecycle state")
			}
			expectedFailure := context.Canceled
			if mode == "deadline_before" {
				expectedFailure = context.DeadlineExceeded
			}
			if mode != "observation_error" && !errors.Is(err, expectedFailure) {
				t.Fatalf("parent cancellation was lost: %v", err)
			}
		})
	}
}

func TestConcurrentDuplicateWaitsAndReuses(t *testing.T) {
	fake := newFake()
	fake.entered = make(chan struct{}, 1)
	fake.release = make(chan struct{})
	options := fakeOptions(t, fake)
	type answer struct {
		result Result
		err    error
	}
	answers := make(chan answer, 2)
	go func() { r, e := Run(context.Background(), options); answers <- answer{r, e} }()
	<-fake.entered
	go func() { r, e := Run(context.Background(), options); answers <- answer{r, e} }()
	close(fake.release)
	a, b := <-answers, <-answers
	if a.err != nil || b.err != nil {
		t.Fatalf("errors: %v %v", a.err, b.err)
	}
	if fake.runs != 1 || a.result.ResultSource == b.result.ResultSource {
		t.Fatalf("duplicate Provider invocation: runs=%d sources=%s,%s", fake.runs, a.result.ResultSource, b.result.ResultSource)
	}
}

func TestIncrementalStopsAfterSecondRound(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	fake.head = "head-2"
	second, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Transaction.Plan.ReviewScope != quality.ReviewScopeIncremental {
		t.Fatalf("scope=%s", second.Transaction.Plan.ReviewScope)
	}
	fake.head = "head-3"
	third, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if third.ExitCode != 5 || third.Transaction.Plan.Status != reviewplan.StatusManualRequired || fake.runs != 2 {
		t.Fatalf("third round did not stop: %#v runs=%d", third, fake.runs)
	}
}

func TestEnvironmentAndContractChangesNeverBecomeNewFull(t *testing.T) {
	for _, field := range []string{"policy", "fingerprint", "base", "contract"} {
		t.Run(field, func(t *testing.T) {
			fake := newFake()
			options := fakeOptions(t, fake)
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "policy":
				options.RunnerPolicyVersion = "policy-2"
			case "fingerprint":
				options.ExecutionFingerprint = "changed"
			case "base":
				fake.base = "new-base"
			case "contract":
				fake.contract = "new-contract"
			}
			result, err := Run(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 4 || fake.runs != 1 {
				t.Fatalf("changed %s started a new review: %#v runs=%d", field, result, fake.runs)
			}
		})
	}
}

func TestRestrictedRecoveryDoesNotRepeatNative(t *testing.T) {
	fake := newFake()
	fake.retryable = true
	options := fakeOptions(t, fake)
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if fake.runs != 1 || fake.resumes != 1 || second.ExitCode != 5 {
		t.Fatalf("unexpected recovery: %#v", second)
	}
	// The real native executor is idempotent for MANUAL_REQUIRED. The fake
	// must not be used to promise another attempt for this terminal state.
}

func TestMissingAndCorruptStateFailWithoutAnotherRun(t *testing.T) {
	for _, damage := range []string{"record", "session", "digest", "publication"} {
		t.Run(damage, func(t *testing.T) {
			fake := newFake()
			options := fakeOptions(t, fake)
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(options.StateRoot, "*", "requests", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal(err, files)
			}
			switch damage {
			case "record":
				if err := os.Remove(files[0]); err != nil {
					t.Fatal(err)
				}
			case "digest":
				if err := os.WriteFile(files[0], []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "session":
				for session := range fake.results {
					if err := os.RemoveAll(session); err != nil {
						t.Fatal(err)
					}
				}
			case "publication":
				fake.broken = true
			}
			if _, err := Run(context.Background(), options); err == nil {
				t.Fatal("damaged state was accepted")
			}
			if fake.runs != 1 {
				t.Fatalf("damage started Native again: %d", fake.runs)
			}
		})
	}
}

func TestSupersededRequestCannotReturnSuccess(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	options.Current = func(context.Context) (string, string, error) { return "base", "new-head", nil }
	result, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.LifecycleStatus != Superseded || result.ExitCode == 0 {
		t.Fatalf("old result remained green: %#v", result)
	}
}

func TestPersonalUnknownContextAndUnsafeStoreRejected(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	options.Transaction.ExecutionProfile = quality.ExecutionProfilePersonal
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("unbound personal environment accepted")
	}
	options.Transaction.ExecutionProfile = quality.ExecutionProfileProductionCI
	options.StateRoot = filepath.Join(options.Transaction.RepositoryPath, "state")
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("repository-local state accepted")
	}
	if fake.runs != 0 {
		t.Fatal("invalid configuration invoked Provider")
	}
}

func TestBudgetCoversFinalizationAndRejectsCurrentThirtyMinuteMismatch(t *testing.T) {
	if err := ValidateBudget(30*time.Minute, 45*time.Minute, 15*time.Minute, time.Minute); err == nil {
		t.Fatal("impossible job budget accepted")
	}
	if err := ValidateBudget(30*time.Minute, 18*time.Minute, 9*time.Minute, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestLostLineageCannotRestartFullForAnotherHead(t *testing.T) {
	fake := newFake()
	options := fakeOptions(t, fake)
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	families, err := filepath.Glob(filepath.Join(options.StateRoot, "*"))
	if err != nil || len(families) != 1 {
		t.Fatal(err, families)
	}
	if err := os.Remove(filepath.Join(families[0], "latest")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(families[0], "requests")); err != nil {
		t.Fatal(err)
	}
	fake.head = "head-2"
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("lost lineage restarted FULL on a new head")
	}
	if fake.runs != 1 {
		t.Fatalf("Native invocations=%d", fake.runs)
	}
}

func TestRepositorySubdirectoryCannotHideRepositoryLocalState(t *testing.T) {
	repository := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, raw)
		}
		return string(raw)
	}
	git("init", "-q", "-b", "main")
	if err := os.Mkdir(filepath.Join(repository, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repository, "subdir", "app.go")
	if err := os.WriteFile(file, []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "topic")
	if err := os.WriteFile(file, []byte("package app\nfunc Run() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "change")
	state := filepath.Join(repository, "state")
	_, err := Run(context.Background(), Options{Transaction: nativereview.TransactionOptions{RepositoryPath: filepath.Join(repository, "subdir"), BaseRef: "main", HeadRef: "topic", ExecutionProfile: quality.ExecutionProfileProductionCI}, StateRoot: state, RunnerPolicyVersion: "p", ExecutionFingerprint: "f", Current: func(context.Context) (string, string, error) { return "", "", nil }})
	if err == nil {
		t.Fatal("repository-local state accepted from subdirectory")
	}
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatalf("invalid state configuration wrote into repository: %v", err)
	}
}
