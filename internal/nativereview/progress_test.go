package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressJSONLReporterEmitsVersionedSequenceAndBoundMetadata(t *testing.T) {
	clock := &progressTestClock{now: time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)}
	var output bytes.Buffer
	reporter, err := newProgressReporter(&output, ProgressFormatJSONL, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	reporter.transition(ProgressPlanStarted, ProgressStagePlan, 0)
	reporter.bind("review-v1:sha256:"+strings.Repeat("a", 64), "FULL")
	clock.Advance(1500 * time.Millisecond)
	reporter.transition(ProgressPlanReady, ProgressStagePlan, 0)
	reporter.transition(ProgressNativeStarted, ProgressStageNative, 1)
	lastActivity := clock.Now().Add(2 * time.Second)
	clock.Advance(5 * time.Second)
	reporter.heartbeat(ProgressStageNative, 1, lastActivity)

	events := decodeProgressEvents(t, output.String())
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	for index, event := range events {
		if event.SchemaVersion != 1 || event.Sequence != int64(index+1) {
			t.Fatalf("event %d = %#v", index, event)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
			t.Fatalf("event %d occurred_at = %q: %v", index, event.OccurredAt, err)
		}
	}
	if events[0].ReviewKey != "" || events[0].ReviewScope != "" {
		t.Fatalf("pre-plan event leaked unbound identity: %#v", events[0])
	}
	if events[1].ReviewKey == "" || events[1].ReviewScope != "FULL" || events[1].ElapsedMS != 1500 {
		t.Fatalf("bound plan event = %#v", events[1])
	}
	if events[3].Event != ProgressNativeHeartbeat || events[3].Attempt != 1 ||
		events[3].ElapsedMS != 5000 || events[3].LastActivityAt != lastActivity.Format(time.RFC3339Nano) {
		t.Fatalf("heartbeat event = %#v", events[3])
	}
}

func TestProgressTextReporterPreservesV058Heartbeat(t *testing.T) {
	clock := &progressTestClock{now: time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)}
	var output bytes.Buffer
	reporter, err := newProgressReporter(&output, ProgressFormatText, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	reporter.transition(ProgressNativeStarted, ProgressStageNative, 1)
	clock.Advance(45 * time.Second)
	reporter.heartbeat(ProgressStageNative, 1, time.Time{})
	if !strings.Contains(output.String(), "quality-review: heartbeat stage=NATIVE_RUNNING attempt=1 elapsed=45s\n") {
		t.Fatalf("text progress = %q", output.String())
	}
}

func TestProgressWriterFailureIsAdvisory(t *testing.T) {
	reporter, err := newProgressReporter(failingProgressWriter{}, ProgressFormatJSONL, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reporter.transition(ProgressPlanStarted, ProgressStagePlan, 0)
	reporter.transition(ProgressPlanReady, ProgressStagePlan, 0)
	if reporter.sequence != 2 {
		t.Fatalf("sequence = %d", reporter.sequence)
	}
}

func TestParseProgressFormatRejectsUnknownValue(t *testing.T) {
	for _, value := range []string{"", "text", "jsonl"} {
		if _, err := ParseProgressFormat(value); err != nil {
			t.Fatalf("format %q: %v", value, err)
		}
	}
	if _, err := ParseProgressFormat("json"); err == nil {
		t.Fatal("unknown progress format was accepted")
	}
}

func TestRunTransactionEmitsLifecycleWithoutChangingPublishedResult(t *testing.T) {
	repository, base, target := transactionRepository(t)
	var progress bytes.Buffer
	transaction, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: repository, Base: base, Target: target, DiffReason: "test",
		OutputRoot: t.TempDir(), CodexBinary: transactionCodex(t, false),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: &progress, ProgressFormat: ProgressFormatJSONL, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.ExitCode != 0 || transaction.Status.State != StatePublished {
		t.Fatalf("transaction = %#v", transaction)
	}
	events := decodeProgressEvents(t, progress.String())
	want := []string{
		ProgressPlanStarted, ProgressPlanReady, ProgressNativeStarted, ProgressNativeFreezing,
		ProgressNativeFrozen, ProgressFinalizing, ProgressPublished,
	}
	if got := progressEventNames(events); !equalStrings(got, want) {
		t.Fatalf("events = %v, want %v\nraw=%s", got, want, progress.String())
	}
	for _, event := range events {
		if event.Event == ProgressRestrictedStarted || event.Event == ProgressRestrictedHeartbeat {
			t.Fatalf("no-blocker run emitted Restricted event: %#v", event)
		}
	}
}

func TestRestrictedRetryAndResumeEmitOneTerminalLifecyclePerInvocation(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "rate limit exceeded")
	var initialProgress bytes.Buffer
	initial, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target, DiffReason: "progress-test",
		OutputRoot: fixture.outputRoot, Provider: fixture.provider(),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: &initialProgress, ProgressFormat: ProgressFormatJSONL, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status.State != StateRestrictedRetryable {
		t.Fatalf("initial = %#v", initial)
	}
	initialNames := progressEventNames(decodeProgressEvents(t, initialProgress.String()))
	for _, want := range []string{
		ProgressPlanStarted, ProgressPlanReady, ProgressNativeStarted, ProgressNativeFreezing,
		ProgressNativeFrozen, ProgressRestrictedStarted, ProgressRestrictedFreezing, ProgressRestrictedRetryable,
	} {
		if !containsString(initialNames, want) {
			t.Fatalf("initial events missing %s: %v", want, initialNames)
		}
	}
	assertOneProgressTerminal(t, initialNames, ProgressRestrictedRetryable)

	var resumeProgress bytes.Buffer
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: &resumeProgress, ProgressFormat: ProgressFormatJSONL, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != StatePublished || resumed.Status.NativeInvocationsThisRun != 0 || resumed.Status.RestrictedInvocationsThisRun != 1 {
		t.Fatalf("resumed = %#v", resumed)
	}
	resumeEvents := decodeProgressEvents(t, resumeProgress.String())
	resumeNames := progressEventNames(resumeEvents)
	wantResume := []string{
		ProgressRecoveryStarted, ProgressRecoveryReady, ProgressRestrictedStarted, ProgressRestrictedFreezing,
		ProgressRestrictedCompleted, ProgressFinalizing, ProgressPublished,
	}
	if !equalStrings(resumeNames, wantResume) {
		t.Fatalf("resume events = %v, want %v\nraw=%s", resumeNames, wantResume, resumeProgress.String())
	}
	for _, event := range resumeEvents {
		if event.Event == ProgressNativeStarted || event.Event == ProgressNativeHeartbeat {
			t.Fatalf("resume reran Native in progress stream: %#v", event)
		}
	}
	assertOneProgressTerminal(t, resumeNames, ProgressPublished)
}

func TestSecondRestrictedFailureEmitsManualRequired(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 2, "provider capacity unavailable")
	initial := fixture.run(t)
	var progress bytes.Buffer
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir, Provider: fixture.provider(),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: &progress, ProgressFormat: ProgressFormatJSONL, HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status.State != StateManualRequired || resumed.ExitCode != 5 {
		t.Fatalf("resumed = %#v", resumed)
	}
	names := progressEventNames(decodeProgressEvents(t, progress.String()))
	assertOneProgressTerminal(t, names, ProgressManualRequired)
	if containsString(names, ProgressPublished) || containsString(names, ProgressRestrictedCompleted) {
		t.Fatalf("manual-required run emitted success events: %v", names)
	}
}

func TestProgressWriterFailureDoesNotChangeTransactionResult(t *testing.T) {
	repository, base, target := transactionRepository(t)
	transaction, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: repository, Base: base, Target: target, DiffReason: "test",
		OutputRoot: t.TempDir(), CodexBinary: transactionCodex(t, false),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: failingProgressWriter{}, ProgressFormat: ProgressFormatJSONL,
	})
	if err != nil || transaction.ExitCode != 0 || transaction.Status.State != StatePublished {
		t.Fatalf("transaction = %#v, error = %v", transaction, err)
	}
}

func TestTransactionErrorEmitsExactlyOneFailedEvent(t *testing.T) {
	var progress bytes.Buffer
	_, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: filepath.Join(t.TempDir(), "missing"),
		AcquireLease:   func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
		ProgressWriter: &progress, ProgressFormat: ProgressFormatJSONL,
	})
	if err == nil {
		t.Fatal("missing repository transaction unexpectedly succeeded")
	}
	names := progressEventNames(decodeProgressEvents(t, progress.String()))
	assertOneProgressTerminal(t, names, ProgressFailed)
}

func TestProcessOutputWriterTracksActivityWithoutInspectingContent(t *testing.T) {
	started := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	activity := newProcessActivity(started)
	observed := started.Add(9 * time.Second)
	var output bytes.Buffer
	writer := processOutputWriter{Writer: &output, activity: activity, now: func() time.Time { return observed }}
	if _, err := writer.Write([]byte("opaque provider bytes")); err != nil {
		t.Fatal(err)
	}
	if output.String() != "opaque provider bytes" || !activity.latest().Equal(observed) {
		t.Fatalf("output=%q activity=%s", output.String(), activity.latest())
	}
}

func TestNativeProcessEmitsJSONHeartbeatWithLastActivity(t *testing.T) {
	root := t.TempDir()
	var progress bytes.Buffer
	reporter, err := newProgressReporter(&progress, ProgressFormatJSONL, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reporter.transition(ProgressNativeStarted, ProgressStageNative, 1)
	err = runNativeProcess(context.Background(), reviewInvocation{
		executable: exec.Command("sh").Path,
		args:       []string{"-c", "printf activity; sleep 0.08"},
		directory:  root,
		paths: capturePaths{
			jsonl: filepath.Join(root, "stdout"), stderr: filepath.Join(root, "stderr"),
		},
		stage: string(StateNativeRunning), attempt: 1,
		heartbeatInterval: 10 * time.Millisecond, progress: reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range decodeProgressEvents(t, progress.String()) {
		if event.Event == ProgressNativeHeartbeat {
			if event.LastActivityAt == "" || event.Attempt != 1 || event.Stage != ProgressStageNative {
				t.Fatalf("heartbeat = %#v", event)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no heartbeat was emitted: %s", progress.String())
	}
}

func decodeProgressEvents(t *testing.T, raw string) []ProgressEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	events := make([]ProgressEvent, 0, len(lines))
	for _, line := range lines {
		var event ProgressEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode progress line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func progressEventNames(events []ProgressEvent) []string {
	names := make([]string, len(events))
	for index, event := range events {
		names[index] = event.Event
	}
	return names
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func assertOneProgressTerminal(t *testing.T, names []string, wanted string) {
	t.Helper()
	terminals := map[string]bool{
		ProgressFullRequired: true, ProgressRestrictedRetryable: true, ProgressPublished: true,
		ProgressManualRequired: true, ProgressFailed: true,
	}
	count := 0
	for _, name := range names {
		if terminals[name] {
			count++
		}
	}
	if count != 1 || !containsString(names, wanted) {
		t.Fatalf("terminal events = %d, wanted %s in %v", count, wanted, names)
	}
}

type progressTestClock struct {
	now time.Time
}

func (clock *progressTestClock) Now() time.Time {
	return clock.now
}

func (clock *progressTestClock) Advance(duration time.Duration) {
	clock.now = clock.now.Add(duration)
}

type failingProgressWriter struct{}

func (failingProgressWriter) Write([]byte) (int, error) {
	return 0, errors.New("progress sink closed")
}
