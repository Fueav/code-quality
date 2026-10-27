package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Fueav/code-quality/quality"
)

func TestNativeMissingFinalMessagePersistsTerminalStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		timeout time.Duration
		class   FailureClass
	}{
		{"deadline", "exec sleep 30", 2 * time.Second, FailureDeadlineExceeded},
		{"process failure", "exit 7", 0, FailureProviderProcess},
		{"successful process without final", "exit 0", 0, FailureProviderProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRestrictedResumeFixture(t, 0, "")
			// A transcript can contain an early empty finding message without a
			// final result. Neither it nor source text in stderr is authoritative.
			injectProviderStage(t, fixture, "native", `printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"findings\":[]}"}}'
printf '%s\n' 'partial source: rate_limit quota HTTP 429' >&2
`+test.body)
			result, err := RunTransaction(context.Background(), TransactionOptions{
				RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target,
				OutputRoot: fixture.outputRoot, Provider: fixture.provider(), NativeTimeout: test.timeout,
				AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
			})
			if err != nil {
				t.Fatalf("persisted failure must reach CLI status output: %v", err)
			}
			assertTerminalIntegrityResult(t, result, test.class)
			if result.Status.NativeAttempts != 1 || result.Status.ProviderAttemptsTotal != 1 ||
				result.Status.NativeInvocationsThisRun != 1 || result.Status.RestrictedAttempts != 0 {
				t.Fatalf("incorrect attempt audit: %#v", result.Status)
			}
			var statusJSON bytes.Buffer
			if err := quality.EncodeJSON(&statusJSON, result.Status); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(statusJSON.String(), `"state": "TERMINAL_ERROR"`) ||
				!strings.Contains(statusJSON.String(), `"session_dir":`) {
				t.Fatalf("terminal status is not structured: %s", statusJSON.String())
			}
			checkpoint, err := loadAndVerifyCheckpoint(result.Status.SessionDir)
			if err != nil {
				t.Fatal(err)
			}
			if checkpoint.State != StateTerminalError || checkpoint.LastFailure == nil || checkpoint.LastFailure.Class != test.class ||
				checkpoint.LastFailure.Retryable || checkpoint.Publication.Status != "UNPUBLISHED" ||
				checkpoint.NativeOutcome == nil || checkpoint.NativeOutcome.Adjudication.SemanticResult != quality.ResultError {
				t.Fatalf("failure checkpoint = %#v", checkpoint)
			}
			if len(checkpoint.NativeEvidence) != 5 || checkpoint.NativeEvidence[0].Present ||
				checkpoint.NativeEvidence[0].Bytes != 0 || checkpoint.NativeEvidence[0].SHA256 != "" {
				t.Fatalf("missing final was not faithfully retained: %#v", checkpoint.NativeEvidence)
			}
			for _, artifact := range checkpoint.NativeEvidence[1:] {
				if !artifact.Present || artifact.SHA256 == "" {
					t.Fatalf("partial evidence lost: %#v", artifact)
				}
			}
			invalid := checkpoint
			invalid.State = StateNativeFrozen
			if err := validateCheckpointState(invalid); err == nil {
				t.Fatal("missing Native final message was admitted to a resumable state")
			}
			finalPath := filepath.Join(result.Status.SessionDir, "output", "native-review.txt")
			if _, err := os.Lstat(finalPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing model output was fabricated: %v", err)
			}
			var metrics NativeRunMetrics
			metricsRaw, err := os.ReadFile(filepath.Join(result.Status.SessionDir, "output", "native-run-metrics.json"))
			if err != nil || json.Unmarshal(metricsRaw, &metrics) != nil || metrics.ProcessDurationMS == nil || metrics.FreezeDurationMS == nil {
				t.Fatalf("partial metrics unavailable: %s, %v", metricsRaw, err)
			}
			if metrics.UsageAvailable || metrics.InputTokens != nil || metrics.OutputTokens != nil {
				t.Fatalf("unfinished transcript invented token usage: %#v", metrics)
			}
			resume := ResumeOptions{SessionDir: result.Status.SessionDir, Provider: fixture.provider(),
				AcquireLease: func() (io.Closer, *os.File, error) {
					t.Error("terminal Native failure attempted another Provider lease")
					return nil, nil, errors.New("Provider is forbidden")
				}}
			resumed, err := ResumeRestricted(context.Background(), resume)
			if err != nil || resumed.Status.State != StateTerminalError || resumed.ExitCode != 1 || resumed.Status.ProviderInvocationsThisRun != 0 {
				t.Fatalf("terminal Native failure resumed: %#v, %v", resumed, err)
			}
			// Absence is immutable evidence too: a later fabricated final message
			// must invalidate the checkpoint, never convert this run to PASS.
			if err := os.WriteFile(finalPath, []byte(`{"findings":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ResumeRestricted(context.Background(), resume); err == nil {
				t.Fatal("a final message added after terminal failure was accepted")
			}
			if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native" {
				t.Fatalf("Provider ledger = %s", got)
			}
		})
	}
}

func TestNativeMalformedPresentFinalKeepsPublishedError(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 0, "")
	if err := os.WriteFile(os.Getenv("FAKE_NATIVE_RESPONSE"), []byte(`{"findings":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.run(t)
	if result.Status.State != StatePublished || result.ExitCode != 1 || result.Summary.Result != quality.ResultError || result.Summary.Release != "HOLD" {
		t.Fatalf("complete malformed final changed publication semantics: %#v", result)
	}
	if got := strings.Join(readInvocationLedger(t, fixture.counterPath), ","); got != "native" {
		t.Fatalf("Provider ledger = %s", got)
	}
}
