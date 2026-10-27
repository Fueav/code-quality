package nativereview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestInvalidRestrictedReferenceHoldsWithoutPublishingOrRetryingNative(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	path := os.Getenv("FAKE_RESTRICTED_RESPONSE")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"path":"app.go"`, `"path":"missing.go"`, 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.run(t)
	assertTerminalIntegrityResult(t, result, FailureProviderProtocol)
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{SessionDir: result.Status.SessionDir})
	if err != nil || resumed.Status.State != StateTerminalError || resumed.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("terminal failure resumed: %#v, %v", resumed, err)
	}
	if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native,restricted" {
		t.Fatalf("Provider ledger = %s", got)
	}
}

func TestNativeIntegrityFailureNeverPublishesPassOrInvokesRestricted(t *testing.T) {
	for _, findings := range []bool{false, true} {
		for name, mutation := range map[string]string{
			"tracked source":        "printf '%s\\n' changed > app.go",
			"hidden tracked source": "git update-index --assume-unchanged app.go\nprintf '%s\\n' changed > app.go",
			"target head":           "git checkout --detach \"$FAKE_ORIGINAL_BASE\" >/dev/null 2>&1",
			"frozen input":          "chmod 600 ../trusted.diff\nprintf '%s\\n' changed > ../trusted.diff",
		} {
			t.Run(name+map[bool]string{false: "/clean-claim", true: "/candidate"}[findings], func(t *testing.T) {
				fixture := newRestrictedResumeFixture(t, 0, "")
				t.Setenv("FAKE_ORIGINAL_BASE", fixture.base)
				if !findings {
					if err := os.WriteFile(os.Getenv("FAKE_NATIVE_RESPONSE"), []byte(`{"findings":[]}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				injectProviderStage(t, fixture, "native", mutation)
				result, err := RunTransaction(context.Background(), TransactionOptions{
					RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target,
					OutputRoot: fixture.outputRoot, Provider: fixture.provider(),
					AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
				})
				if err == nil || !strings.Contains(err.Error(), "verify Native target integrity") {
					t.Fatalf("Native integrity error = %v", err)
				}
				assertTerminalIntegrityResult(t, result, FailureArtifactIntegrity)
				if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native" {
					t.Fatalf("Provider ledger = %s", got)
				}
				if got := strings.TrimSpace(runTransactionGit(t, fixture.repository, "rev-parse", "HEAD")); got != fixture.target {
					t.Fatalf("source HEAD changed to %s", got)
				}
				if got := runTransactionGit(t, fixture.repository, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
					t.Fatalf("source checkout changed: %s", got)
				}
			})
		}
	}
}

func TestRestrictedStartsWithFreshTargetAndRecordsSeparatedTimings(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	injectProviderStage(t, fixture, "native", "printf '%s\\n' 'untrusted generated context' > native-generated.txt")
	injectProviderStage(t, fixture, "restricted", "test ! -e native-generated.txt\ntest \"$(git rev-parse HEAD)\" = \"$FAKE_ORIGINAL_TARGET\"")
	t.Setenv("FAKE_ORIGINAL_TARGET", fixture.target)
	result := fixture.run(t)
	if result.ExitCode != 3 || result.Status.State != StatePublished {
		t.Fatalf("fresh target review = %#v", result)
	}
	for _, path := range []string{
		filepath.Join(result.Status.SessionDir, "output", "native-run-metrics.json"),
		filepath.Join(result.Status.SessionDir, "output", "restricted-attempts", "0001", "restricted-adjudication-metrics.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var metrics NativeRunMetrics
		if err := json.Unmarshal(raw, &metrics); err != nil {
			t.Fatal(err)
		}
		if metrics.ProcessDurationMS == nil || metrics.FreezeDurationMS == nil || *metrics.ProcessDurationMS < 0 || *metrics.FreezeDurationMS < 0 {
			t.Fatalf("missing measured durations: %s", raw)
		}
		// Millisecond truncation may lose one millisecond across the split.
		if sum := *metrics.ProcessDurationMS + *metrics.FreezeDurationMS; sum > metrics.DurationMS+1 || metrics.DurationMS > sum+1 {
			t.Fatalf("inconsistent timing totals: %s", raw)
		}
	}
}

func TestRestrictedSourceMutationHoldsOnInitialAndResumedAttempts(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "resumed"}[resumed], func(t *testing.T) {
			fixture := newRestrictedResumeFixture(t, 0, "")
			var sessionDir string
			if resumed {
				sessionDir = fixture.prepareNativeFrozen(t)
			}
			injectProviderStage(t, fixture, "restricted", "printf '%s\\n' changed > app.go")
			var result TransactionResult
			if resumed {
				var err error
				result, err = ResumeRestricted(context.Background(), ResumeOptions{
					SessionDir: sessionDir, Provider: fixture.provider(),
					AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				result = fixture.run(t)
			}
			assertTerminalIntegrityResult(t, result, FailureArtifactIntegrity)
			if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native,restricted" {
				t.Fatalf("Provider ledger = %s", got)
			}
		})
	}
}

func TestRestrictedVerificationCancellationCannotSucceed(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		failure FailureClass
	}{
		{"deadline", context.DeadlineExceeded, FailureDeadlineExceeded},
		{"interruption", context.Canceled, FailureProcessInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRestrictedResumeFixture(t, 0, "")
			sessionDir := fixture.prepareNativeFrozen(t)
			checkpoint, err := loadAndVerifyCheckpoint(sessionDir)
			if err != nil {
				t.Fatal(err)
			}
			session, err := reviewsession.ReopenNative(context.Background(), sessionDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Cleanup() })
			outcome, err := quality.RestoreNativeOutcome(*checkpoint.NativeOutcome)
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			// The metric is written only after the successful process and evidence
			// freeze. Cancel on the next context check inside target verification.
			ctx := &artifactCancellationContext{Context: base, cancel: cancel, failure: test.err,
				marker: restrictedAttemptPaths(session, 1).metrics}
			attempt, err := runRestrictedAttempt(ctx, restrictedRunOptions{
				Session: session, Plan: checkpoint.Plan, Provider: fixture.provider(),
				Model: checkpoint.Plan.Contract.Model, ReasoningEffort: checkpoint.Plan.Contract.ReasoningEffort,
				Attempt: 1, Resumed: true, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
				InputArtifacts: checkpoint.InputArtifacts,
			}, outcome)
			if err != nil {
				t.Fatal(err)
			}
			if attempt.Record.Status != "FAILED" || attempt.Record.Failure == nil ||
				attempt.Record.Failure.Class != test.failure || !attempt.Record.Failure.Retryable {
				t.Fatalf("incomplete target verification succeeded: %#v", attempt.Record)
			}
			if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native,restricted" {
				t.Fatalf("Provider ledger = %s", got)
			}
		})
	}
}

type artifactCancellationContext struct {
	context.Context
	cancel  context.CancelFunc
	marker  string
	failure error
}

func (ctx *artifactCancellationContext) Err() error {
	if _, err := os.Lstat(ctx.marker); err == nil {
		ctx.cancel()
	}
	if ctx.Context.Err() != nil {
		return ctx.failure
	}
	return nil
}

func injectProviderStage(t *testing.T, fixture restrictedResumeFixture, stage, body string) {
	t.Helper()
	raw, err := os.ReadFile(fixture.providerPath)
	if err != nil {
		t.Fatal(err)
	}
	marker := "printf '%s\\n' " + stage + " >> \"$FAKE_INVOCATION_COUNTER\""
	if strings.Count(string(raw), marker) != 1 {
		t.Fatal("fake Provider stage marker is not unique")
	}
	script := strings.Replace(string(raw), marker, marker+"\n"+body, 1)
	if err := os.WriteFile(fixture.providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertTerminalIntegrityResult(t *testing.T, result TransactionResult, class FailureClass) {
	t.Helper()
	if result.ExitCode != 1 || result.Status.State != StateTerminalError || result.Status.Failure == nil || result.Status.Failure.Class != class || result.Status.Failure.Retryable {
		t.Fatalf("expected terminal hold, got %#v", result)
	}
	if result.Summary.Result == quality.ResultPass {
		t.Fatal("integrity failure reported PASS")
	}
	if _, err := os.Lstat(filepath.Join(result.Status.SessionDir, "output", "review-result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("integrity failure published a formal result: %v", err)
	}
}
