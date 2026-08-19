package nativereview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type SessionState string

const (
	StatePlanned             SessionState = "PLANNED"
	StateNativeRunning       SessionState = "NATIVE_RUNNING"
	StateNativeFrozen        SessionState = "NATIVE_FROZEN"
	StateRestrictedRunning   SessionState = "RESTRICTED_RUNNING"
	StateRestrictedRetryable SessionState = "RESTRICTED_RETRYABLE"
	StatePublished           SessionState = "PUBLISHED"
	StateManualRequired      SessionState = "MANUAL_REQUIRED"
	StateTerminalError       SessionState = "TERMINAL_ERROR"
)

type FailureClass string

const (
	FailureProviderQuota      FailureClass = "PROVIDER_QUOTA"
	FailureProviderCapacity   FailureClass = "PROVIDER_CAPACITY"
	FailureProviderRateLimit  FailureClass = "PROVIDER_RATE_LIMIT"
	FailureDeadlineExceeded   FailureClass = "DEADLINE_EXCEEDED"
	FailureProcessInterrupted FailureClass = "PROCESS_INTERRUPTED"
	FailureProviderProcess    FailureClass = "PROVIDER_PROCESS_ERROR"
	FailureProviderProtocol   FailureClass = "PROVIDER_PROTOCOL_ERROR"
	FailureArtifactIntegrity  FailureClass = "ARTIFACT_INTEGRITY"
	FailureContractMismatch   FailureClass = "CONTRACT_MISMATCH"
	FailureTargetUnavailable  FailureClass = "TARGET_COMMIT_UNAVAILABLE"
)

type ArtifactDigest struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256,omitempty"`
}

type CheckpointContractArtifacts struct {
	NativePromptSHA256     string `json:"native_prompt_sha256"`
	RestrictedPromptSHA256 string `json:"restricted_prompt_sha256,omitempty"`
	RubricSHA256           string `json:"rubric_sha256"`
	RestrictedPolicySHA256 string `json:"restricted_policy_sha256"`
	RestrictedSchemaSHA256 string `json:"restricted_schema_sha256"`
	ProviderSchemaSHA256   string `json:"provider_schema_sha256"`
	ResultSchemaSHA256     string `json:"result_schema_sha256"`
}

type AttemptFailure struct {
	Class     FailureClass `json:"class"`
	Retryable bool         `json:"retryable"`
	Reason    string       `json:"reason"`
}

type RestrictedAttemptRecord struct {
	SchemaVersion int              `json:"schema_version"`
	Attempt       int              `json:"attempt"`
	Resumed       bool             `json:"resumed"`
	Status        string           `json:"status"`
	StartedAt     string           `json:"started_at"`
	FinishedAt    string           `json:"finished_at,omitempty"`
	Failure       *AttemptFailure  `json:"failure"`
	Artifacts     []ArtifactDigest `json:"artifacts"`
	AttemptDigest string           `json:"attempt_digest"`
}

type PublicationRecord struct {
	Status    string           `json:"status"`
	Artifacts []ArtifactDigest `json:"artifacts"`
}

type SessionCheckpoint struct {
	SchemaVersion            int                         `json:"schema_version"`
	ToolVersion              string                      `json:"tool_version"`
	Sequence                 int                         `json:"sequence"`
	State                    SessionState                `json:"state"`
	CheckpointDigest         string                      `json:"checkpoint_digest"`
	SessionDigest            string                      `json:"session_digest"`
	RepositoryRoot           string                      `json:"repository_root"`
	ReviewGoal               string                      `json:"review_goal"`
	Plan                     reviewplan.Decision         `json:"plan"`
	PreviousBlockingFindings []quality.NativeFinding     `json:"previous_blocking_findings"`
	ContractArtifacts        CheckpointContractArtifacts `json:"contract_artifacts"`
	InputArtifacts           []ArtifactDigest            `json:"input_artifacts"`
	NativeEvidence           []ArtifactDigest            `json:"native_evidence"`
	NativeOutcome            *quality.NativeReviewResult `json:"native_outcome"`
	FrozenBlockingFindings   []quality.NativeFinding     `json:"frozen_blocking_findings"`
	RestrictedAttempts       []RestrictedAttemptRecord   `json:"restricted_attempts"`
	AdoptedRestrictedAttempt *int                        `json:"adopted_restricted_attempt"`
	Resumed                  bool                        `json:"resumed"`
	ResumedSessionDigest     *string                     `json:"resumed_session_digest"`
	LastFailure              *AttemptFailure             `json:"last_failure"`
	Publication              PublicationRecord           `json:"publication"`
}

type SessionStatus struct {
	SchemaVersion                int             `json:"schema_version"`
	State                        SessionState    `json:"state"`
	ReviewKey                    string          `json:"review_key"`
	ContractDigest               string          `json:"contract_digest"`
	SessionDir                   string          `json:"session_dir"`
	NativeReviewReused           bool            `json:"native_review_reused"`
	NativeInvocationsThisRun     int             `json:"native_invocations_this_run"`
	RestrictedInvocationsThisRun int             `json:"restricted_invocations_this_run"`
	ProviderInvocationsThisRun   int             `json:"provider_invocations_this_run"`
	NativeAttempts               int             `json:"native_attempts"`
	RestrictedAttempts           int             `json:"restricted_attempts"`
	ProviderAttemptsTotal        int             `json:"provider_attempts_total"`
	AdoptedRestrictedAttempt     *int            `json:"adopted_restricted_attempt"`
	Resumed                      bool            `json:"resumed"`
	ResumedSessionDigest         *string         `json:"resumed_session_digest"`
	Failure                      *AttemptFailure `json:"failure,omitempty"`
}

var (
	checkpointDigestPattern = regexp.MustCompile(`^checkpoint-v1:sha256:[0-9a-f]{64}$`)
	sessionDigestPattern    = regexp.MustCompile(`^session-v1:sha256:[0-9a-f]{64}$`)
)

func newSessionCheckpoint(session reviewsession.NativeSession, plan reviewplan.Decision, goal string) (SessionCheckpoint, error) {
	previousBlockingFindings := plan.PreviousBlockingFindings()
	nativePrompt := []byte(buildPlanReviewPromptWithPrevious(plan, goal, plan.Contract.ExecutionProfile == quality.ExecutionProfileProductionCI, previousBlockingFindings))
	rubric, err := bundle.ReviewLens()
	if err != nil {
		return SessionCheckpoint{}, err
	}
	policy, err := bundle.RestrictedAdjudicationPolicy()
	if err != nil {
		return SessionCheckpoint{}, err
	}
	restrictedSchema, err := bundle.Schema("restricted-adjudication-output.schema.json")
	if err != nil {
		return SessionCheckpoint{}, err
	}
	resultSchema, err := bundle.Schema("review-result-v10.schema.json")
	if err != nil {
		return SessionCheckpoint{}, err
	}
	inputPaths := []struct {
		name string
		path string
	}{
		{name: "review_request", path: session.RequestPath()},
		{name: "session_metadata", path: session.MetadataPath()},
		{name: "trusted_diff", path: session.TrustedDiffPath()},
		{name: "provider_schema", path: session.OutputSchemaPath()},
		{name: "restricted_policy", path: session.RestrictedAdjudicationPolicyPath()},
		{name: "restricted_schema", path: session.RestrictedAdjudicationSchemaPath()},
	}
	inputs := make([]ArtifactDigest, 0, len(inputPaths))
	for _, input := range inputPaths {
		artifact, err := digestArtifact(session.Directory(), input.name, input.path, true)
		if err != nil {
			return SessionCheckpoint{}, err
		}
		inputs = append(inputs, artifact)
	}
	checkpoint := SessionCheckpoint{
		SchemaVersion: 1, ToolVersion: quality.SkillVersion, State: StatePlanned,
		RepositoryRoot: plan.RepositoryRoot(), ReviewGoal: strings.TrimSpace(goal), Plan: plan,
		PreviousBlockingFindings: previousBlockingFindings,
		ContractArtifacts: CheckpointContractArtifacts{
			NativePromptSHA256: quality.SHA256Digest(nativePrompt), RubricSHA256: quality.SHA256Digest(rubric),
			RestrictedPolicySHA256: quality.SHA256Digest(policy), RestrictedSchemaSHA256: quality.SHA256Digest(restrictedSchema),
			ProviderSchemaSHA256: plan.Contract.ProviderOutputSchema, ResultSchemaSHA256: quality.SHA256Digest(resultSchema),
		},
		InputArtifacts: inputs, NativeEvidence: []ArtifactDigest{}, FrozenBlockingFindings: []quality.NativeFinding{},
		RestrictedAttempts: []RestrictedAttemptRecord{}, Publication: PublicationRecord{Status: "UNPUBLISHED", Artifacts: []ArtifactDigest{}},
	}
	return checkpoint, nil
}

func checkpointNativeFrozen(checkpoint *SessionCheckpoint, session reviewsession.NativeSession, outcome quality.NativeOutcome) error {
	result := outcome.Result()
	checkpoint.NativeOutcome = &result
	checkpoint.FrozenBlockingFindings = outcome.BlockingFindings()
	checkpoint.ContractArtifacts.RestrictedPromptSHA256 = quality.SHA256Digest([]byte(buildRestrictedAdjudicationPrompt(checkpoint.Plan, checkpoint.FrozenBlockingFindings)))
	artifacts := session.Artifacts()
	paths := []struct {
		name string
		path string
	}{
		{name: "native_final_message", path: artifacts.FinalMessagePath()},
		{name: "native_stdout", path: artifacts.JSONLPath()},
		{name: "native_stderr", path: artifacts.StderrPath()},
		{name: "native_freeze_manifest", path: artifacts.FreezeManifestPath()},
		{name: "native_metrics", path: artifacts.MetricsPath()},
	}
	checkpoint.NativeEvidence = make([]ArtifactDigest, 0, len(paths))
	for _, value := range paths {
		artifact, err := digestArtifact(session.Directory(), value.name, value.path, true)
		if err != nil {
			return err
		}
		checkpoint.NativeEvidence = append(checkpoint.NativeEvidence, artifact)
	}
	checkpoint.State = StateNativeFrozen
	return nil
}

func checkpointPublication(checkpoint *SessionCheckpoint, session reviewsession.NativeSession) error {
	artifacts := session.Artifacts()
	paths := []struct {
		name string
		path string
	}{
		{name: "review_result", path: artifacts.ResultPath()},
		{name: "review_markdown", path: artifacts.MarkdownPath()},
		{name: "review_summary_json", path: artifacts.SummaryJSONPath()},
		{name: "review_summary_markdown", path: artifacts.SummaryMarkdownPath()},
	}
	checkpoint.Publication = PublicationRecord{Status: "PUBLISHED", Artifacts: []ArtifactDigest{}}
	for _, value := range paths {
		artifact, err := digestArtifact(session.Directory(), value.name, value.path, true)
		if err != nil {
			return err
		}
		checkpoint.Publication.Artifacts = append(checkpoint.Publication.Artifacts, artifact)
	}
	checkpoint.State = StatePublished
	return nil
}

func writeCheckpoint(sessionDir string, checkpoint *SessionCheckpoint) error {
	if err := validateCheckpointDirectory(sessionDir); err != nil {
		return err
	}
	checkpoint.Sequence++
	checkpoint.SessionDigest = computeSessionDigest(*checkpoint)
	checkpoint.CheckpointDigest = ""
	digest, err := hashPrefixed("checkpoint-v1:sha256:", checkpoint)
	if err != nil {
		return err
	}
	checkpoint.CheckpointDigest = digest
	path := filepath.Join(sessionDir, "checkpoint.json")
	if info, err := os.Lstat(path); err == nil {
		if err := validatePrivateRegularFile(info); err != nil || info.Mode().Perm() != 0o600 {
			return errors.New("checkpoint path is not a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(sessionDir, ".checkpoint-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		_ = temporary.Close()
		if !installed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if err := quality.EncodeJSON(temporary, checkpoint); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	installed = true
	return syncCheckpointDirectory(sessionDir)
}

func loadAndVerifyCheckpoint(sessionDir string) (SessionCheckpoint, error) {
	if err := validateCheckpointDirectory(sessionDir); err != nil {
		return SessionCheckpoint{}, err
	}
	raw, err := readCheckpointFile(filepath.Join(sessionDir, "checkpoint.json"), 16<<20, 0o600)
	if err != nil {
		return SessionCheckpoint{}, err
	}
	checkpoint, err := quality.DecodeStrict[SessionCheckpoint](bytes.NewReader(raw))
	if err != nil {
		return SessionCheckpoint{}, fmt.Errorf("decode checkpoint: %w", err)
	}
	if checkpoint.SchemaVersion != 1 || checkpoint.ToolVersion != quality.SkillVersion || checkpoint.Sequence < 1 {
		return SessionCheckpoint{}, errors.New("checkpoint is not a current v0.5.8 session")
	}
	wantCheckpointDigest := checkpoint.CheckpointDigest
	checkpoint.CheckpointDigest = ""
	gotCheckpointDigest, err := hashPrefixed("checkpoint-v1:sha256:", checkpoint)
	if err != nil {
		return SessionCheckpoint{}, err
	}
	checkpoint.CheckpointDigest = wantCheckpointDigest
	if !checkpointDigestPattern.MatchString(wantCheckpointDigest) || gotCheckpointDigest != wantCheckpointDigest {
		return SessionCheckpoint{}, errors.New("checkpoint digest does not match its contents")
	}
	if got := computeSessionDigest(checkpoint); !sessionDigestPattern.MatchString(checkpoint.SessionDigest) || got != checkpoint.SessionDigest {
		return SessionCheckpoint{}, errors.New("session digest does not match checkpoint evidence")
	}
	if checkpoint.Plan.Status != reviewplan.StatusReady || checkpoint.Plan.ReviewKey == "" || checkpoint.Plan.ContractDigest == "" ||
		checkpoint.Plan.Contract.ToolVersion != quality.SkillVersion || checkpoint.Plan.Contract.ResultSchemaVersion != quality.NativeResultSchemaVersion {
		return SessionCheckpoint{}, errors.New("checkpoint review plan is not resumable")
	}
	if err := verifyCheckpointPlanIdentity(checkpoint); err != nil {
		return SessionCheckpoint{}, err
	}
	if err := validateCheckpointState(checkpoint); err != nil {
		return SessionCheckpoint{}, err
	}
	if !filepath.IsAbs(checkpoint.RepositoryRoot) {
		return SessionCheckpoint{}, errors.New("checkpoint repository root must be absolute")
	}
	for _, artifact := range append(append([]ArtifactDigest{}, checkpoint.InputArtifacts...), checkpoint.NativeEvidence...) {
		if err := verifyArtifact(sessionDir, artifact); err != nil {
			return SessionCheckpoint{}, fmt.Errorf("verify %s: %w", artifact.Name, err)
		}
	}
	if err := verifyContractArtifacts(checkpoint); err != nil {
		return SessionCheckpoint{}, err
	}
	if checkpoint.NativeOutcome != nil {
		outcome, err := quality.RestoreNativeOutcome(*checkpoint.NativeOutcome)
		if err != nil {
			return SessionCheckpoint{}, err
		}
		result := outcome.Result()
		if !reflect.DeepEqual(result.ReviewIdentity, checkpoint.Plan.ReviewIdentity) ||
			!reflect.DeepEqual(result.Request, checkpoint.Plan.Request) || strings.TrimSpace(result.ReviewGoal) != strings.TrimSpace(checkpoint.ReviewGoal) ||
			!reflect.DeepEqual(result.PreviousBlockingFindings, checkpoint.PreviousBlockingFindings) {
			return SessionCheckpoint{}, errors.New("Native outcome does not match the frozen checkpoint plan")
		}
		if !reflect.DeepEqual(outcome.BlockingFindings(), checkpoint.FrozenBlockingFindings) {
			return SessionCheckpoint{}, errors.New("frozen blocking finding content or order changed")
		}
	}
	for index, attempt := range checkpoint.RestrictedAttempts {
		if attempt.Attempt != index+1 || attempt.SchemaVersion != 1 {
			return SessionCheckpoint{}, errors.New("restricted attempt ledger is not contiguous")
		}
		if attempt.Status == "RUNNING" {
			continue
		}
		if err := verifyAttemptRecord(sessionDir, attempt); err != nil {
			return SessionCheckpoint{}, err
		}
	}
	if checkpoint.Publication.Status == "PUBLISHED" {
		for _, artifact := range checkpoint.Publication.Artifacts {
			if err := verifyArtifact(sessionDir, artifact); err != nil {
				return SessionCheckpoint{}, fmt.Errorf("verify publication %s: %w", artifact.Name, err)
			}
		}
	}
	return checkpoint, nil
}

func verifyCheckpointPlanIdentity(checkpoint SessionCheckpoint) error {
	expected, err := quality.BuildReviewIdentity(quality.ReviewIdentityInput{
		Contract: checkpoint.Plan.Contract, Request: checkpoint.Plan.Request, ReviewGoal: checkpoint.ReviewGoal,
		ReviewScope: checkpoint.Plan.ReviewScope, BaseRef: checkpoint.Plan.BaseRef, HeadRef: checkpoint.Plan.HeadRef,
		BaseTipCommit: checkpoint.Plan.BaseTipCommit, MergeBase: checkpoint.Plan.MergeBase,
		ParentReviewKey: checkpoint.Plan.ParentReviewKey, PreviousHead: checkpoint.Plan.PreviousHead,
		CurrentHead: checkpoint.Plan.CurrentHead, DeltaChangedFiles: checkpoint.Plan.DeltaChangedFiles,
	})
	if err != nil || !reflect.DeepEqual(expected, checkpoint.Plan.ReviewIdentity) {
		return errors.New("checkpoint review identity does not match its contract and scope")
	}
	if problems := quality.ValidateRequest(checkpoint.Plan.ProviderRequest); len(problems) > 0 || checkpoint.Plan.ProviderRequest.TargetCommit != checkpoint.Plan.CurrentHead {
		return errors.New("checkpoint Provider request is invalid")
	}
	return nil
}

func validateCheckpointState(checkpoint SessionCheckpoint) error {
	if checkpoint.Resumed != (checkpoint.ResumedSessionDigest != nil) ||
		(checkpoint.ResumedSessionDigest != nil && !sessionDigestPattern.MatchString(*checkpoint.ResumedSessionDigest)) {
		return errors.New("checkpoint resume audit is invalid")
	}
	if len(checkpoint.RestrictedAttempts) > 2 {
		return errors.New("restricted attempt limit exceeded")
	}
	if err := validateArtifactNames(checkpoint.InputArtifacts, []string{
		"review_request", "session_metadata", "trusted_diff", "provider_schema", "restricted_policy", "restricted_schema",
	}); err != nil {
		return fmt.Errorf("checkpoint input artifacts: %w", err)
	}
	if checkpoint.NativeOutcome != nil {
		if err := validateArtifactNames(checkpoint.NativeEvidence, []string{
			"native_final_message", "native_stdout", "native_stderr", "native_freeze_manifest", "native_metrics",
		}); err != nil {
			return fmt.Errorf("checkpoint Native evidence: %w", err)
		}
	}
	for index, attempt := range checkpoint.RestrictedAttempts {
		if attempt.Attempt != index+1 || attempt.SchemaVersion != 1 {
			return errors.New("restricted attempt ledger is not contiguous")
		}
		switch attempt.Status {
		case "RUNNING":
			if index != len(checkpoint.RestrictedAttempts)-1 || attempt.FinishedAt != "" || attempt.Failure != nil || len(attempt.Artifacts) != 0 || attempt.AttemptDigest != "" {
				return errors.New("running restricted attempt record is invalid")
			}
		case "FAILED":
			if attempt.FinishedAt == "" || attempt.Failure == nil || len(attempt.Artifacts) == 0 || attempt.AttemptDigest == "" {
				return errors.New("failed restricted attempt record is incomplete")
			}
			if err := validateArtifactNames(attempt.Artifacts, []string{
				"restricted_final_message", "restricted_stdout", "restricted_stderr", "restricted_freeze_manifest", "restricted_metrics",
			}); err != nil {
				return fmt.Errorf("failed restricted attempt artifacts: %w", err)
			}
		case "SUCCEEDED":
			if attempt.FinishedAt == "" || attempt.Failure != nil || len(attempt.Artifacts) == 0 || attempt.AttemptDigest == "" {
				return errors.New("successful restricted attempt record is incomplete")
			}
			if err := validateArtifactNames(attempt.Artifacts, []string{
				"restricted_final_message", "restricted_stdout", "restricted_stderr", "restricted_freeze_manifest", "restricted_metrics",
			}); err != nil {
				return fmt.Errorf("successful restricted attempt artifacts: %w", err)
			}
		default:
			return errors.New("restricted attempt status is invalid")
		}
		if attempt.Resumed && !checkpoint.Resumed {
			return errors.New("resumed restricted attempt requires session resume audit")
		}
		if index == 1 && !attempt.Resumed {
			return errors.New("second restricted attempt must be launched by resume")
		}
	}
	if checkpoint.AdoptedRestrictedAttempt != nil {
		value := *checkpoint.AdoptedRestrictedAttempt
		if value < 1 || value > len(checkpoint.RestrictedAttempts) || checkpoint.RestrictedAttempts[value-1].Status != "SUCCEEDED" {
			return errors.New("adopted restricted attempt is outside the successful ledger")
		}
	}
	if checkpoint.NativeOutcome == nil && checkpoint.State != StatePlanned && checkpoint.State != StateNativeRunning {
		return errors.New("checkpoint state requires a frozen Native outcome")
	}
	switch checkpoint.State {
	case StateNativeFrozen:
		if len(checkpoint.RestrictedAttempts) != 0 {
			return errors.New("Native-frozen checkpoint cannot contain restricted attempts")
		}
	case StateRestrictedRunning:
		if len(checkpoint.RestrictedAttempts) == 0 || checkpoint.RestrictedAttempts[len(checkpoint.RestrictedAttempts)-1].Status != "RUNNING" {
			return errors.New("restricted-running checkpoint has no running attempt")
		}
	case StateRestrictedRetryable:
		if len(checkpoint.RestrictedAttempts) != 1 || checkpoint.RestrictedAttempts[0].Status != "FAILED" ||
			checkpoint.RestrictedAttempts[0].Failure == nil || !checkpoint.RestrictedAttempts[0].Failure.Retryable {
			return errors.New("restricted-retryable checkpoint has no retryable first failure")
		}
	case StateManualRequired:
		if len(checkpoint.RestrictedAttempts) != 2 || checkpoint.RestrictedAttempts[1].Status != "FAILED" {
			return errors.New("manual-required checkpoint must contain two failed restricted attempts")
		}
	case StatePublished:
		if checkpoint.Publication.Status != "PUBLISHED" || len(checkpoint.Publication.Artifacts) == 0 ||
			(len(checkpoint.RestrictedAttempts) > 0 && (checkpoint.RestrictedAttempts[len(checkpoint.RestrictedAttempts)-1].Status != "SUCCEEDED" || checkpoint.AdoptedRestrictedAttempt == nil)) ||
			(len(checkpoint.RestrictedAttempts) == 0 && checkpoint.AdoptedRestrictedAttempt != nil) {
			return errors.New("published checkpoint is incomplete")
		}
		if err := validateArtifactNames(checkpoint.Publication.Artifacts, []string{
			"review_result", "review_markdown", "review_summary_json", "review_summary_markdown",
		}); err != nil {
			return fmt.Errorf("published artifacts: %w", err)
		}
	case StatePlanned, StateNativeRunning, StateTerminalError:
	default:
		return errors.New("checkpoint state is invalid")
	}
	return nil
}

func validateArtifactNames(artifacts []ArtifactDigest, expected []string) error {
	if len(artifacts) != len(expected) {
		return fmt.Errorf("expected %d entries, got %d", len(expected), len(artifacts))
	}
	for index, name := range expected {
		if artifacts[index].Name != name {
			return fmt.Errorf("entry %d must be %s", index, name)
		}
	}
	return nil
}

func verifyContractArtifacts(checkpoint SessionCheckpoint) error {
	nativePrompt := []byte(buildPlanReviewPromptWithPrevious(checkpoint.Plan, checkpoint.ReviewGoal, checkpoint.Plan.Contract.ExecutionProfile == quality.ExecutionProfileProductionCI, checkpoint.PreviousBlockingFindings))
	rubric, err := bundle.ReviewLens()
	if err != nil {
		return err
	}
	policy, err := bundle.RestrictedAdjudicationPolicy()
	if err != nil {
		return err
	}
	restrictedSchema, err := bundle.Schema("restricted-adjudication-output.schema.json")
	if err != nil {
		return err
	}
	resultSchema, err := bundle.Schema("review-result-v10.schema.json")
	if err != nil {
		return err
	}
	values := map[string][2]string{
		"native prompt":     {checkpoint.ContractArtifacts.NativePromptSHA256, quality.SHA256Digest(nativePrompt)},
		"review rubric":     {checkpoint.ContractArtifacts.RubricSHA256, quality.SHA256Digest(rubric)},
		"restricted policy": {checkpoint.ContractArtifacts.RestrictedPolicySHA256, quality.SHA256Digest(policy)},
		"restricted schema": {checkpoint.ContractArtifacts.RestrictedSchemaSHA256, quality.SHA256Digest(restrictedSchema)},
		"provider schema":   {checkpoint.ContractArtifacts.ProviderSchemaSHA256, checkpoint.Plan.Contract.ProviderOutputSchema},
		"result schema":     {checkpoint.ContractArtifacts.ResultSchemaSHA256, quality.SHA256Digest(resultSchema)},
	}
	if checkpoint.NativeOutcome != nil && len(checkpoint.FrozenBlockingFindings) > 0 {
		values["restricted prompt"] = [2]string{
			checkpoint.ContractArtifacts.RestrictedPromptSHA256,
			quality.SHA256Digest([]byte(buildRestrictedAdjudicationPrompt(checkpoint.Plan, checkpoint.FrozenBlockingFindings))),
		}
	}
	for name, pair := range values {
		if pair[0] == "" || pair[0] != pair[1] {
			return fmt.Errorf("%s digest does not match the frozen contract", name)
		}
	}
	if checkpoint.Plan.Contract.EvaluationRubricDigest != checkpoint.ContractArtifacts.RubricSHA256 ||
		checkpoint.Plan.Contract.RestrictedPolicyDigest != checkpoint.ContractArtifacts.RestrictedPolicySHA256 ||
		checkpoint.Plan.Contract.RestrictedSchemaDigest != checkpoint.ContractArtifacts.RestrictedSchemaSHA256 {
		return errors.New("review contract artifact digests do not match checkpoint")
	}
	inputDigests := map[string]string{}
	for _, artifact := range checkpoint.InputArtifacts {
		if !artifact.Present || artifact.SHA256 == "" {
			return fmt.Errorf("checkpoint input %s is not present", artifact.Name)
		}
		inputDigests[artifact.Name] = artifact.SHA256
	}
	for name, expected := range map[string]string{
		"provider_schema":   checkpoint.ContractArtifacts.ProviderSchemaSHA256,
		"restricted_policy": checkpoint.ContractArtifacts.RestrictedPolicySHA256,
		"restricted_schema": checkpoint.ContractArtifacts.RestrictedSchemaSHA256,
	} {
		if inputDigests[name] != expected {
			return fmt.Errorf("checkpoint input %s does not match its contract digest", name)
		}
	}
	return nil
}

func digestArtifact(sessionDir, name, path string, required bool) (ArtifactDigest, error) {
	relative, err := checkpointRelativePath(sessionDir, path)
	if err != nil {
		return ArtifactDigest{}, err
	}
	if err := validateArtifactParentDirectories(sessionDir, relative); err != nil {
		return ArtifactDigest{}, err
	}
	artifact := ArtifactDigest{Name: name, Path: relative}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return artifact, nil
	}
	if err != nil {
		return ArtifactDigest{}, err
	}
	if err := validatePrivateRegularFile(info); err != nil {
		return ArtifactDigest{}, err
	}
	raw, err := reviewsession.ReadRegularFile(path, 16<<20)
	if err != nil {
		return ArtifactDigest{}, err
	}
	artifact.Present = true
	artifact.Bytes = int64(len(raw))
	artifact.SHA256 = quality.SHA256Digest(raw)
	return artifact, nil
}

func verifyArtifact(sessionDir string, artifact ArtifactDigest) error {
	if strings.TrimSpace(artifact.Name) == "" || artifact.Path == "" || filepath.IsAbs(artifact.Path) || filepath.Clean(artifact.Path) != artifact.Path ||
		artifact.Path == ".." || strings.HasPrefix(artifact.Path, ".."+string(filepath.Separator)) {
		return errors.New("artifact path is invalid")
	}
	path := filepath.Join(sessionDir, artifact.Path)
	current, err := digestArtifact(sessionDir, artifact.Name, path, artifact.Present)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, artifact) {
		return errors.New("artifact digest changed")
	}
	return nil
}

func validateArtifactParentDirectories(sessionDir, relative string) error {
	current := sessionDir
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return errors.New("artifact parent path is invalid")
		}
		current = filepath.Join(current, part)
		if err := validateCheckpointDirectory(current); err != nil {
			return fmt.Errorf("artifact parent directory %s is unsafe: %w", part, err)
		}
	}
	return nil
}

func checkpointRelativePath(sessionDir, path string) (string, error) {
	sessionAbs, err := filepath.Abs(sessionDir)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(sessionAbs, pathAbs)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes the session")
	}
	return filepath.Clean(relative), nil
}

func validatePrivateRegularFile(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	mode := info.Mode().Perm()
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || (mode != 0o400 && mode != 0o600) || !ok ||
		stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return errors.New("artifact must be an owner-only regular non-symlink file")
	}
	return nil
}

func validateCheckpointDirectory(sessionDir string) error {
	if !filepath.IsAbs(sessionDir) {
		return errors.New("session directory must be absolute")
	}
	info, err := os.Lstat(sessionDir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("session directory must be owner-controlled mode 0700")
	}
	return nil
}

func readCheckpointFile(path string, limit int64, mode os.FileMode) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateRegularFile(info); err != nil || info.Mode().Perm() != mode {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("file mode does not match checkpoint contract")
	}
	return reviewsession.ReadRegularFile(path, limit)
}

func computeSessionDigest(checkpoint SessionCheckpoint) string {
	type stableAttempt struct {
		SchemaVersion int
		Attempt       int
		Resumed       bool
		Status        string
		Failure       *AttemptFailure
		Artifacts     []ArtifactDigest
	}
	attempts := make([]stableAttempt, 0, len(checkpoint.RestrictedAttempts))
	for _, attempt := range checkpoint.RestrictedAttempts {
		attempts = append(attempts, stableAttempt{
			SchemaVersion: attempt.SchemaVersion, Attempt: attempt.Attempt, Resumed: attempt.Resumed,
			Status: attempt.Status, Failure: attempt.Failure, Artifacts: attempt.Artifacts,
		})
	}
	input := struct {
		ToolVersion              string
		RepositoryRoot           string
		ReviewGoal               string
		Plan                     reviewplan.Decision
		PreviousBlockingFindings []quality.NativeFinding
		ContractArtifacts        CheckpointContractArtifacts
		InputArtifacts           []ArtifactDigest
		NativeEvidence           []ArtifactDigest
		NativeOutcome            *quality.NativeReviewResult
		FrozenBlockingFindings   []quality.NativeFinding
		RestrictedAttempts       []stableAttempt
		Publication              PublicationRecord
	}{
		checkpoint.ToolVersion, checkpoint.RepositoryRoot, checkpoint.ReviewGoal, checkpoint.Plan,
		checkpoint.PreviousBlockingFindings, checkpoint.ContractArtifacts, checkpoint.InputArtifacts,
		checkpoint.NativeEvidence, checkpoint.NativeOutcome, checkpoint.FrozenBlockingFindings,
		attempts, checkpoint.Publication,
	}
	digest, _ := hashPrefixed("session-v1:sha256:", input)
	return digest
}

func hashPrefixed(prefix string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(digest[:]), nil
}

func syncCheckpointDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

func writeAttemptRecord(sessionDir string, record *RestrictedAttemptRecord) error {
	record.SchemaVersion = 1
	record.AttemptDigest = ""
	digest, err := hashPrefixed("attempt-v1:sha256:", record)
	if err != nil {
		return err
	}
	record.AttemptDigest = digest
	directory := restrictedAttemptDirectory(sessionDir, record.Attempt)
	if err := validateCheckpointDirectory(directory); err != nil {
		return err
	}
	path := filepath.Join(directory, "attempt.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := quality.EncodeJSON(file, record); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return err
	}
	return syncCheckpointDirectory(directory)
}

func verifyAttemptRecord(sessionDir string, record RestrictedAttemptRecord) error {
	want := record.AttemptDigest
	record.AttemptDigest = ""
	got, err := hashPrefixed("attempt-v1:sha256:", record)
	if err != nil || want != got {
		return errors.New("restricted attempt digest changed")
	}
	for _, artifact := range record.Artifacts {
		if err := verifyArtifact(sessionDir, artifact); err != nil {
			return fmt.Errorf("verify restricted attempt %d %s: %w", record.Attempt, artifact.Name, err)
		}
	}
	raw, err := readCheckpointFile(filepath.Join(restrictedAttemptDirectory(sessionDir, record.Attempt), "attempt.json"), 1<<20, 0o400)
	if err != nil {
		return err
	}
	stored, err := quality.DecodeStrict[RestrictedAttemptRecord](bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(stored, recordWithDigest(record, want)) {
		return errors.New("restricted attempt record does not match checkpoint ledger")
	}
	return nil
}

func recordWithDigest(record RestrictedAttemptRecord, digest string) RestrictedAttemptRecord {
	record.AttemptDigest = digest
	return record
}

func restrictedAttemptDirectory(sessionDir string, attempt int) string {
	return filepath.Join(sessionDir, "output", "restricted-attempts", fmt.Sprintf("%04d", attempt))
}

func newAttemptRecord(attempt int, resumed bool) RestrictedAttemptRecord {
	return RestrictedAttemptRecord{
		SchemaVersion: 1, Attempt: attempt, Resumed: resumed, Status: "RUNNING",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Artifacts: []ArtifactDigest{},
	}
}

func statusFromCheckpoint(checkpoint SessionCheckpoint, sessionDir string, nativeThisRun, restrictedThisRun int) SessionStatus {
	return SessionStatus{
		SchemaVersion: 1, State: checkpoint.State, ReviewKey: checkpoint.Plan.ReviewKey,
		ContractDigest: checkpoint.Plan.ContractDigest, SessionDir: sessionDir,
		NativeReviewReused:       nativeThisRun == 0 && checkpoint.NativeOutcome != nil,
		NativeInvocationsThisRun: nativeThisRun, RestrictedInvocationsThisRun: restrictedThisRun,
		ProviderInvocationsThisRun: nativeThisRun + restrictedThisRun,
		NativeAttempts:             boolInt(checkpoint.NativeOutcome != nil), RestrictedAttempts: len(checkpoint.RestrictedAttempts),
		ProviderAttemptsTotal:    boolInt(checkpoint.NativeOutcome != nil) + len(checkpoint.RestrictedAttempts),
		AdoptedRestrictedAttempt: cloneIntPointer(checkpoint.AdoptedRestrictedAttempt), Resumed: checkpoint.Resumed,
		ResumedSessionDigest: cloneString(checkpoint.ResumedSessionDigest), Failure: cloneFailure(checkpoint.LastFailure),
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFailure(value *AttemptFailure) *AttemptFailure {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
