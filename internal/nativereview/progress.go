package nativereview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type ProgressFormat string

const (
	ProgressFormatText  ProgressFormat = "text"
	ProgressFormatJSONL ProgressFormat = "jsonl"
)

const (
	ProgressPlanStarted         = "PLAN_STARTED"
	ProgressPlanReady           = "PLAN_READY"
	ProgressFullRequired        = "FULL_REQUIRED"
	ProgressRecoveryStarted     = "RECOVERY_STARTED"
	ProgressRecoveryReady       = "RECOVERY_READY"
	ProgressNativeStarted       = "NATIVE_STARTED"
	ProgressNativeHeartbeat     = "NATIVE_HEARTBEAT"
	ProgressNativeFreezing      = "NATIVE_FREEZING"
	ProgressNativeFrozen        = "NATIVE_FROZEN"
	ProgressRestrictedStarted   = "RESTRICTED_STARTED"
	ProgressRestrictedHeartbeat = "RESTRICTED_HEARTBEAT"
	ProgressRestrictedFreezing  = "RESTRICTED_FREEZING"
	ProgressRestrictedCompleted = "RESTRICTED_COMPLETED"
	ProgressRestrictedRetryable = "RESTRICTED_RETRYABLE"
	ProgressFinalizing          = "FINALIZING"
	ProgressPublished           = "PUBLISHED"
	ProgressManualRequired      = "MANUAL_REQUIRED"
	ProgressFailed              = "FAILED"
)

const (
	ProgressStagePlan             = "PLAN"
	ProgressStageRecovery         = "RECOVERY"
	ProgressStageNative           = "NATIVE"
	ProgressStageNativeFreeze     = "NATIVE_FREEZE"
	ProgressStageRestricted       = "RESTRICTED"
	ProgressStageRestrictedFreeze = "RESTRICTED_FREEZE"
	ProgressStageFinalize         = "FINALIZE"
)

type ProgressEvent struct {
	SchemaVersion  int    `json:"schema_version"`
	Sequence       int64  `json:"sequence"`
	Event          string `json:"event"`
	Stage          string `json:"stage"`
	OccurredAt     string `json:"occurred_at"`
	ElapsedMS      int64  `json:"elapsed_ms"`
	ReviewKey      string `json:"review_key,omitempty"`
	ReviewScope    string `json:"review_scope,omitempty"`
	Attempt        int    `json:"attempt,omitempty"`
	LastActivityAt string `json:"last_activity_at,omitempty"`
}

type progressSink interface {
	write(ProgressEvent)
}

type progressReporter struct {
	mu           sync.Mutex
	sink         progressSink
	now          func() time.Time
	sequence     int64
	reviewKey    string
	reviewScope  string
	currentStage string
	currentTry   int
	stageStarted map[string]time.Time
}

func ParseProgressFormat(value string) (ProgressFormat, error) {
	format := ProgressFormat(strings.TrimSpace(value))
	if format == "" {
		format = ProgressFormatText
	}
	switch format {
	case ProgressFormatText, ProgressFormatJSONL:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported progress format %q; use text or jsonl", value)
	}
}

func newProgressReporter(writer io.Writer, format ProgressFormat, now func() time.Time) (*progressReporter, error) {
	parsed, err := ParseProgressFormat(string(format))
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	reporter := &progressReporter{now: now, stageStarted: map[string]time.Time{}}
	if writer == nil {
		return reporter, nil
	}
	switch parsed {
	case ProgressFormatText:
		reporter.sink = textProgressSink{writer: writer}
	case ProgressFormatJSONL:
		reporter.sink = jsonlProgressSink{writer: writer}
	}
	return reporter, nil
}

func (reporter *progressReporter) enabled() bool {
	return reporter != nil && reporter.sink != nil
}

func (reporter *progressReporter) bind(reviewKey, reviewScope string) {
	if reporter == nil {
		return
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reviewKey = reviewKey
	reporter.reviewScope = reviewScope
}

func (reporter *progressReporter) transition(event, stage string, attempt int) {
	if reporter == nil {
		return
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	now := reporter.now().UTC()
	key := progressStageKey(stage, attempt)
	if progressStartsStage(event) {
		reporter.stageStarted[key] = now
	}
	started, ok := reporter.stageStarted[key]
	if !ok {
		started = now
		reporter.stageStarted[key] = now
	}
	reporter.currentStage = stage
	reporter.currentTry = attempt
	reporter.emitLocked(ProgressEvent{
		Event: event, Stage: stage, OccurredAt: now.Format(time.RFC3339Nano),
		ElapsedMS: nonNegativeDuration(now.Sub(started)).Milliseconds(), Attempt: attempt,
	})
}

func (reporter *progressReporter) heartbeat(stage string, attempt int, lastActivity time.Time) {
	if reporter == nil {
		return
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	now := reporter.now().UTC()
	key := progressStageKey(stage, attempt)
	started, ok := reporter.stageStarted[key]
	if !ok {
		started = now
		reporter.stageStarted[key] = now
	}
	event := ProgressNativeHeartbeat
	if stage == ProgressStageRestricted {
		event = ProgressRestrictedHeartbeat
	}
	value := ProgressEvent{
		Event: event, Stage: stage, OccurredAt: now.Format(time.RFC3339Nano),
		ElapsedMS: nonNegativeDuration(now.Sub(started)).Milliseconds(), Attempt: attempt,
	}
	if lastActivity.IsZero() {
		lastActivity = started
	}
	value.LastActivityAt = lastActivity.UTC().Format(time.RFC3339Nano)
	reporter.currentStage = stage
	reporter.currentTry = attempt
	reporter.emitLocked(value)
}

func (reporter *progressReporter) failed() {
	if reporter == nil {
		return
	}
	reporter.mu.Lock()
	stage, attempt := reporter.currentStage, reporter.currentTry
	reporter.mu.Unlock()
	if stage == "" {
		stage = ProgressStagePlan
	}
	reporter.transition(ProgressFailed, stage, attempt)
}

func (reporter *progressReporter) emitLocked(event ProgressEvent) {
	reporter.sequence++
	event.SchemaVersion = 1
	event.Sequence = reporter.sequence
	event.ReviewKey = reporter.reviewKey
	event.ReviewScope = reporter.reviewScope
	if reporter.sink != nil {
		reporter.sink.write(event)
	}
}

func progressStartsStage(event string) bool {
	switch event {
	case ProgressPlanStarted, ProgressRecoveryStarted, ProgressNativeStarted, ProgressNativeFreezing,
		ProgressRestrictedStarted, ProgressRestrictedFreezing, ProgressFinalizing:
		return true
	default:
		return false
	}
}

func progressStageKey(stage string, attempt int) string {
	return fmt.Sprintf("%s:%d", stage, attempt)
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

type textProgressSink struct {
	writer io.Writer
}

func (sink textProgressSink) write(event ProgressEvent) {
	if event.Event == ProgressNativeHeartbeat || event.Event == ProgressRestrictedHeartbeat {
		stage := string(StateNativeRunning)
		if event.Stage == ProgressStageRestricted {
			stage = string(StateRestrictedRunning)
		}
		_, _ = fmt.Fprintf(sink.writer, "quality-review: heartbeat stage=%s attempt=%d elapsed=%s\n",
			stage, event.Attempt, (time.Duration(event.ElapsedMS) * time.Millisecond).Round(time.Second))
		return
	}
	if event.Attempt > 0 {
		_, _ = fmt.Fprintf(sink.writer, "quality-review: progress event=%s stage=%s attempt=%d elapsed=%s\n",
			event.Event, event.Stage, event.Attempt, (time.Duration(event.ElapsedMS) * time.Millisecond).Round(time.Second))
		return
	}
	_, _ = fmt.Fprintf(sink.writer, "quality-review: progress event=%s stage=%s elapsed=%s\n",
		event.Event, event.Stage, (time.Duration(event.ElapsedMS) * time.Millisecond).Round(time.Second))
}

type jsonlProgressSink struct {
	writer io.Writer
}

func (sink jsonlProgressSink) write(event ProgressEvent) {
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	_, _ = sink.writer.Write(raw)
}

type processActivity struct {
	mu   sync.Mutex
	last time.Time
}

func newProcessActivity(started time.Time) *processActivity {
	return &processActivity{last: started.UTC()}
}

func (activity *processActivity) mark(value time.Time) {
	if activity == nil {
		return
	}
	activity.mu.Lock()
	activity.last = value.UTC()
	activity.mu.Unlock()
}

func (activity *processActivity) latest() time.Time {
	if activity == nil {
		return time.Time{}
	}
	activity.mu.Lock()
	defer activity.mu.Unlock()
	return activity.last
}

type processOutputWriter struct {
	io.Writer
	activity *processActivity
	now      func() time.Time
}

func (writer processOutputWriter) Write(value []byte) (int, error) {
	if writer.Writer == nil {
		return 0, errors.New("process output writer is missing")
	}
	written, err := writer.Writer.Write(value)
	if written > 0 && writer.activity != nil {
		now := writer.now
		if now == nil {
			now = time.Now
		}
		writer.activity.mark(now())
	}
	return written, err
}
