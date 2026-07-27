package quality

import (
	"fmt"
	"strings"
)

const (
	ResultPass         = "PASS"
	ResultManualReview = "MANUAL_REVIEW"
	ResultBlock        = "BLOCK"
	ResultIncomplete   = "INCOMPLETE"
)

func Adjudicate(request ReviewRequest, review ModelReview, policy PolicyManifest) ReviewResult {
	result := ReviewResult{
		SchemaVersion:         1,
		PolicyVersion:         policy.PolicyVersion,
		Request:               request,
		ActivatedRuleFamilies: nonNil(review.ActivatedRuleFamilies),
		InactiveRuleFamilies:  nonNilInactive(review.InactiveRuleFamilies),
		Execution:             review.Execution,
		UninspectedScope:      nonNil(review.UninspectedScope),
		MissingContext:        nonNil(review.MissingContext),
		InspectedContext:      nonNilInspected(review.InspectedContext),
		Findings:              []AdjudicatedFinding{},
		Adjudication: Adjudication{
			SemanticResult: ResultPass,
			RolloutMode:    "report_only",
			CIAction:       "publish_report",
			Reasons:        []string{},
		},
	}

	// Fatal validation only: policy, request, and report-level structure.
	validationErrors := append(ValidatePolicy(policy), ValidateRequest(request)...)
	validationErrors = append(validationErrors, ValidateModelReviewStructure(review, policy)...)
	if len(validationErrors) > 0 {
		result.Adjudication.SemanticResult = ResultIncomplete
		result.Adjudication.Reasons = uniqueSorted(validationErrors)
		return result
	}

	// Per-finding: drop malformed findings, keep the rest as MANUAL_REVIEW.
	var dropped []string
	for _, finding := range review.Findings {
		if problems := ValidateFinding(finding, policy); len(problems) > 0 {
			dropped = append(dropped, fmt.Sprintf("dropped finding %s: %s", finding.ID, strings.Join(problems, "; ")))
			continue
		}
		result.Findings = append(result.Findings, AdjudicatedFinding{
			Candidate:    finding,
			FinalVerdict: ResultManualReview,
		})
	}
	if len(dropped) > 0 {
		result.MissingContext = append(result.MissingContext, dropped...)
	}

	if len(result.Findings) > 0 {
		result.Adjudication.SemanticResult = ResultManualReview
		for _, finding := range result.Findings {
			result.Adjudication.Reasons = append(
				result.Adjudication.Reasons,
				fmt.Sprintf("%s requires manual review", finding.Candidate.ID),
			)
		}
	} else if review.Execution.RetryCount != nil && *review.Execution.RetryCount > 0 {
		result.Adjudication.Reasons = []string{"no material changed-code finding was reported after two review rounds"}
	} else {
		result.Adjudication.Reasons = []string{"no material changed-code finding was reported"}
	}
	return result
}

func IncompleteResult(request ReviewRequest, policy PolicyManifest, reasons ...string) ReviewResult {
	return IncompleteResultWithExecution(request, policy, Execution{}, reasons...)
}

func IncompleteResultWithExecution(request ReviewRequest, policy PolicyManifest, execution Execution, reasons ...string) ReviewResult {
	request.ChangedFiles = nonNil(request.ChangedFiles)
	request.AffectedEntries = nonNil(request.AffectedEntries)
	return ReviewResult{
		SchemaVersion:         1,
		PolicyVersion:         policy.PolicyVersion,
		Request:               request,
		ActivatedRuleFamilies: []string{},
		InactiveRuleFamilies:  []InactiveRuleFamily{},
		Findings:              []AdjudicatedFinding{},
		Execution:             execution,
		UninspectedScope:      []string{},
		MissingContext:        []string{},
		InspectedContext:      []InspectedContext{},
		Adjudication: Adjudication{
			SemanticResult: ResultIncomplete,
			RolloutMode:    "report_only",
			CIAction:       "publish_report",
			Reasons:        uniqueSorted(reasons),
		},
	}
}

func nonNilInspected(values []InspectedContext) []InspectedContext {
	if values == nil {
		return []InspectedContext{}
	}
	return values
}

func nonNilInactive(values []InactiveRuleFamily) []InactiveRuleFamily {
	if values == nil {
		return []InactiveRuleFamily{}
	}
	return values
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
