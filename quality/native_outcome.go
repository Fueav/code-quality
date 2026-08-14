package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// NativeOutcomeOptions contains only facts fixed before the provider process
// runs. Identity is authoritative when supplied by reviewplan.Build. The
// legacy host/model fields remain for package callers that construct a FULL
// outcome directly; they are normalized into the same versioned identity.
type NativeOutcomeOptions struct {
	Request                  ReviewRequest
	ProviderRequest          ReviewRequest
	Identity                 ReviewIdentity
	PreviousBlockingFindings []NativeFinding
	ReviewGoal               string
	Host                     string
	ExecutionProfile         string
	Model                    string
	ReasoningEffort          string
}

// NativeOutcome is the validated runtime outcome of one ordinary provider
// review. Its wire representation is NativeReviewResult schema v9.
type NativeOutcome struct {
	result NativeReviewResult
}

// ClassifyFrozenNativeReview applies the deterministic three-state contract to
// the exact frozen final-message bytes from one provider process.
func ClassifyFrozenNativeReview(options NativeOutcomeOptions, finalMessage []byte, processErr error) (NativeOutcome, error) {
	normalized, err := normalizeNativeOutcomeOptions(options)
	if err != nil {
		return NativeOutcome{}, err
	}
	contract := normalized.Identity.Contract
	result := NativeReviewResult{
		ReviewIdentity:             cloneReviewIdentity(normalized.Identity),
		SchemaVersion:              NativeResultSchemaVersion,
		EvaluationRubricVersion:    EvaluationRubricVersion,
		Request:                    cloneReviewRequest(normalized.Request),
		ReviewGoal:                 normalized.ReviewGoal,
		Findings:                   []NativeFinding{},
		PreviousBlockingFindings:   cloneNativeFindings(normalized.PreviousBlockingFindings),
		PreviousFindingResolutions: []PreviousFindingResolution{},
		NewFindings:                []NativeFinding{},
		Execution: NativeExecution{
			Host: contract.ProviderHost, ReviewMode: "native_review", ExecutionProfile: contract.ExecutionProfile,
			Model: contract.Model, ReasoningEffort: contract.ReasoningEffort,
			ProviderInvocations: 1, AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultError, RolloutMode: "release_gate", CIAction: "hold_release", Reasons: []string{},
		},
	}
	switch {
	case processErr != nil:
		result.Adjudication.Reasons = []string{"native review failed: " + processErr.Error()}
	case strings.TrimSpace(string(finalMessage)) == "":
		result.Adjudication.Reasons = []string{"native review output is missing or empty"}
	default:
		var classifyErr error
		if result.ReviewScope == ReviewScopeIncremental {
			result.PreviousFindingResolutions, result.NewFindings, result.Findings, classifyErr = classifyIncrementalResponse(
				finalMessage, normalized.PreviousBlockingFindings, normalized.Request, normalized.ProviderRequest,
			)
		} else {
			result.Findings, classifyErr = decodeFullProviderFindings(finalMessage, normalized.ProviderRequest.ChangedFiles)
			result.NewFindings = cloneNativeFindings(result.Findings)
		}
		if classifyErr != nil {
			result.Findings = []NativeFinding{}
			result.PreviousFindingResolutions = []PreviousFindingResolution{}
			result.NewFindings = []NativeFinding{}
			result.Adjudication.Reasons = []string{"native review structured output is invalid: " + classifyErr.Error()}
			break
		}
		classifyReleaseGate(&result)
	}
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		result.Findings = []NativeFinding{}
		result.PreviousFindingResolutions = []PreviousFindingResolution{}
		result.NewFindings = []NativeFinding{}
		result.Adjudication.SemanticResult = ResultError
		result.Adjudication.CIAction = "hold_release"
		result.Adjudication.Reasons = []string{"native review structured output failed validation: " + strings.Join(problems, "; ")}
		if fallbackProblems := ValidateNativeResult(result); len(fallbackProblems) > 0 {
			return NativeOutcome{}, fmt.Errorf("native review error outcome is invalid: %s", strings.Join(fallbackProblems, "; "))
		}
	}
	return NativeOutcome{result: result}, nil
}

func normalizeNativeOutcomeOptions(options NativeOutcomeOptions) (NativeOutcomeOptions, error) {
	options.Request = cloneReviewRequest(options.Request)
	if strings.TrimSpace(options.ProviderRequest.Repository) == "" {
		options.ProviderRequest = cloneReviewRequest(options.Request)
	} else {
		options.ProviderRequest = cloneReviewRequest(options.ProviderRequest)
	}
	options.ReviewGoal = strings.TrimSpace(options.ReviewGoal)
	if len(options.ReviewGoal) > 4000 {
		return NativeOutcomeOptions{}, errors.New("review goal exceeds 4000 bytes")
	}
	if options.Identity.ReviewKey == "" {
		host := strings.TrimSpace(options.Host)
		if host == "" {
			host = "codex"
		}
		model := strings.TrimSpace(options.Model)
		if model == "" {
			if host == "claude-code" {
				model = "opus"
			} else {
				model = "gpt-5.6-sol"
			}
		}
		effort := strings.TrimSpace(options.ReasoningEffort)
		if effort == "" {
			effort = "max"
		}
		profile := strings.TrimSpace(options.ExecutionProfile)
		if profile == "" {
			profile = ExecutionProfilePersonal
		}
		baseRef, headRef, baseTip := options.Request.TargetBranch, options.Request.TargetCommit, options.Request.BaseCommit
		if options.Request.Change != nil {
			baseRef, headRef, baseTip = options.Request.Change.BaseRef, options.Request.Change.HeadRef, options.Request.Change.BaseTipCommit
		}
		identity, err := BuildReviewIdentity(ReviewIdentityInput{
			Contract: NativeReviewContract{
				ToolVersion: SkillVersion, ResultSchemaVersion: NativeResultSchemaVersion,
				ProviderOutputSchema:  SHA256Digest([]byte("direct-full-native-review-output")),
				PromptContractVersion: "3", EvaluationRubricVersion: EvaluationRubricVersion,
				ProviderHost: host, Model: model, ReasoningEffort: effort, ExecutionProfile: profile,
			},
			Request: options.Request, ReviewGoal: options.ReviewGoal, ReviewScope: ReviewScopeFull,
			BaseRef: baseRef, HeadRef: headRef, BaseTipCommit: baseTip,
			MergeBase: options.Request.BaseCommit, CurrentHead: options.Request.TargetCommit,
			DeltaChangedFiles: []string{},
		})
		if err != nil {
			return NativeOutcomeOptions{}, fmt.Errorf("build native review identity: %w", err)
		}
		options.Identity = identity
	}
	expected, err := BuildReviewIdentity(ReviewIdentityInput{
		Contract: options.Identity.Contract, Request: options.Request, ReviewGoal: options.ReviewGoal,
		ReviewScope: options.Identity.ReviewScope, BaseRef: options.Identity.BaseRef, HeadRef: options.Identity.HeadRef,
		BaseTipCommit: options.Identity.BaseTipCommit, MergeBase: options.Identity.MergeBase,
		ParentReviewKey: options.Identity.ParentReviewKey, PreviousHead: options.Identity.PreviousHead,
		CurrentHead: options.Identity.CurrentHead, DeltaChangedFiles: options.Identity.DeltaChangedFiles,
	})
	if err != nil {
		return NativeOutcomeOptions{}, fmt.Errorf("validate native review identity: %w", err)
	}
	if expected.ReviewKey != options.Identity.ReviewKey || expected.ContractDigest != options.Identity.ContractDigest {
		return NativeOutcomeOptions{}, errors.New("native review identity does not match its canonical inputs")
	}
	if options.ProviderRequest.TargetCommit != options.Identity.CurrentHead {
		return NativeOutcomeOptions{}, errors.New("provider request target must equal current head")
	}
	if problems := ValidateRequest(options.ProviderRequest); len(problems) > 0 {
		return NativeOutcomeOptions{}, fmt.Errorf("provider request is invalid: %s", strings.Join(problems, "; "))
	}
	options.PreviousBlockingFindings = cloneNativeFindings(options.PreviousBlockingFindings)
	return options, nil
}

func classifyReleaseGate(result *NativeReviewResult) {
	blockingFindings := nativeBlockingFindingCount(result.Findings)
	if blockingFindings == 0 {
		result.Adjudication.SemanticResult = ResultPass
		result.Adjudication.CIAction = "continue_release"
		if len(result.Findings) == 0 {
			result.Adjudication.Reasons = []string{"no P0/P1 blocking issue was reported"}
		} else {
			result.Adjudication.Reasons = []string{fmt.Sprintf("no P0/P1 blocking issue was reported; %d advisory issue(s) were retained", len(result.Findings))}
		}
		return
	}
	result.Adjudication.SemanticResult = ResultBlock
	result.Adjudication.Reasons = []string{fmt.Sprintf("%d P0/P1 blocking issue(s) must be fixed before release", blockingFindings)}
}

// Result returns a detached copy of the schema-v9 wire representation.
func (outcome NativeOutcome) Result() NativeReviewResult {
	return cloneNativeReviewResult(outcome.result)
}

func (outcome NativeOutcome) EncodeJSON(writer io.Writer) error {
	if problems := ValidateNativeResult(outcome.result); len(problems) > 0 {
		return fmt.Errorf("native review outcome is invalid: %s", strings.Join(problems, "; "))
	}
	return EncodeJSON(writer, outcome.result)
}

func (outcome NativeOutcome) Markdown() string { return RenderNativeMarkdown(outcome.result) }
func (outcome NativeOutcome) Summary() NativeReleaseSummary {
	return SummarizeNativeResult(outcome.result)
}
func (outcome NativeOutcome) SemanticResult() string {
	return outcome.result.Adjudication.SemanticResult
}
func (outcome NativeOutcome) ProviderInvocations() int {
	return outcome.result.Execution.ProviderInvocations
}

// ValidatePublication rejects an intermediate native BLOCK before it can be
// mistaken for the final production-floor decision.
func (outcome NativeOutcome) ValidatePublication() error {
	if problems := ValidateNativeResult(outcome.result); len(problems) > 0 {
		return fmt.Errorf("native review outcome is invalid: %s", strings.Join(problems, "; "))
	}
	if outcome.result.Adjudication.SemanticResult == ResultBlock && outcome.result.Execution.ProviderInvocations != 2 {
		return errors.New("native P0/P1 candidates require restricted adjudication before publication")
	}
	if len(outcome.result.Execution.AdapterDrops) > 0 && outcome.result.Execution.ProviderInvocations != 2 {
		return errors.New("restricted adapter drops require two provider invocations")
	}
	return nil
}

// BlockingFindings returns the frozen native P0/P1 candidates that require a
// second, restricted production-floor adjudication.
func (outcome NativeOutcome) BlockingFindings() []NativeFinding {
	values := make([]NativeFinding, 0, nativeBlockingFindingCount(outcome.result.Findings))
	for _, finding := range outcome.result.Findings {
		if isBlockingNativePriority(finding.Priority) {
			values = append(values, finding)
		}
	}
	return values
}

// ApplyRestrictedAdjudication removes native P0/P1 candidates that do not meet
// the production floor. Removed candidate prose is retained only in the raw,
// frozen provider evidence and is not copied into the public result.
func (outcome NativeOutcome) ApplyRestrictedAdjudication(decisions []RestrictedFindingDecision) (NativeOutcome, error) {
	result := cloneNativeReviewResult(outcome.result)
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		return NativeOutcome{}, fmt.Errorf("native outcome is invalid before restricted adjudication: %s", strings.Join(problems, "; "))
	}
	if result.Execution.ProviderInvocations != 1 {
		return NativeOutcome{}, errors.New("restricted adjudication requires exactly one prior provider invocation")
	}
	blocking := outcome.BlockingFindings()
	if len(blocking) == 0 {
		return NativeOutcome{}, errors.New("restricted adjudication requires at least one P0/P1 candidate")
	}
	if len(decisions) != len(blocking) {
		return NativeOutcome{}, errors.New("restricted adjudication decision count does not match P0/P1 candidates")
	}
	retained := make(map[string]bool, len(decisions))
	for index, decision := range decisions {
		if decision.FindingID != blocking[index].ID {
			return NativeOutcome{}, fmt.Errorf("restricted adjudication decision %d does not match frozen candidate order", index)
		}
		if _, duplicate := retained[decision.FindingID]; duplicate {
			return NativeOutcome{}, fmt.Errorf("restricted adjudication decision %d duplicates a finding id", index)
		}
		retained[decision.FindingID] = decision.Retain
	}

	filter := func(findings []NativeFinding, recordDrops bool) []NativeFinding {
		filtered := make([]NativeFinding, 0, len(findings))
		for index, finding := range findings {
			keep, adjudicated := retained[finding.ID]
			if !adjudicated || keep {
				filtered = append(filtered, finding)
				continue
			}
			if recordDrops {
				result.Execution.AdapterDrops = append(result.Execution.AdapterDrops, AdapterDrop{
					Index: index, Reason: RestrictedAdjudicationDropReason,
				})
			}
		}
		return filtered
	}
	result.Findings = filter(result.Findings, true)
	result.NewFindings = filter(result.NewFindings, false)
	for index := range result.PreviousFindingResolutions {
		resolution := &result.PreviousFindingResolutions[index]
		if resolution.Status != ResolutionUnresolved || resolution.CurrentFinding == nil {
			continue
		}
		if keep, adjudicated := retained[resolution.CurrentFinding.ID]; adjudicated && !keep {
			resolution.Status = ResolutionDismissed
			resolution.Reason = RestrictedAdjudicationDropReason
			resolution.CurrentFinding = nil
		}
	}
	result.Execution.ProviderInvocations = 2
	classifyReleaseGate(&result)
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		return NativeOutcome{}, fmt.Errorf("restricted native outcome is invalid: %s", strings.Join(problems, "; "))
	}
	return NativeOutcome{result: result}, nil
}

// RestrictedAdjudicationFailure fails closed without publishing native
// candidate prose. Detailed diagnostics remain in the frozen evidence files.
func (outcome NativeOutcome) RestrictedAdjudicationFailure() (NativeOutcome, error) {
	result := cloneNativeReviewResult(outcome.result)
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		return NativeOutcome{}, fmt.Errorf("native outcome is invalid before restricted adjudication failure: %s", strings.Join(problems, "; "))
	}
	if result.Execution.ProviderInvocations != 1 || nativeBlockingFindingCount(result.Findings) == 0 {
		return NativeOutcome{}, errors.New("restricted adjudication failure requires a native P0/P1 candidate")
	}
	result.Findings = []NativeFinding{}
	result.PreviousFindingResolutions = []PreviousFindingResolution{}
	result.NewFindings = []NativeFinding{}
	result.Execution.ProviderInvocations = 2
	result.Adjudication = Adjudication{
		SemanticResult: ResultError, RolloutMode: "release_gate", CIAction: "hold_release",
		Reasons: []string{"restricted production-floor adjudication failed; inspect frozen evidence"},
	}
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		return NativeOutcome{}, fmt.Errorf("restricted adjudication error outcome is invalid: %s", strings.Join(problems, "; "))
	}
	return NativeOutcome{result: result}, nil
}

type nativeProviderFinding struct {
	Title        string             `json:"title"`
	Priority     int                `json:"priority"`
	CodeLocation NativeCodeLocation `json:"code_location"`
	Reason       string             `json:"reason"`
	Suggestion   string             `json:"suggestion"`
}

type fullProviderEnvelope struct {
	Findings []nativeProviderFinding `json:"findings"`
}

type incrementalProviderResolution struct {
	FindingID      string                 `json:"finding_id"`
	Status         string                 `json:"status"`
	Reason         string                 `json:"reason"`
	CurrentFinding *nativeProviderFinding `json:"current_finding"`
}

type incrementalProviderEnvelope struct {
	PreviousFindingResolutions []incrementalProviderResolution `json:"previous_finding_resolutions"`
	NewFindings                []nativeProviderFinding         `json:"new_findings"`
}

func decodeFullProviderFindings(raw []byte, allowedPaths []string) ([]NativeFinding, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}
	encodedFindings, exists := shape["findings"]
	if !exists || len(shape) != 1 || bytes.Equal(bytes.TrimSpace(encodedFindings), []byte("null")) {
		return nil, errors.New("root must contain only a non-null findings array")
	}
	envelope, err := DecodeStrict[fullProviderEnvelope](bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return identifyProviderFindings(envelope.Findings, allowedPaths)
}

func classifyIncrementalResponse(raw []byte, previous []NativeFinding, fullRequest, providerRequest ReviewRequest) ([]PreviousFindingResolution, []NativeFinding, []NativeFinding, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, nil, nil, err
	}
	for _, key := range []string{"previous_finding_resolutions", "new_findings"} {
		value, exists := shape[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, nil, nil, fmt.Errorf("%s must be a non-null array", key)
		}
	}
	if len(shape) != 2 {
		return nil, nil, nil, errors.New("incremental root must contain only previous_finding_resolutions and new_findings")
	}
	envelope, err := DecodeStrict[incrementalProviderEnvelope](bytes.NewReader(raw))
	if err != nil {
		return nil, nil, nil, err
	}
	previousByID := make(map[string]NativeFinding, len(previous))
	for index, finding := range previous {
		if !isBlockingNativePriority(finding.Priority) || !validDigest(finding.ID, findingIDPrefix) {
			return nil, nil, nil, fmt.Errorf("previous blocking finding %d is invalid", index)
		}
		if _, exists := previousByID[finding.ID]; exists {
			return nil, nil, nil, fmt.Errorf("previous blocking finding id %q is duplicated", finding.ID)
		}
		previousByID[finding.ID] = finding
	}
	seen := map[string]struct{}{}
	resolutions := make([]PreviousFindingResolution, 0, len(envelope.PreviousFindingResolutions))
	current := []NativeFinding{}
	for index, providerResolution := range envelope.PreviousFindingResolutions {
		prior, exists := previousByID[providerResolution.FindingID]
		if !exists {
			return nil, nil, nil, fmt.Errorf("resolution %d references unknown finding %q", index, providerResolution.FindingID)
		}
		if _, duplicate := seen[providerResolution.FindingID]; duplicate {
			return nil, nil, nil, fmt.Errorf("resolution for finding %q is duplicated", providerResolution.FindingID)
		}
		seen[providerResolution.FindingID] = struct{}{}
		reason := strings.TrimSpace(providerResolution.Reason)
		if reason == "" || len(reason) > 1000 || strings.ContainsAny(reason, "\r\n") {
			return nil, nil, nil, fmt.Errorf("resolution %d reason must be concise single-line content", index)
		}
		resolution := PreviousFindingResolution{FindingID: prior.ID, Status: providerResolution.Status, Reason: reason}
		switch providerResolution.Status {
		case ResolutionResolved:
			if providerResolution.CurrentFinding != nil {
				return nil, nil, nil, fmt.Errorf("resolved finding %q must have null current_finding", prior.ID)
			}
		case ResolutionUnresolved:
			if providerResolution.CurrentFinding == nil {
				return nil, nil, nil, fmt.Errorf("unresolved finding %q requires current_finding", prior.ID)
			}
			finding, identifyErr := identifyProviderFinding(*providerResolution.CurrentFinding, fullRequest.ChangedFiles)
			if identifyErr != nil {
				return nil, nil, nil, fmt.Errorf("unresolved finding %q: %w", prior.ID, identifyErr)
			}
			if !isBlockingNativePriority(finding.Priority) {
				return nil, nil, nil, fmt.Errorf("unresolved finding %q must remain P0/P1", prior.ID)
			}
			finding.ID = prior.ID
			resolution.CurrentFinding = &finding
			current = append(current, finding)
		default:
			return nil, nil, nil, fmt.Errorf("resolution %d has invalid status %q", index, providerResolution.Status)
		}
		resolutions = append(resolutions, resolution)
	}
	if len(seen) != len(previousByID) {
		missing := make([]string, 0, len(previousByID)-len(seen))
		for id := range previousByID {
			if _, exists := seen[id]; !exists {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		return nil, nil, nil, fmt.Errorf("missing resolution for previous finding(s): %s", strings.Join(missing, ", "))
	}
	newFindings, err := identifyProviderFindings(envelope.NewFindings, providerRequest.ChangedFiles)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new findings: %w", err)
	}
	currentIDs := map[string]struct{}{}
	for _, finding := range current {
		currentIDs[finding.ID] = struct{}{}
	}
	for _, finding := range newFindings {
		if _, duplicate := currentIDs[finding.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf("new finding id %q duplicates a previous finding", finding.ID)
		}
		currentIDs[finding.ID] = struct{}{}
		current = append(current, finding)
	}
	sort.Slice(resolutions, func(i, j int) bool { return resolutions[i].FindingID < resolutions[j].FindingID })
	sortNativeFindings(current)
	return resolutions, newFindings, current, nil
}

func identifyProviderFindings(values []nativeProviderFinding, allowedPaths []string) ([]NativeFinding, error) {
	findings := make([]NativeFinding, 0, len(values))
	seen := map[string]struct{}{}
	for index, value := range values {
		finding, err := identifyProviderFinding(value, allowedPaths)
		if err != nil {
			return nil, fmt.Errorf("finding %d: %w", index, err)
		}
		if _, duplicate := seen[finding.ID]; duplicate {
			return nil, fmt.Errorf("finding id %q is duplicated", finding.ID)
		}
		seen[finding.ID] = struct{}{}
		findings = append(findings, finding)
	}
	sortNativeFindings(findings)
	return findings, nil
}

func identifyProviderFinding(value nativeProviderFinding, allowedPaths []string) (NativeFinding, error) {
	finding, err := IdentifyNativeFinding(NativeFinding{
		Title: value.Title, Priority: value.Priority, CodeLocation: value.CodeLocation,
		Reason: value.Reason, Suggestion: value.Suggestion,
	})
	if err != nil {
		return NativeFinding{}, err
	}
	allowed := make(map[string]struct{}, len(allowedPaths))
	for _, path := range allowedPaths {
		allowed[path] = struct{}{}
	}
	if _, exists := allowed[finding.CodeLocation.Path]; !exists {
		return NativeFinding{}, fmt.Errorf("code_location path %q is outside the allowed changed files", finding.CodeLocation.Path)
	}
	return finding, nil
}

func sortNativeFindings(findings []NativeFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		if left.CodeLocation.Path != right.CodeLocation.Path {
			return left.CodeLocation.Path < right.CodeLocation.Path
		}
		if left.CodeLocation.StartLine != right.CodeLocation.StartLine {
			return left.CodeLocation.StartLine < right.CodeLocation.StartLine
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		return left.ID < right.ID
	})
}

func cloneNativeReviewResult(result NativeReviewResult) NativeReviewResult {
	result.ReviewIdentity = cloneReviewIdentity(result.ReviewIdentity)
	result.Request = cloneReviewRequest(result.Request)
	result.Findings = cloneNativeFindings(result.Findings)
	result.PreviousBlockingFindings = cloneNativeFindings(result.PreviousBlockingFindings)
	result.PreviousFindingResolutions = clonePreviousFindingResolutions(result.PreviousFindingResolutions)
	result.NewFindings = cloneNativeFindings(result.NewFindings)
	result.Execution.AdapterDrops = append([]AdapterDrop{}, result.Execution.AdapterDrops...)
	result.Adjudication.Reasons = append([]string(nil), result.Adjudication.Reasons...)
	return result
}

func cloneReviewIdentity(identity ReviewIdentity) ReviewIdentity {
	identity.ParentReviewKey = cloneStringPointer(identity.ParentReviewKey)
	identity.PreviousHead = cloneStringPointer(identity.PreviousHead)
	identity.DeltaChangedFiles = append([]string{}, identity.DeltaChangedFiles...)
	return identity
}

func cloneNativeFindings(values []NativeFinding) []NativeFinding {
	if values == nil {
		return []NativeFinding{}
	}
	return append([]NativeFinding{}, values...)
}

func clonePreviousFindingResolutions(values []PreviousFindingResolution) []PreviousFindingResolution {
	if values == nil {
		return []PreviousFindingResolution{}
	}
	result := make([]PreviousFindingResolution, len(values))
	for index, value := range values {
		result[index] = value
		if value.CurrentFinding != nil {
			copy := *value.CurrentFinding
			result[index].CurrentFinding = &copy
		}
	}
	return result
}

func cloneReviewRequest(request ReviewRequest) ReviewRequest {
	request.ChangedFiles = append([]string{}, request.ChangedFiles...)
	request.AffectedEntries = append([]string{}, request.AffectedEntries...)
	if request.Change != nil {
		change := *request.Change
		request.Change = &change
	}
	return request
}
