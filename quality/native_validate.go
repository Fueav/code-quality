package quality

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func ValidateNativeResult(result NativeReviewResult) []string {
	var problems []string
	if result.SchemaVersion != NativeResultSchemaVersion {
		problems = append(problems, fmt.Sprintf("native result schema_version must be %d", NativeResultSchemaVersion))
	}
	if result.EvaluationRubricVersion != EvaluationRubricVersion || result.EvaluationRubricVersion != result.Contract.EvaluationRubricVersion {
		problems = append(problems, "evaluation_rubric_version must match the frozen contract")
	}
	problems = append(problems, ValidateRequest(result.Request)...)
	if result.Request.ChangedFiles == nil || result.Request.AffectedEntries == nil || result.DeltaChangedFiles == nil {
		problems = append(problems, "request and delta arrays must be non-null")
	}
	if !sort.StringsAreSorted(result.Request.ChangedFiles) {
		problems = append(problems, "request.changed_files must be sorted")
	}
	if len(result.ReviewGoal) > 4000 {
		problems = append(problems, "review_goal exceeds 4000 bytes")
	}
	expectedIdentity, identityErr := RecomputeReviewIdentity(result)
	if identityErr != nil {
		problems = append(problems, "review identity is invalid: "+identityErr.Error())
	} else {
		if result.ContractDigest != expectedIdentity.ContractDigest {
			problems = append(problems, "contract_digest does not match contract")
		}
		if result.ReviewKey != expectedIdentity.ReviewKey {
			problems = append(problems, "review_key does not match normalized inputs")
		}
	}
	if result.Findings == nil || result.PreviousBlockingFindings == nil || result.PreviousFindingResolutions == nil || result.NewFindings == nil {
		problems = append(problems, "native finding and resolution arrays must be non-null")
	}
	if result.Execution.AdapterDrops == nil {
		problems = append(problems, "execution.adapter_drops must be non-null")
	}
	problems = append(problems, validateNativeExecution(result)...)

	fullPaths := stringSet(result.Request.ChangedFiles)
	switch result.ReviewScope {
	case ReviewScopeFull:
		problems = append(problems, validateFullResultFindings(result, fullPaths)...)
	case ReviewScopeIncremental:
		problems = append(problems, validateIncrementalResultFindings(result, fullPaths)...)
	default:
		problems = append(problems, "review_scope must be FULL or INCREMENTAL")
	}

	if result.Adjudication.RolloutMode != "release_gate" {
		problems = append(problems, "adjudication rollout_mode must be release_gate")
	}
	if len(result.Adjudication.Reasons) == 0 || containsBlank(result.Adjudication.Reasons) {
		problems = append(problems, "adjudication reasons are required")
	}
	switch result.Adjudication.SemanticResult {
	case ResultPass:
		if nativeBlockingFindingCount(result.Findings) != 0 {
			problems = append(problems, "PASS result cannot contain P0/P1 findings")
		}
		if result.Adjudication.CIAction != "continue_release" {
			problems = append(problems, "PASS must continue_release")
		}
	case ResultBlock:
		if nativeBlockingFindingCount(result.Findings) == 0 {
			problems = append(problems, "BLOCK must contain at least one P0/P1 finding")
		}
		if result.Adjudication.CIAction != "hold_release" {
			problems = append(problems, "BLOCK must hold_release")
		}
	case ResultError:
		if len(result.Findings) != 0 || len(result.NewFindings) != 0 || len(result.PreviousFindingResolutions) != 0 {
			problems = append(problems, "ERROR result cannot contain findings or resolutions")
		}
		if result.Adjudication.CIAction != "hold_release" {
			problems = append(problems, "ERROR must hold_release")
		}
	default:
		problems = append(problems, "native adjudication semantic_result is invalid")
	}
	return uniqueSorted(problems)
}

func validateNativeExecution(result NativeReviewResult) []string {
	var problems []string
	execution := result.Execution
	contract := result.Contract
	if (execution.Host != "codex" && execution.Host != "claude-code") || execution.ReviewMode != "native_review" {
		problems = append(problems, "execution must use a supported native_review provider")
	}
	if execution.Host != contract.ProviderHost || execution.Model != contract.Model || execution.ReasoningEffort != contract.ReasoningEffort || execution.ExecutionProfile != contract.ExecutionProfile {
		problems = append(problems, "execution must match the frozen provider contract")
	}
	if execution.ExecutionProfile != ExecutionProfilePersonal && execution.ExecutionProfile != ExecutionProfileProductionCI {
		problems = append(problems, "execution.execution_profile must be personal or production-ci")
	}
	if execution.NativeAttempts != 1 {
		problems = append(problems, "execution.native_attempts must be 1")
	}
	if execution.RestrictedAttempts < 0 || execution.RestrictedAttempts > 2 {
		problems = append(problems, "execution.restricted_attempts must be between 0 and 2")
	}
	if execution.ProviderAttemptsTotal != execution.NativeAttempts+execution.RestrictedAttempts || execution.ProviderAttemptsTotal < 1 || execution.ProviderAttemptsTotal > 3 {
		problems = append(problems, "execution.provider_attempts_total must equal native plus restricted attempts and be at most 3")
	}
	if execution.ProviderInvocations != execution.ProviderAttemptsTotal {
		problems = append(problems, "execution.provider_invocations must equal provider_attempts_total")
	}
	if execution.RestrictedAttempts == 0 && execution.AdoptedRestrictedAttempt != nil {
		problems = append(problems, "execution.adopted_restricted_attempt must be null without a restricted attempt")
	}
	if execution.RestrictedAttempts > 0 && execution.AdoptedRestrictedAttempt == nil && result.Adjudication.SemanticResult != ResultError {
		problems = append(problems, "execution.adopted_restricted_attempt is required with restricted attempts")
	}
	if execution.AdoptedRestrictedAttempt != nil && (*execution.AdoptedRestrictedAttempt < 1 || *execution.AdoptedRestrictedAttempt > execution.RestrictedAttempts) {
		problems = append(problems, "execution.adopted_restricted_attempt is outside the attempt ledger")
	}
	if execution.Resumed {
		if execution.RestrictedAttempts == 0 {
			problems = append(problems, "execution.resumed requires a restricted attempt")
		}
		if execution.ResumedSessionDigest == nil || !validDigest(*execution.ResumedSessionDigest, "session-v1:sha256:") {
			problems = append(problems, "execution.resumed requires a valid resumed_session_digest")
		}
	} else if execution.ResumedSessionDigest != nil {
		problems = append(problems, "execution.resumed_session_digest requires resumed=true")
	}
	if execution.RestrictedAttempts == 2 && !execution.Resumed {
		problems = append(problems, "two restricted attempts require resumed=true")
	}
	if execution.RestrictedAttempts == 0 && len(execution.AdapterDrops) != 0 {
		problems = append(problems, "execution.adapter_drops require a restricted second invocation")
	}
	seenDropIndexes := map[int]struct{}{}
	originalFindingCount := len(result.Findings) + len(execution.AdapterDrops)
	for index, dropped := range execution.AdapterDrops {
		if dropped.Index < 0 || dropped.Index >= originalFindingCount || dropped.Reason != RestrictedAdjudicationDropReason {
			problems = append(problems, fmt.Sprintf("execution.adapter_drops[%d] is invalid", index))
		}
		if _, duplicate := seenDropIndexes[dropped.Index]; duplicate {
			problems = append(problems, fmt.Sprintf("execution.adapter_drops[%d] index is duplicated", index))
		}
		seenDropIndexes[dropped.Index] = struct{}{}
	}
	return problems
}

func validateFullResultFindings(result NativeReviewResult, fullPaths map[string]struct{}) []string {
	var problems []string
	if len(result.PreviousBlockingFindings) != 0 || len(result.PreviousFindingResolutions) != 0 || len(result.DeltaChangedFiles) != 0 {
		problems = append(problems, "FULL result cannot contain incremental findings or lineage")
	}
	problems = append(problems, validateFindingSet(result.Findings, fullPaths, true, "findings")...)
	problems = append(problems, validateFindingSet(result.NewFindings, fullPaths, true, "new_findings")...)
	if !reflect.DeepEqual(result.Findings, result.NewFindings) {
		problems = append(problems, "FULL new_findings must equal findings")
	}
	return problems
}

func validateIncrementalResultFindings(result NativeReviewResult, fullPaths map[string]struct{}) []string {
	var problems []string
	previousByID := map[string]NativeFinding{}
	for index, finding := range result.PreviousBlockingFindings {
		prefix := fmt.Sprintf("previous_blocking_findings[%d]", index)
		problems = append(problems, validateNativeFinding(finding, fullPaths, prefix)...)
		if !validDigest(finding.ID, findingIDPrefix) || !isBlockingNativePriority(finding.Priority) {
			problems = append(problems, prefix+" must have a stable P0/P1 identity")
		}
		if _, duplicate := previousByID[finding.ID]; duplicate {
			problems = append(problems, prefix+" id is duplicated")
		}
		previousByID[finding.ID] = finding
	}
	if result.Adjudication.SemanticResult == ResultError {
		return problems
	}
	deltaPaths := stringSet(result.DeltaChangedFiles)
	problems = append(problems, validateFindingSet(result.NewFindings, deltaPaths, true, "new_findings")...)
	seenResolutions := map[string]struct{}{}
	expectedCurrent := cloneNativeFindings(result.NewFindings)
	for index, resolution := range result.PreviousFindingResolutions {
		prefix := fmt.Sprintf("previous_finding_resolutions[%d]", index)
		if _, exists := previousByID[resolution.FindingID]; !exists {
			problems = append(problems, prefix+" references an unknown previous finding")
		}
		if _, duplicate := seenResolutions[resolution.FindingID]; duplicate {
			problems = append(problems, prefix+" is duplicated")
		}
		seenResolutions[resolution.FindingID] = struct{}{}
		if strings.TrimSpace(resolution.Reason) == "" || len(resolution.Reason) > 1000 || strings.ContainsAny(resolution.Reason, "\r\n") {
			problems = append(problems, prefix+" reason must be concise single-line content")
		}
		switch resolution.Status {
		case ResolutionResolved, ResolutionDismissed:
			if resolution.CurrentFinding != nil {
				problems = append(problems, prefix+" "+resolution.Status+" must have null current_finding")
			}
		case ResolutionUnresolved:
			if resolution.CurrentFinding == nil {
				problems = append(problems, prefix+" UNRESOLVED requires current_finding")
				continue
			}
			finding := *resolution.CurrentFinding
			problems = append(problems, validateNativeFinding(finding, fullPaths, prefix+".current_finding")...)
			if finding.ID != resolution.FindingID || !isBlockingNativePriority(finding.Priority) {
				problems = append(problems, prefix+" current_finding must retain the previous P0/P1 identity")
			}
			expectedCurrent = append(expectedCurrent, finding)
		default:
			problems = append(problems, prefix+" status must be RESOLVED, UNRESOLVED, or DISMISSED")
		}
	}
	if len(seenResolutions) != len(previousByID) {
		problems = append(problems, "every previous P0/P1 finding requires exactly one resolution")
	}
	sortNativeFindings(expectedCurrent)
	if !reflect.DeepEqual(expectedCurrent, result.Findings) {
		problems = append(problems, "current findings must equal unresolved previous findings plus new findings")
	}
	problems = append(problems, validateFindingSet(result.Findings, fullPaths, false, "findings")...)
	return problems
}

func validateFindingSet(findings []NativeFinding, allowedPaths map[string]struct{}, verifyContentIdentity bool, name string) []string {
	var problems []string
	seen := map[string]struct{}{}
	for index, finding := range findings {
		prefix := fmt.Sprintf("%s[%d]", name, index)
		problems = append(problems, validateNativeFinding(finding, allowedPaths, prefix)...)
		if !validDigest(finding.ID, findingIDPrefix) {
			problems = append(problems, prefix+" id is invalid")
		} else if _, duplicate := seen[finding.ID]; duplicate {
			problems = append(problems, prefix+" id is duplicated")
		} else {
			seen[finding.ID] = struct{}{}
		}
		if verifyContentIdentity {
			identified, err := IdentifyNativeFinding(finding)
			if err != nil || identified.ID != finding.ID {
				problems = append(problems, prefix+" id does not match normalized finding content")
			}
		}
	}
	return problems
}

func validateNativeFinding(finding NativeFinding, allowedPaths map[string]struct{}, prefix string) []string {
	var problems []string
	if strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Reason) == "" || strings.TrimSpace(finding.Suggestion) == "" {
		problems = append(problems, prefix+" title, reason, and suggestion are required")
	}
	if len(finding.Title) > 160 || len(finding.Reason) > 1000 || len(finding.Suggestion) > 1000 ||
		strings.ContainsAny(finding.Title+finding.Reason+finding.Suggestion, "\r\n") {
		problems = append(problems, prefix+" text must be concise single-line content")
	}
	if finding.Priority < 0 || finding.Priority > 3 {
		problems = append(problems, prefix+" priority is outside 0-3")
	}
	location := finding.CodeLocation
	if !isCleanRelativePath(location.Path) || location.StartLine < 1 || location.EndLine < location.StartLine {
		problems = append(problems, prefix+" code_location is invalid")
	} else if allowedPaths != nil {
		if _, exists := allowedPaths[location.Path]; !exists {
			problems = append(problems, prefix+" code_location is not in an allowed changed file")
		}
	}
	return problems
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
