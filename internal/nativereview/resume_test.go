package nativereview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestRestrictedResumeStartsFromColdNativeFrozenCheckpoint(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	sessionDir := fixture.prepareNativeFrozen(t)

	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: sessionDir, Provider: NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExitCode != 3 || resumed.Status.State != StatePublished || !resumed.Status.NativeReviewReused ||
		resumed.Status.NativeInvocationsThisRun != 0 || resumed.Status.RestrictedInvocationsThisRun != 1 {
		t.Fatalf("cold Native-frozen resume = %#v", resumed)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted" {
		t.Fatalf("cold resume Provider ledger = %#v", got)
	}
	result := readTransactionResult(t, filepath.Join(sessionDir, "output", "review-result.json"))
	if result.Execution.NativeAttempts != 1 || result.Execution.RestrictedAttempts != 1 ||
		result.Execution.ProviderAttemptsTotal != 2 || result.Execution.AdoptedRestrictedAttempt == nil ||
		*result.Execution.AdoptedRestrictedAttempt != 1 || !result.Execution.Resumed {
		t.Fatalf("cold resume attempt accounting = %#v", result.Execution)
	}
}

func TestSessionDigestExcludesCheckpointClockAndState(t *testing.T) {
	checkpoint := SessionCheckpoint{
		ToolVersion: "0.5.8", State: StateRestrictedRetryable,
		RestrictedAttempts: []RestrictedAttemptRecord{{
			SchemaVersion: 1, Attempt: 1, Status: "FAILED",
			StartedAt: "2026-08-19T00:00:00Z", FinishedAt: "2026-08-19T00:01:00Z",
			Failure:       &AttemptFailure{Class: FailureProviderQuota, Retryable: true, Reason: "Provider quota was unavailable"},
			Artifacts:     []ArtifactDigest{{Name: "stderr", Path: "output/restricted-attempts/0001/stderr", Present: true, Bytes: 1, SHA256: "sha256:" + strings.Repeat("a", 64)}},
			AttemptDigest: "attempt-v1:sha256:" + strings.Repeat("b", 64),
		}},
	}
	other := checkpoint
	other.State = StateTerminalError
	other.Sequence = 99
	other.RestrictedAttempts = append([]RestrictedAttemptRecord{}, checkpoint.RestrictedAttempts...)
	other.RestrictedAttempts[0].StartedAt = "2026-08-20T00:00:00Z"
	other.RestrictedAttempts[0].FinishedAt = "2026-08-20T00:05:00Z"
	other.RestrictedAttempts[0].AttemptDigest = "attempt-v1:sha256:" + strings.Repeat("c", 64)
	if left, right := computeSessionDigest(checkpoint), computeSessionDigest(other); left != right {
		t.Fatalf("session digest depends on clock or checkpoint state: %s != %s", left, right)
	}
}

func TestClaudeRestrictedResumeReusesFrozenNativeReview(t *testing.T) {
	fixture := newRestrictedResumeFixtureForHost(t, 0, "", "claude-code")
	sessionDir := fixture.prepareNativeFrozen(t)
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: sessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != StatePublished || resumed.Status.NativeInvocationsThisRun != 0 ||
		resumed.Status.RestrictedInvocationsThisRun != 1 || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("Claude resumed transaction = %#v, failure=%+v", resumed, resumed.Status.Failure)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted" {
		t.Fatalf("Claude resume Provider ledger = %#v", got)
	}
	result := readTransactionResult(t, filepath.Join(sessionDir, "output", "review-result.json"))
	if result.Execution.Host != "claude-code" || result.Contract.ProviderHost != "claude-code" ||
		result.Execution.ProviderAttemptsTotal != 2 || result.Execution.AdoptedRestrictedAttempt == nil ||
		*result.Execution.AdoptedRestrictedAttempt != 1 || !result.Execution.Resumed {
		t.Fatalf("Claude resume audit = %#v", result)
	}
}

func TestRestrictedResumeSealsInterruptedAttemptBeforeOneFinalCall(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	sessionDir := fixture.prepareNativeFrozen(t)
	checkpoint, err := loadAndVerifyCheckpoint(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.RestrictedAttempts = append(checkpoint.RestrictedAttempts, newAttemptRecord(1, false))
	checkpoint.State = StateRestrictedRunning
	if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	attemptDir := restrictedAttemptDirectory(sessionDir, 1)
	if err := os.MkdirAll(attemptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := capturePaths{
		finalMessage: filepath.Join(attemptDir, "restricted-adjudication.json"),
		jsonl:        filepath.Join(attemptDir, "restricted-adjudication.stdout.log"), stderr: filepath.Join(attemptDir, "restricted-adjudication.stderr.log"),
		freezeManifest: filepath.Join(attemptDir, "restricted-adjudication-freeze.json"), metrics: filepath.Join(attemptDir, "restricted-adjudication-metrics.json"),
	}
	if err := os.WriteFile(paths.jsonl, []byte(`{"type":"item.completed"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.stderr, []byte("process exited with parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: sessionDir, Provider: NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != StatePublished || resumed.Status.RestrictedInvocationsThisRun != 1 || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("interrupted resume = %#v", resumed)
	}
	raw, err := readCheckpointFile(filepath.Join(attemptDir, "attempt.json"), 1<<20, 0o400)
	if err != nil {
		t.Fatal(err)
	}
	record, err := quality.DecodeStrict[RestrictedAttemptRecord](strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "FAILED" || record.Failure == nil || record.Failure.Class != FailureProcessInterrupted || !record.Failure.Retryable {
		t.Fatalf("sealed interrupted attempt = %#v", record)
	}
	result := readTransactionResult(t, filepath.Join(sessionDir, "output", "review-result.json"))
	if result.Execution.ProviderAttemptsTotal != 3 || result.Execution.RestrictedAttempts != 2 ||
		result.Execution.AdoptedRestrictedAttempt == nil || *result.Execution.AdoptedRestrictedAttempt != 2 {
		t.Fatalf("interrupted attempt accounting = %#v", result.Execution)
	}
}

func TestRestrictedResumeAdoptsCompletedAttemptBeforeCheckpointAdvance(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	sessionDir := fixture.prepareNativeFrozen(t)
	checkpoint, err := loadAndVerifyCheckpoint(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	running := newAttemptRecord(1, false)
	checkpoint.RestrictedAttempts = append(checkpoint.RestrictedAttempts, running)
	checkpoint.State = StateRestrictedRunning
	if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	session, err := reviewsession.ReopenNative(context.Background(), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := quality.RestoreNativeOutcome(*checkpoint.NativeOutcome)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := runRestrictedAttempt(context.Background(), restrictedRunOptions{
		Session: session, Plan: checkpoint.Plan, Provider: fixture.provider(),
		Model: checkpoint.Plan.Contract.Model, ReasoningEffort: checkpoint.Plan.Contract.ReasoningEffort,
		Attempt: 1, Resumed: false, StartedAt: running.StartedAt,
	}, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Record.Status != "SUCCEEDED" {
		t.Fatalf("completed attempt = %#v", attempt.Record)
	}
	partial, err := attempt.Outcome.WithAttemptAudit(1, 1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveEncoded(session.Artifacts().ResultPath(), partial.EncodeJSON); err != nil {
		t.Fatal(err)
	}
	if err := session.Cleanup(); err != nil {
		t.Fatal(err)
	}

	before := len(readInvocationLedger(t, fixture.counterPath))
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: sessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != StatePublished || resumed.Status.NativeInvocationsThisRun != 0 ||
		resumed.Status.RestrictedInvocationsThisRun != 0 || resumed.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("adopted completed attempt = %#v", resumed)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("resume invoked Provider after completed attempt: before=%d after=%d", before, after)
	}
	result := readTransactionResult(t, filepath.Join(sessionDir, "output", "review-result.json"))
	if result.Execution.ProviderAttemptsTotal != 2 || result.Execution.RestrictedAttempts != 1 ||
		result.Execution.AdoptedRestrictedAttempt == nil || *result.Execution.AdoptedRestrictedAttempt != 1 || !result.Execution.Resumed {
		t.Fatalf("adopted attempt accounting = %#v", result.Execution)
	}
}

func TestRestrictedResumeRejectsContractDriftAndMissingTargetBeforeProvider(t *testing.T) {
	t.Run("contract", func(t *testing.T) {
		fixture := newRestrictedResumeFixture(t, 0, "")
		sessionDir := fixture.prepareNativeFrozen(t)
		checkpoint, err := loadAndVerifyCheckpoint(sessionDir)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint.Plan.Contract.Model = "drifted-model"
		if err := writeCheckpoint(sessionDir, &checkpoint); err != nil {
			t.Fatal(err)
		}
		before := len(readInvocationLedger(t, fixture.counterPath))
		result, err := ResumeRestricted(context.Background(), ResumeOptions{
			SessionDir: sessionDir, Provider: NewCodexProvider(fixture.providerPath),
			AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		})
		if err == nil || result.Status.ProviderInvocationsThisRun != 0 || len(readInvocationLedger(t, fixture.counterPath)) != before {
			t.Fatalf("contract drift resume = %#v, err = %v", result, err)
		}
	})

	t.Run("target", func(t *testing.T) {
		fixture := newRestrictedResumeFixture(t, 0, "")
		sessionDir := fixture.prepareNativeFrozen(t)
		moved := fixture.repository + ".unavailable"
		if err := os.Rename(fixture.repository, moved); err != nil {
			t.Fatal(err)
		}
		before := len(readInvocationLedger(t, fixture.counterPath))
		result, err := ResumeRestricted(context.Background(), ResumeOptions{
			SessionDir: sessionDir, Provider: NewCodexProvider(fixture.providerPath),
			AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		})
		if err == nil || result.Status.ProviderInvocationsThisRun != 0 || result.Status.State != StateTerminalError ||
			result.Status.Failure == nil || result.Status.Failure.Class != FailureTargetUnavailable ||
			len(readInvocationLedger(t, fixture.counterPath)) != before {
			t.Fatalf("missing target resume = %#v, err = %v", result, err)
		}
	})
}

func TestRestrictedResumeReusesFrozenNativeReview(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "quota exceeded")
	initial := fixture.run(t)
	if initial.ExitCode != 1 || initial.Status.State != StateRestrictedRetryable {
		t.Fatalf("initial transaction = %#v", initial)
	}
	if initial.Status.NativeInvocationsThisRun != 1 || initial.Status.RestrictedInvocationsThisRun != 1 || initial.Status.ProviderInvocationsThisRun != 2 {
		t.Fatalf("initial invocation accounting = %#v", initial.Status)
	}
	for _, path := range []string{
		filepath.Join(initial.Status.SessionDir, "checkpoint.json"),
		filepath.Join(initial.Status.SessionDir, "output", "native-review-freeze.json"),
		filepath.Join(initial.Status.SessionDir, "output", "restricted-attempts", "0001", "attempt.json"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retained checkpoint artifact %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(initial.Status.SessionDir, "output", "review-result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retryable session published a formal result: %v", err)
	}

	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExitCode != 3 || resumed.Status.State != StatePublished || !resumed.Status.NativeReviewReused {
		t.Fatalf("resumed transaction = %#v", resumed)
	}
	if resumed.Status.NativeInvocationsThisRun != 0 || resumed.Status.RestrictedInvocationsThisRun != 1 || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("resume invocation accounting = %#v", resumed.Status)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted,restricted" {
		t.Fatalf("provider invocation ledger = %#v", got)
	}

	result := readTransactionResult(t, filepath.Join(initial.Status.SessionDir, "output", "review-result.json"))
	if result.Execution.ProviderInvocations != 3 || result.Execution.ProviderAttemptsTotal != 3 || result.Execution.NativeAttempts != 1 || result.Execution.RestrictedAttempts != 2 {
		t.Fatalf("published attempt accounting = %#v", result.Execution)
	}
	if result.Execution.AdoptedRestrictedAttempt == nil || *result.Execution.AdoptedRestrictedAttempt != 2 || !result.Execution.Resumed || result.Execution.ResumedSessionDigest == nil {
		t.Fatalf("published resume audit = %#v", result.Execution)
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != fixture.finding.ID {
		t.Fatalf("frozen finding identity/order changed: %#v", result.Findings)
	}

	if err := os.Remove(fixture.providerPath); err != nil {
		t.Fatal(err)
	}
	idempotent, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.Status.State != StatePublished || idempotent.Status.ProviderInvocationsThisRun != 0 || idempotent.ExitCode != 3 {
		t.Fatalf("idempotent resume = %#v", idempotent)
	}
}

func TestResumedResultMatchesOneShotResultOutsideAttemptAudit(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "quota exceeded")
	oneShot := fixture.run(t)
	oneShotResult := readTransactionResult(t, filepath.Join(oneShot.Summary.EvidenceDir, "output", "review-result.json"))

	if err := os.WriteFile(fixture.counterPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_RESTRICTED_FAILURES", "1")
	initial := fixture.run(t)
	if initial.Status.State != StateRestrictedRetryable {
		t.Fatalf("initial retryable transaction = %#v", initial)
	}
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil || resumed.Status.State != StatePublished {
		t.Fatalf("resumed transaction = %#v, err = %v", resumed, err)
	}
	resumedResult := readTransactionResult(t, filepath.Join(initial.Status.SessionDir, "output", "review-result.json"))
	if !reflect.DeepEqual(withoutAttemptAudit(oneShotResult), withoutAttemptAudit(resumedResult)) {
		t.Fatalf("resumed result changed non-audit semantics:\none-shot=%#v\nresumed=%#v", oneShotResult, resumedResult)
	}
}

func TestIncrementalRestrictedResumeRestoresFrozenPreviousBlockers(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "quota exceeded")
	runTransactionGit(t, fixture.repository, "branch", "production", fixture.base)
	runTransactionGit(t, fixture.repository, "branch", "deploy", fixture.target)
	full, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: fixture.repository, BaseRef: "production", HeadRef: "deploy", ReviewScope: quality.ReviewScopeFull,
		OutputRoot: fixture.outputRoot, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil || full.Status.State != StatePublished || full.ExitCode != 3 {
		t.Fatalf("full parent review = %#v, err = %v", full, err)
	}
	previousResultPath := filepath.Join(full.Status.SessionDir, "output", "review-result.json")

	runTransactionGit(t, fixture.repository, "switch", "deploy")
	if err := os.WriteFile(filepath.Join(fixture.repository, "app.go"), []byte("package app\nfunc Run() bool { return false }\nfunc Delta() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTransactionGit(t, fixture.repository, "add", "app.go")
	runTransactionGit(t, fixture.repository, "commit", "-qm", "retain blocker in repair delta")
	incremental := fmt.Sprintf(`{"previous_finding_resolutions":[{"finding_id":%q,"status":"UNRESOLVED","reason":"The duplicate payment path remains reachable.","current_finding":{"priority":1,"title":"Reachable duplicate payment","code_location":{"path":"app.go","start_line":2,"end_line":2},"reason":"The changed path can submit the same payment twice.","suggestion":"Make the operation idempotent."}}],"new_findings":[]}`, fixture.finding.ID)
	if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.providerPath), "native.json"), []byte(incremental), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.counterPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_RESTRICTED_FAILURES", "1")
	initial, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: fixture.repository, BaseRef: "production", HeadRef: "deploy",
		ReviewScope: quality.ReviewScopeIncremental, PreviousResultPath: previousResultPath,
		OutputRoot: filepath.Join(t.TempDir(), "incremental-sessions"), Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil || initial.Status.State != StateRestrictedRetryable {
		t.Fatalf("incremental retryable review = %#v, err = %v", initial, err)
	}
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil || resumed.Status.State != StatePublished || resumed.Status.NativeInvocationsThisRun != 0 || resumed.Status.RestrictedInvocationsThisRun != 1 {
		t.Fatalf("incremental resumed review = %#v, err = %v", resumed, err)
	}
	result := readTransactionResult(t, filepath.Join(initial.Status.SessionDir, "output", "review-result.json"))
	if result.ReviewScope != quality.ReviewScopeIncremental || len(result.PreviousBlockingFindings) != 1 ||
		len(result.PreviousFindingResolutions) != 1 || result.PreviousFindingResolutions[0].Status != quality.ResolutionUnresolved ||
		result.Execution.ProviderAttemptsTotal != 3 || !result.Execution.Resumed {
		t.Fatalf("incremental resumed result = %#v", result)
	}
}

func withoutAttemptAudit(result quality.NativeReviewResult) quality.NativeReviewResult {
	result.Execution.ProviderInvocations = 0
	result.Execution.NativeAttempts = 0
	result.Execution.RestrictedAttempts = 0
	result.Execution.ProviderAttemptsTotal = 0
	result.Execution.AdoptedRestrictedAttempt = nil
	result.Execution.Resumed = false
	result.Execution.ResumedSessionDigest = nil
	return result
}

func TestRestrictedResumeRejectsTamperingBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		path func(string) string
	}{
		{name: "trusted diff", path: func(root string) string { return filepath.Join(root, "input", "trusted.diff") }},
		{name: "native freeze", path: func(root string) string { return filepath.Join(root, "output", "native-review-freeze.json") }},
		{name: "restricted policy", path: func(root string) string { return filepath.Join(root, "input", "restricted-adjudication-policy.md") }},
		{name: "restricted schema", path: func(root string) string {
			return filepath.Join(root, "input", "restricted-adjudication-output.schema.json")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRestrictedResumeFixture(t, 1, "request timed out")
			initial := fixture.run(t)
			before := len(readInvocationLedger(t, fixture.counterPath))
			path := test.path(initial.Status.SessionDir)
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tampered\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
				SessionDir: initial.Status.SessionDir,
				Provider:   NewCodexProvider(fixture.providerPath),
				AcquireLease: func() (io.Closer, *os.File, error) {
					return &recordingLease{}, nil, nil
				},
			})
			if err == nil || resumed.Status.ProviderInvocationsThisRun != 0 {
				t.Fatalf("tampered resume = %#v, err = %v", resumed, err)
			}
			after := len(readInvocationLedger(t, fixture.counterPath))
			if after != before {
				t.Fatalf("tampered resume invoked Provider: before=%d after=%d", before, after)
			}
		})
	}
}

func TestRestrictedResumeRejectsSymlinkedArtifactParentBeforeProvider(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "request timed out")
	initial := fixture.run(t)
	before := len(readInvocationLedger(t, fixture.counterPath))
	inputDir := filepath.Join(initial.Status.SessionDir, "input")
	movedInput := initial.Status.SessionDir + ".moved-input"
	if err := os.Rename(inputDir, movedInput); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(movedInput, inputDir); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err == nil || resumed.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("symlinked artifact parent resume = %#v, err = %v", resumed, err)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("symlinked artifact parent invoked Provider: before=%d after=%d", before, after)
	}
}

func TestRestrictedResumeRejectsRehashedTrustedDiffBeforeProvider(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "request timed out")
	initial := fixture.run(t)
	checkpoint, err := loadAndVerifyCheckpoint(initial.Status.SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(initial.Status.SessionDir, "input", "trusted.diff")
	if err := os.Chmod(diffPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diffPath, []byte("coordinated but false diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, artifact := range checkpoint.InputArtifacts {
		if artifact.Name == "trusted_diff" {
			checkpoint.InputArtifacts[index], err = digestArtifact(initial.Status.SessionDir, artifact.Name, diffPath, true)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writeCheckpoint(initial.Status.SessionDir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndVerifyCheckpoint(initial.Status.SessionDir); err != nil {
		t.Fatalf("coordinated checkpoint should reach Git-object verification: %v", err)
	}
	before := len(readInvocationLedger(t, fixture.counterPath))
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err == nil || resumed.Status.State != StateTerminalError || resumed.Status.ProviderInvocationsThisRun != 0 ||
		resumed.Status.Failure == nil || resumed.Status.Failure.Class != FailureArtifactIntegrity {
		t.Fatalf("rehashed trusted diff resume = %#v, err = %v", resumed, err)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("rehashed trusted diff invoked Provider: before=%d after=%d", before, after)
	}
}

func TestRestrictedResumeRejectsRehashedPolicyOutsideContractBeforeProvider(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "request timed out")
	initial := fixture.run(t)
	checkpoint, err := loadAndVerifyCheckpoint(initial.Status.SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(initial.Status.SessionDir, "input", "restricted-adjudication-policy.md")
	if err := os.Chmod(policyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("replacement policy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index, artifact := range checkpoint.InputArtifacts {
		if artifact.Name == "restricted_policy" {
			checkpoint.InputArtifacts[index], err = digestArtifact(initial.Status.SessionDir, artifact.Name, policyPath, true)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writeCheckpoint(initial.Status.SessionDir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	before := len(readInvocationLedger(t, fixture.counterPath))
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err == nil || resumed.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("rehashed policy resume = %#v, err = %v", resumed, err)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("rehashed policy invoked Provider: before=%d after=%d", before, after)
	}
}

func TestRestrictedResumeReclassifiesFrozenNativeEvidenceBeforeProvider(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "request timed out")
	initial := fixture.run(t)
	checkpoint, err := loadAndVerifyCheckpoint(initial.Status.SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	originalID := checkpoint.FrozenBlockingFindings[0].ID
	tampered := checkpoint.FrozenBlockingFindings[0]
	tampered.Title = "Checkpoint-only replacement finding"
	tampered, err = quality.IdentifyNativeFinding(tampered)
	if err != nil {
		t.Fatal(err)
	}
	replaceFinding := func(values []quality.NativeFinding) {
		for index := range values {
			if values[index].ID == originalID {
				values[index] = tampered
			}
		}
	}
	replaceFinding(checkpoint.NativeOutcome.Findings)
	replaceFinding(checkpoint.NativeOutcome.NewFindings)
	checkpoint.FrozenBlockingFindings[0] = tampered
	checkpoint.ContractArtifacts.RestrictedPromptSHA256 = quality.SHA256Digest([]byte(buildRestrictedAdjudicationPrompt(checkpoint.Plan, checkpoint.FrozenBlockingFindings)))
	if err := writeCheckpoint(initial.Status.SessionDir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndVerifyCheckpoint(initial.Status.SessionDir); err != nil {
		t.Fatalf("coordinated Native outcome should reach raw-evidence reclassification: %v", err)
	}
	before := len(readInvocationLedger(t, fixture.counterPath))
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err == nil || resumed.Status.State != StateTerminalError || resumed.Status.ProviderInvocationsThisRun != 0 ||
		resumed.Status.Failure == nil || resumed.Status.Failure.Class != FailureArtifactIntegrity {
		t.Fatalf("reclassified Native evidence resume = %#v, err = %v", resumed, err)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("reclassified Native evidence invoked Provider: before=%d after=%d", before, after)
	}
}

func TestSecondRestrictedFailureRequiresManualAndStopsFurtherCalls(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 2, "provider capacity unavailable")
	initial := fixture.run(t)
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExitCode != 5 || resumed.Status.State != StateManualRequired || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("second restricted failure = %#v", resumed)
	}
	before := len(readInvocationLedger(t, fixture.counterPath))
	again, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ExitCode != 5 || again.Status.State != StateManualRequired || again.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("manual-required retry = %#v", again)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("manual-required retry invoked Provider: before=%d after=%d", before, after)
	}
}

func TestConcurrentRestrictedResumeAllowsOneProviderInvocation(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "rate limit exceeded")
	initial := fixture.run(t)
	t.Setenv("FAKE_RESTRICTED_SLEEP", "1")
	type response struct {
		result TransactionResult
		err    error
	}
	firstDone := make(chan response, 1)
	go func() {
		result, err := ResumeRestricted(context.Background(), ResumeOptions{
			SessionDir: initial.Status.SessionDir,
			Provider:   NewCodexProvider(fixture.providerPath),
			AcquireLease: func() (io.Closer, *os.File, error) {
				return &recordingLease{}, nil, nil
			},
		})
		firstDone <- response{result: result, err: err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for len(readInvocationLedger(t, fixture.counterPath)) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	second, secondErr := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	first := <-firstDone
	if first.err != nil || first.result.Status.State != StatePublished {
		t.Fatalf("first resume = %#v, err = %v", first.result, first.err)
	}
	if !errors.Is(secondErr, ErrRestrictedResumeActive) || second.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("concurrent resume = %#v, err = %v", second, secondErr)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted,restricted" {
		t.Fatalf("concurrent Provider ledger = %#v", got)
	}
}

type restrictedResumeFixture struct {
	repository   string
	base         string
	target       string
	providerPath string
	counterPath  string
	outputRoot   string
	finding      quality.NativeFinding
	host         string
}

func newRestrictedResumeFixture(t *testing.T, failures int, failureText string) restrictedResumeFixture {
	return newRestrictedResumeFixtureForHost(t, failures, failureText, "codex")
}

func newRestrictedResumeFixtureForHost(t *testing.T, failures int, failureText, host string) restrictedResumeFixture {
	t.Helper()
	if host != "codex" && host != "claude-code" {
		t.Fatalf("unsupported fixture host %q", host)
	}
	repository, base, target := transactionRepository(t)
	directory := t.TempDir()
	nativeResponsePath := filepath.Join(directory, "native.json")
	restrictedResponsePath := filepath.Join(directory, "restricted.json")
	counterPath := filepath.Join(directory, "invocations.log")
	if err := os.WriteFile(counterPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	finding, err := quality.IdentifyNativeFinding(quality.NativeFinding{
		Priority: 1, Title: "Reachable duplicate payment", CodeLocation: quality.NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
		Reason: "The changed path can submit the same payment twice.", Suggestion: "Make the operation idempotent.",
	})
	if err != nil {
		t.Fatal(err)
	}
	native := `{"findings":[{"priority":1,"title":"Reachable duplicate payment","code_location":{"path":"app.go","start_line":2,"end_line":2},"reason":"The changed path can submit the same payment twice.","suggestion":"Make the operation idempotent."}]}`
	restricted := fmt.Sprintf(`{"adjudications":[{"finding_id":%q,"validity":"SUPPORTED","severity":"S3","trigger_confidence":"T3","evidence_level":"E2","introduced_or_worsened_by_change":true,"trigger_condition_is_concrete":true,"causal_chain_is_complete":true,"finding_is_not_style_preference":true,"recommended_disposition":"BLOCK","evidence_refs":[{"path":"app.go","start_line":2,"end_line":2,"support":"The committed target contains the reachable payment path."}],"uncertainties":[],"reason":"The exact target proves the duplicate payment."}]}`, finding.ID)
	if err := os.WriteFile(nativeResponsePath, []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restrictedResponsePath, []byte(restricted), 0o600); err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(directory, "codex")
	var script string
	if host == "codex" {
		script = `#!/bin/sh
set -eu
output=''
previous=''
for argument in "$@"; do
  if [ "$previous" = '--output-last-message' ]; then output="$argument"; fi
  previous="$argument"
done
test -n "$output"
cat >/dev/null
case "$output" in
  */restricted-attempts/*)
    printf '%s\n' restricted >> "$FAKE_INVOCATION_COUNTER"
    count=$(grep -c '^restricted$' "$FAKE_INVOCATION_COUNTER")
    if [ "$count" -le "$FAKE_RESTRICTED_FAILURES" ]; then
      printf '%s\n' "$FAKE_RESTRICTED_FAILURE" >&2
      printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":8,"output_tokens":1}}'
      exit 75
    fi
    if [ -n "${FAKE_RESTRICTED_SLEEP:-}" ]; then sleep "$FAKE_RESTRICTED_SLEEP"; fi
    cp "$FAKE_RESTRICTED_RESPONSE" "$output"
    ;;
  *)
    printf '%s\n' native >> "$FAKE_INVOCATION_COUNTER"
    cp "$FAKE_NATIVE_RESPONSE" "$output"
    ;;
esac
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":20,"output_tokens":4}}'
`
	} else {
		providerPath = filepath.Join(directory, "claude")
		nativeEvent, err := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "is_error": false, "result": native,
			"usage": map[string]int{"input_tokens": 20, "output_tokens": 4, "cache_read_input_tokens": 10},
		})
		if err != nil {
			t.Fatal(err)
		}
		restrictedEvent, err := json.Marshal(map[string]any{
			"type": "result", "subtype": "success", "is_error": false, "result": restricted,
			"usage": map[string]int{"input_tokens": 8, "output_tokens": 2, "cache_read_input_tokens": 4},
		})
		if err != nil {
			t.Fatal(err)
		}
		nativeTranscript := filepath.Join(directory, "native-transcript.jsonl")
		restrictedTranscript := filepath.Join(directory, "restricted-transcript.jsonl")
		if err := os.WriteFile(nativeTranscript, append(nativeEvent, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(restrictedTranscript, append(restrictedEvent, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_NATIVE_TRANSCRIPT", nativeTranscript)
		t.Setenv("FAKE_RESTRICTED_TRANSCRIPT", restrictedTranscript)
		script = `#!/bin/sh
set -eu
cat >/dev/null
restricted=0
plan=0
safe=0
strict=0
previous=''
for argument in "$@"; do
  if [ "$previous" = '--permission-mode' ] && [ "$argument" = 'plan' ]; then plan=1; fi
  case "$argument" in
    --system-prompt) restricted=1 ;;
    --safe-mode) safe=1 ;;
    --strict-mcp-config) strict=1 ;;
  esac
  previous="$argument"
done
if [ "$restricted" -eq 1 ]; then
    if [ "$plan" -ne 1 ] || [ "$safe" -ne 1 ] || [ "$strict" -ne 1 ]; then
      printf '%s\n' 'restricted invocation was not read-only' >&2
      exit 64
    fi
    printf '%s\n' restricted >> "$FAKE_INVOCATION_COUNTER"
    count=$(grep -c '^restricted$' "$FAKE_INVOCATION_COUNTER")
    if [ "$count" -le "$FAKE_RESTRICTED_FAILURES" ]; then
      printf '%s\n' "$FAKE_RESTRICTED_FAILURE" >&2
      cat "$FAKE_RESTRICTED_TRANSCRIPT"
      exit 75
    fi
    cat "$FAKE_RESTRICTED_TRANSCRIPT"
else
    printf '%s\n' native >> "$FAKE_INVOCATION_COUNTER"
    cat "$FAKE_NATIVE_TRANSCRIPT"
fi
`
	}
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NATIVE_RESPONSE", nativeResponsePath)
	t.Setenv("FAKE_RESTRICTED_RESPONSE", restrictedResponsePath)
	t.Setenv("FAKE_INVOCATION_COUNTER", counterPath)
	t.Setenv("FAKE_RESTRICTED_FAILURES", fmt.Sprintf("%d", failures))
	t.Setenv("FAKE_RESTRICTED_FAILURE", failureText)
	return restrictedResumeFixture{
		repository: repository, base: base, target: target, providerPath: providerPath,
		counterPath: counterPath, outputRoot: filepath.Join(t.TempDir(), "sessions"), finding: finding, host: host,
	}
}

func (fixture restrictedResumeFixture) provider() Provider {
	if fixture.host == "claude-code" {
		return NewClaudeProvider(fixture.providerPath)
	}
	return NewCodexProvider(fixture.providerPath)
}

func (fixture restrictedResumeFixture) run(t *testing.T) TransactionResult {
	t.Helper()
	result, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target, DiffReason: "resume-test",
		OutputRoot: fixture.outputRoot, Provider: fixture.provider(),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (fixture restrictedResumeFixture) prepareNativeFrozen(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	provider := fixture.provider()
	contract, err := ResolveContract(provider, "", "", "", quality.ReviewScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reviewplan.Build(ctx, reviewplan.Input{
		RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target,
		DiffReason: "cold-native-frozen-test", ReviewScope: quality.ReviewScopeFull,
		Contract: contract.Contract, ParentContract: contract.Contract,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := reviewsession.PrepareNative(ctx, reviewsession.Options{
		RepositoryRoot: plan.RepositoryRoot(), OutputRoot: fixture.outputRoot, Host: provider.Host(),
		Request: plan.ProviderRequest, DirtyWorktree: plan.DirtyWorktree, NativeSchemaName: contract.OutputSchemaName,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := newSessionCheckpoint(session, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.State = StateNativeRunning
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		t.Fatal(err)
	}
	outcome, err := runNativeSession(ctx, nativeRunOptions{
		Session: session, Plan: plan, Provider: provider, Model: contract.Contract.Model,
		ReasoningEffort: contract.Contract.ReasoningEffort, ExecutionProfile: contract.Contract.ExecutionProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.BlockingFindings()) == 0 {
		t.Fatal("fixture Native review did not produce a frozen blocker")
	}
	if err := checkpointNativeFrozen(&checkpoint, session, outcome); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := session.Cleanup(); err != nil {
		t.Fatal(err)
	}
	return session.Directory()
}

func readInvocationLedger(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}
