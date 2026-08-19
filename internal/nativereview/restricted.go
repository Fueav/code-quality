package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const maxRestrictedEvidenceFileBytes = int64(10 << 20)

type restrictedEvidenceRef struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Support   string `json:"support"`
}

type restrictedAdjudication struct {
	FindingID                    string                  `json:"finding_id"`
	Validity                     string                  `json:"validity"`
	Severity                     string                  `json:"severity"`
	TriggerConfidence            string                  `json:"trigger_confidence"`
	EvidenceLevel                string                  `json:"evidence_level"`
	IntroducedOrWorsenedByChange bool                    `json:"introduced_or_worsened_by_change"`
	TriggerConditionIsConcrete   bool                    `json:"trigger_condition_is_concrete"`
	CausalChainIsComplete        bool                    `json:"causal_chain_is_complete"`
	FindingIsNotStylePreference  bool                    `json:"finding_is_not_style_preference"`
	RecommendedDisposition       string                  `json:"recommended_disposition"`
	EvidenceRefs                 []restrictedEvidenceRef `json:"evidence_refs"`
	Uncertainties                []string                `json:"uncertainties"`
	Reason                       string                  `json:"reason"`
}

type restrictedAdjudicationEnvelope struct {
	Adjudications []restrictedAdjudication `json:"adjudications"`
}

type restrictedRunOptions struct {
	Session           reviewsession.NativeSession
	Plan              reviewplan.Decision
	Provider          Provider
	Model             string
	ReasoningEffort   string
	LeaseFile         *os.File
	SessionLockFile   *os.File
	Attempt           int
	Resumed           bool
	StartedAt         string
	HeartbeatInterval time.Duration
	ProgressWriter    io.Writer
}

type restrictedAttemptResult struct {
	Outcome quality.NativeOutcome
	Record  RestrictedAttemptRecord
}

func runRestrictedAttempt(ctx context.Context, options restrictedRunOptions, outcome quality.NativeOutcome) (restrictedAttemptResult, error) {
	findings := outcome.BlockingFindings()
	if len(findings) == 0 {
		return restrictedAttemptResult{}, errors.New("restricted attempt requires frozen P0/P1 findings")
	}
	if options.Attempt < 1 || options.Attempt > 2 {
		return restrictedAttemptResult{}, errors.New("restricted attempt number must be 1 or 2")
	}
	paths, err := prepareRestrictedAttemptPaths(options.Session, options.Attempt)
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	policy, err := reviewsession.ReadRegularFile(options.Session.RestrictedAdjudicationPolicyPath(), maxNativeOutputBytes)
	if err != nil {
		return restrictedAttemptResult{}, fmt.Errorf("read restricted adjudication policy: %w", err)
	}
	schema, err := reviewsession.ReadRegularFile(options.Session.RestrictedAdjudicationSchemaPath(), maxNativeOutputBytes)
	if err != nil {
		return restrictedAttemptResult{}, fmt.Errorf("read restricted adjudication schema: %w", err)
	}
	invocation := options.Provider.buildRestrictedInvocation(restrictedInvocationOptions{
		Session: options.Session, Plan: options.Plan, Findings: findings, Model: options.Model,
		ReasoningEffort: options.ReasoningEffort, LeaseFile: options.LeaseFile, SessionLockFile: options.SessionLockFile,
		Policy: policy, OutputSchema: schema, CapturePaths: paths,
	})
	invocation.stage = string(StateRestrictedRunning)
	invocation.attempt = options.Attempt
	invocation.heartbeatInterval = options.HeartbeatInterval
	invocation.progress = options.ProgressWriter
	trustedDiff, err := options.Session.ReadTrustedDiff(maxNativeOutputBytes)
	if err != nil {
		return restrictedAttemptResult{}, fmt.Errorf("read trusted diff for restricted adjudication: %w", err)
	}
	record := newAttemptRecord(options.Attempt, options.Resumed)
	if options.StartedAt == "" {
		return restrictedAttemptResult{}, errors.New("restricted attempt start time is required")
	}
	record.StartedAt = options.StartedAt
	started := time.Now()
	processErr := runNativeProcess(ctx, invocation)
	materializeErr := materializeProviderFinalMessage(options.Provider, invocation.paths)
	frozen, err := freezeNativeArtifacts(invocation.paths, options.Provider)
	if err != nil {
		return restrictedAttemptResult{}, fmt.Errorf("freeze restricted adjudication: %w", err)
	}
	metrics := collectStageRunMetrics(options.Session.Request(), "RESTRICTED", options.Attempt, time.Since(started), int64(len(trustedDiff)), frozen)
	if err := writeExclusiveJSON(invocation.paths.metrics, metrics); err != nil {
		return restrictedAttemptResult{}, fmt.Errorf("write restricted adjudication metrics: %w", err)
	}
	record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.Artifacts, err = restrictedAttemptArtifacts(options.Session, paths)
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	if processErr != nil {
		failure := classifyRestrictedProcessFailure(ctx, processErr, paths.stderr)
		record.Status = "FAILED"
		record.Failure = &failure
		if err := writeAttemptRecord(options.Session.Directory(), &record); err != nil {
			return restrictedAttemptResult{}, err
		}
		return restrictedAttemptResult{Outcome: outcome, Record: record}, nil
	}
	if materializeErr != nil || frozen.ProtocolError != nil {
		failure := AttemptFailure{Class: FailureProviderProtocol, Reason: "restricted Provider transcript is not verifiable"}
		record.Status = "FAILED"
		record.Failure = &failure
		if err := writeAttemptRecord(options.Session.Directory(), &record); err != nil {
			return restrictedAttemptResult{}, err
		}
		return restrictedAttemptResult{Outcome: outcome, Record: record}, nil
	}
	decisions, err := decodeRestrictedAdjudication(frozen.FinalMessage, findings, options.Session.RepositoryDirectory())
	if err != nil {
		failure := AttemptFailure{Class: FailureProviderProtocol, Reason: "restricted adjudication evidence failed validation"}
		record.Status = "FAILED"
		record.Failure = &failure
		if err := writeAttemptRecord(options.Session.Directory(), &record); err != nil {
			return restrictedAttemptResult{}, err
		}
		return restrictedAttemptResult{Outcome: outcome, Record: record}, nil
	}
	adjudicated, err := outcome.ApplyRestrictedAdjudication(decisions)
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	record.Status = "SUCCEEDED"
	record.Failure = nil
	if err := writeAttemptRecord(options.Session.Directory(), &record); err != nil {
		return restrictedAttemptResult{}, err
	}
	return restrictedAttemptResult{Outcome: adjudicated, Record: record}, nil
}

func buildRestrictedAdjudicationPrompt(plan reviewplan.Decision, findings []quality.NativeFinding) string {
	encoded, _ := json.Marshal(findings)
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Adjudicate only the frozen P0/P1 findings below for full committed change %s..%s.\n", plan.Request.BaseCommit, plan.Request.TargetCommit)
	if plan.ReviewScope == quality.ReviewScopeIncremental {
		fmt.Fprintf(&prompt, "This is the second and final automatic review round; the repair delta is %s..%s, while previous blockers may be located anywhere in the full change.\n", plan.ProviderRequest.BaseCommit, plan.ProviderRequest.TargetCommit)
	}
	prompt.WriteString("\nUse target-reachable repository evidence and the exact full Git diff. Do not inspect later commits, remotes, sibling experiments, historical outcomes, or expected labels. Do not add findings. Return exactly one schema-valid adjudication per supplied finding_id, in the supplied order.\n\nFrozen native P0/P1 findings:\n")
	prompt.Write(encoded)
	prompt.WriteByte('\n')
	return prompt.String()
}

func prepareRestrictedAttemptPaths(session reviewsession.NativeSession, attempt int) (capturePaths, error) {
	root := filepath.Join(session.OutputDirectory(), "restricted-attempts")
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return capturePaths{}, err
	}
	if err := validateCheckpointDirectory(root); err != nil {
		return capturePaths{}, err
	}
	directory := restrictedAttemptDirectory(session.Directory(), attempt)
	if err := os.Mkdir(directory, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return capturePaths{}, errors.New("restricted attempt directory already exists")
		}
		return capturePaths{}, err
	}
	if err := validateCheckpointDirectory(directory); err != nil {
		return capturePaths{}, err
	}
	return restrictedAttemptPaths(session, attempt), nil
}

func restrictedAttemptPaths(session reviewsession.NativeSession, attempt int) capturePaths {
	directory := restrictedAttemptDirectory(session.Directory(), attempt)
	return capturePaths{
		finalMessage:   filepath.Join(directory, "restricted-adjudication.json"),
		jsonl:          filepath.Join(directory, "restricted-adjudication.stdout.log"),
		stderr:         filepath.Join(directory, "restricted-adjudication.stderr.log"),
		freezeManifest: filepath.Join(directory, "restricted-adjudication-freeze.json"),
		metrics:        filepath.Join(directory, "restricted-adjudication-metrics.json"),
	}
}

func ensureInterruptedAttemptPaths(session reviewsession.NativeSession, attempt int) (capturePaths, error) {
	root := filepath.Join(session.OutputDirectory(), "restricted-attempts")
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return capturePaths{}, err
	}
	if err := validateCheckpointDirectory(root); err != nil {
		return capturePaths{}, err
	}
	directory := restrictedAttemptDirectory(session.Directory(), attempt)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return capturePaths{}, err
	}
	if err := validateCheckpointDirectory(directory); err != nil {
		return capturePaths{}, err
	}
	paths := restrictedAttemptPaths(session, attempt)
	if _, err := os.Lstat(paths.freezeManifest); errors.Is(err, os.ErrNotExist) {
		for _, path := range []string{paths.jsonl, paths.stderr} {
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				if err := writeExclusiveFile(path, nil); err != nil {
					return capturePaths{}, err
				}
			} else if err != nil {
				return capturePaths{}, err
			}
		}
	} else if err != nil {
		return capturePaths{}, err
	}
	return paths, nil
}

func recoverOrSealRunningAttempt(session reviewsession.NativeSession, provider Provider, checkpoint *SessionCheckpoint, outcome quality.NativeOutcome) (restrictedAttemptResult, error) {
	if len(checkpoint.RestrictedAttempts) == 0 {
		return restrictedAttemptResult{}, errors.New("restricted-running checkpoint has no attempt")
	}
	running := checkpoint.RestrictedAttempts[len(checkpoint.RestrictedAttempts)-1]
	if running.Status != "RUNNING" || running.Attempt != len(checkpoint.RestrictedAttempts) {
		return restrictedAttemptResult{}, errors.New("restricted-running checkpoint ledger is invalid")
	}
	attemptPath := filepath.Join(restrictedAttemptDirectory(session.Directory(), running.Attempt), "attempt.json")
	if _, err := os.Lstat(attemptPath); err == nil {
		raw, err := readCheckpointFile(attemptPath, 1<<20, 0o400)
		if err != nil {
			return restrictedAttemptResult{}, err
		}
		stored, err := quality.DecodeStrict[RestrictedAttemptRecord](bytes.NewReader(raw))
		if err != nil || stored.Attempt != running.Attempt || stored.Resumed != running.Resumed || stored.StartedAt != running.StartedAt {
			return restrictedAttemptResult{}, errors.New("completed restricted attempt does not match running checkpoint")
		}
		if err := verifyAttemptRecord(session.Directory(), stored); err != nil {
			return restrictedAttemptResult{}, err
		}
		if stored.Status != "SUCCEEDED" {
			return restrictedAttemptResult{Outcome: outcome, Record: stored}, nil
		}
		frozen, err := loadFrozenNativeArtifacts(restrictedAttemptPaths(session, stored.Attempt), provider)
		if err != nil {
			return restrictedAttemptResult{}, err
		}
		decisions, err := decodeRestrictedAdjudication(frozen.FinalMessage, outcome.BlockingFindings(), session.RepositoryDirectory())
		if err != nil {
			return restrictedAttemptResult{}, err
		}
		adjudicated, err := outcome.ApplyRestrictedAdjudication(decisions)
		if err != nil {
			return restrictedAttemptResult{}, err
		}
		return restrictedAttemptResult{Outcome: adjudicated, Record: stored}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return restrictedAttemptResult{}, err
	}

	paths, err := ensureInterruptedAttemptPaths(session, running.Attempt)
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	var frozen frozenNativeArtifacts
	manifestErr := error(nil)
	if _, statErr := os.Lstat(paths.freezeManifest); statErr == nil {
		frozen, manifestErr = loadFrozenNativeArtifacts(paths, provider)
	} else if errors.Is(statErr, os.ErrNotExist) {
		frozen, manifestErr = freezeNativeArtifacts(paths, provider)
	} else {
		manifestErr = statErr
	}
	err = manifestErr
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	if _, err := os.Lstat(paths.metrics); errors.Is(err, os.ErrNotExist) {
		trustedDiff, readErr := session.ReadTrustedDiff(maxNativeOutputBytes)
		if readErr != nil {
			return restrictedAttemptResult{}, readErr
		}
		started, parseErr := time.Parse(time.RFC3339Nano, running.StartedAt)
		if parseErr != nil {
			return restrictedAttemptResult{}, errors.New("restricted attempt start time is invalid")
		}
		elapsed := time.Since(started)
		if elapsed < 0 {
			elapsed = 0
		}
		metrics := collectStageRunMetrics(session.Request(), "RESTRICTED", running.Attempt, elapsed, int64(len(trustedDiff)), frozen)
		if err := writeExclusiveJSON(paths.metrics, metrics); err != nil {
			return restrictedAttemptResult{}, err
		}
	} else if err != nil {
		return restrictedAttemptResult{}, err
	} else if err := validateRestrictedMetrics(paths.metrics, running.Attempt); err != nil {
		return restrictedAttemptResult{}, err
	}
	running.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	running.Status = "FAILED"
	failure := AttemptFailure{Class: FailureProcessInterrupted, Retryable: true, Reason: "restricted Provider process was interrupted before checkpoint completion"}
	running.Failure = &failure
	running.Artifacts, err = restrictedAttemptArtifacts(session, paths)
	if err != nil {
		return restrictedAttemptResult{}, err
	}
	if err := writeAttemptRecord(session.Directory(), &running); err != nil {
		return restrictedAttemptResult{}, err
	}
	return restrictedAttemptResult{Outcome: outcome, Record: running}, nil
}

func validateRestrictedMetrics(path string, attempt int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validatePrivateRegularFile(info); err != nil {
		return err
	}
	raw, err := reviewsession.ReadRegularFile(path, 1<<20)
	if err != nil {
		return err
	}
	metrics, err := quality.DecodeStrict[NativeRunMetrics](bytes.NewReader(raw))
	if err != nil || metrics.SchemaVersion != 2 || metrics.Stage != "RESTRICTED" || metrics.Attempt != attempt || metrics.DurationMS < 0 {
		return errors.New("restricted attempt metrics are invalid")
	}
	return nil
}

func restrictedCapturePathsFromSession(session reviewsession.NativeSession) capturePaths {
	artifacts := session.Artifacts()
	return capturePaths{
		finalMessage: artifacts.RestrictedFinalMessagePath(), jsonl: artifacts.RestrictedJSONLPath(),
		stderr: artifacts.RestrictedStderrPath(), freezeManifest: artifacts.RestrictedFreezeManifestPath(),
		metrics: artifacts.RestrictedMetricsPath(),
	}
}

func restrictedAttemptArtifacts(session reviewsession.NativeSession, paths capturePaths) ([]ArtifactDigest, error) {
	values := []struct {
		name     string
		path     string
		required bool
	}{
		{name: "restricted_final_message", path: paths.finalMessage},
		{name: "restricted_stdout", path: paths.jsonl, required: true},
		{name: "restricted_stderr", path: paths.stderr, required: true},
		{name: "restricted_freeze_manifest", path: paths.freezeManifest, required: true},
		{name: "restricted_metrics", path: paths.metrics, required: true},
	}
	artifacts := make([]ArtifactDigest, 0, len(values))
	for _, value := range values {
		artifact, err := digestArtifact(session.Directory(), value.name, value.path, value.required)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func classifyRestrictedProcessFailure(ctx context.Context, processErr error, stderrPath string) AttemptFailure {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return AttemptFailure{Class: FailureDeadlineExceeded, Retryable: true, Reason: "restricted Provider deadline exceeded"}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return AttemptFailure{Class: FailureProcessInterrupted, Retryable: true, Reason: "restricted Provider process was interrupted"}
	}
	raw, _ := reviewsession.ReadRegularFile(stderrPath, 1<<20)
	message := strings.ToLower(processErr.Error() + " " + string(raw))
	switch {
	case strings.Contains(message, "quota"):
		return AttemptFailure{Class: FailureProviderQuota, Retryable: true, Reason: "Provider quota was unavailable"}
	case strings.Contains(message, "capacity") || strings.Contains(message, "overloaded"):
		return AttemptFailure{Class: FailureProviderCapacity, Retryable: true, Reason: "Provider capacity was unavailable"}
	case strings.Contains(message, "rate limit") || strings.Contains(message, "rate_limit") || strings.Contains(message, "too many requests") || strings.Contains(message, "429"):
		return AttemptFailure{Class: FailureProviderRateLimit, Retryable: true, Reason: "Provider rate limit was reached"}
	case strings.Contains(message, "deadline") || strings.Contains(message, "timed out") || strings.Contains(message, "timeout"):
		return AttemptFailure{Class: FailureDeadlineExceeded, Retryable: true, Reason: "restricted Provider deadline exceeded"}
	default:
		return AttemptFailure{Class: FailureProviderProcess, Retryable: false, Reason: "restricted Provider process failed"}
	}
}

func decodeRestrictedAdjudication(raw []byte, findings []quality.NativeFinding, repository string) ([]quality.RestrictedFindingDecision, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	encoded, exists := root["adjudications"]
	if !exists || len(root) != 1 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return nil, errors.New("restricted adjudication root must contain only a non-null adjudications array")
	}
	var itemShapes []map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &itemShapes); err != nil || itemShapes == nil {
		return nil, errors.New("restricted adjudications must be an array of objects")
	}
	required := map[string]struct{}{
		"finding_id": {}, "validity": {}, "severity": {}, "trigger_confidence": {}, "evidence_level": {},
		"introduced_or_worsened_by_change": {}, "trigger_condition_is_concrete": {}, "causal_chain_is_complete": {},
		"finding_is_not_style_preference": {}, "recommended_disposition": {}, "evidence_refs": {},
		"uncertainties": {}, "reason": {},
	}
	for index, shape := range itemShapes {
		if len(shape) != len(required) {
			return nil, fmt.Errorf("restricted adjudication %d fields are invalid", index)
		}
		for name := range required {
			if _, ok := shape[name]; !ok {
				return nil, fmt.Errorf("restricted adjudication %d is missing %s", index, name)
			}
		}
	}
	envelope, err := quality.DecodeStrict[restrictedAdjudicationEnvelope](bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if len(envelope.Adjudications) != len(findings) {
		return nil, errors.New("restricted adjudication count does not match frozen findings")
	}
	decisions := make([]quality.RestrictedFindingDecision, len(findings))
	for index, value := range envelope.Adjudications {
		if value.FindingID != findings[index].ID {
			return nil, fmt.Errorf("restricted adjudication %d finding id or order does not match", index)
		}
		if !oneOf(value.Validity, "SUPPORTED", "CONTRADICTED", "INSUFFICIENT") ||
			!oneOf(value.Severity, "S1", "S2", "S3") ||
			!oneOf(value.TriggerConfidence, "T1", "T2", "T3") ||
			!oneOf(value.EvidenceLevel, "E1", "E2", "E3") ||
			!oneOf(value.RecommendedDisposition, "BLOCK", "MANUAL_REVIEW", "ADVISORY", "REJECT") {
			return nil, fmt.Errorf("restricted adjudication %d contains an invalid enum", index)
		}
		if strings.TrimSpace(value.Reason) == "" || len(value.Reason) > 1500 {
			return nil, fmt.Errorf("restricted adjudication %d reason is invalid", index)
		}
		if value.Uncertainties == nil || value.EvidenceRefs == nil {
			return nil, fmt.Errorf("restricted adjudication %d arrays must be non-null", index)
		}
		for _, uncertainty := range value.Uncertainties {
			if strings.TrimSpace(uncertainty) == "" || len(uncertainty) > 500 {
				return nil, fmt.Errorf("restricted adjudication %d uncertainty is invalid", index)
			}
		}
		validEvidence := len(value.EvidenceRefs) > 0
		for _, ref := range value.EvidenceRefs {
			if !restrictedEvidenceRefValid(repository, ref) {
				validEvidence = false
			}
		}
		retain := value.Validity == "SUPPORTED" && value.Severity == "S3" &&
			value.TriggerConfidence == "T3" && oneOf(value.EvidenceLevel, "E2", "E3") &&
			value.IntroducedOrWorsenedByChange && value.TriggerConditionIsConcrete &&
			value.CausalChainIsComplete && value.FindingIsNotStylePreference && validEvidence
		decisions[index] = quality.RestrictedFindingDecision{FindingID: value.FindingID, Retain: retain}
	}
	return decisions, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func restrictedEvidenceRefValid(repository string, ref restrictedEvidenceRef) bool {
	if strings.TrimSpace(ref.Path) == "" || strings.Contains(ref.Path, "\\") || filepath.IsAbs(ref.Path) ||
		ref.StartLine < 1 || ref.EndLine < ref.StartLine || strings.TrimSpace(ref.Support) == "" || len(ref.Support) > 500 {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(ref.Path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != ref.Path {
		return false
	}
	current := repository
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	info, err := os.Lstat(current)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	raw, err := reviewsession.ReadRegularFile(current, maxRestrictedEvidenceFileBytes)
	if err != nil {
		return false
	}
	lineCount := 1
	if len(raw) > 0 {
		lineCount = bytes.Count(raw, []byte{'\n'})
		if raw[len(raw)-1] != '\n' {
			lineCount++
		}
	}
	return ref.EndLine <= lineCount
}
