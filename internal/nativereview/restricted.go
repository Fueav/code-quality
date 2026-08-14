package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Session         reviewsession.NativeSession
	Plan            reviewplan.Decision
	Provider        Provider
	Model           string
	ReasoningEffort string
	LeaseFile       *os.File
}

func runRestrictedAdjudication(ctx context.Context, options restrictedRunOptions, outcome quality.NativeOutcome) (quality.NativeOutcome, error) {
	findings := outcome.BlockingFindings()
	if len(findings) == 0 {
		return outcome, nil
	}
	policy, err := reviewsession.ReadRegularFile(options.Session.RestrictedAdjudicationPolicyPath(), maxNativeOutputBytes)
	if err != nil {
		return quality.NativeOutcome{}, fmt.Errorf("read restricted adjudication policy: %w", err)
	}
	schema, err := reviewsession.ReadRegularFile(options.Session.RestrictedAdjudicationSchemaPath(), maxNativeOutputBytes)
	if err != nil {
		return quality.NativeOutcome{}, fmt.Errorf("read restricted adjudication schema: %w", err)
	}
	invocation := options.Provider.buildRestrictedInvocation(restrictedInvocationOptions{
		Session: options.Session, Plan: options.Plan, Findings: findings, Model: options.Model,
		ReasoningEffort: options.ReasoningEffort, LeaseFile: options.LeaseFile,
		Policy: policy, OutputSchema: schema,
	})
	trustedDiff, err := options.Session.ReadTrustedDiff(maxNativeOutputBytes)
	if err != nil {
		return quality.NativeOutcome{}, fmt.Errorf("read trusted diff for restricted adjudication: %w", err)
	}
	started := time.Now()
	processErr := runNativeProcess(ctx, invocation)
	materializeErr := materializeProviderFinalMessage(options.Provider, invocation.paths)
	frozen, err := freezeNativeArtifacts(invocation.paths, options.Provider)
	if err != nil {
		return quality.NativeOutcome{}, fmt.Errorf("freeze restricted adjudication: %w", err)
	}
	processErr = errors.Join(processErr, materializeErr, frozen.ProtocolError)
	metrics := collectRunMetrics(options.Session.Request(), time.Since(started), int64(len(trustedDiff)), frozen)
	if err := writeExclusiveJSON(invocation.paths.metrics, metrics); err != nil {
		return quality.NativeOutcome{}, fmt.Errorf("write restricted adjudication metrics: %w", err)
	}
	if processErr != nil {
		return outcome.RestrictedAdjudicationFailure()
	}
	decisions, err := decodeRestrictedAdjudication(frozen.FinalMessage, findings, options.Session.RepositoryDirectory())
	if err != nil {
		return outcome.RestrictedAdjudicationFailure()
	}
	return outcome.ApplyRestrictedAdjudication(decisions)
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

func restrictedCapturePathsFromSession(session reviewsession.NativeSession) capturePaths {
	artifacts := session.Artifacts()
	return capturePaths{
		finalMessage:   artifacts.RestrictedFinalMessagePath(),
		jsonl:          artifacts.RestrictedJSONLPath(),
		stderr:         artifacts.RestrictedStderrPath(),
		freezeManifest: artifacts.RestrictedFreezeManifestPath(),
		metrics:        artifacts.RestrictedMetricsPath(),
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
