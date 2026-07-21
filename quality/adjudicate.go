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
		Findings:              []AdjudicatedFinding{},
		Adjudication: Adjudication{
			SemanticResult: ResultPass,
			RolloutMode:    "report_only",
			CIAction:       "publish_report",
			Reasons:        []string{},
		},
	}

	validationErrors := append(ValidatePolicy(policy), ValidateRequest(request)...)
	validationErrors = append(validationErrors, ValidateModelReview(review, policy)...)
	if len(validationErrors) > 0 {
		result.Adjudication.SemanticResult = ResultIncomplete
		result.Adjudication.Reasons = uniqueSorted(validationErrors)
		return result
	}

	for _, finding := range review.Findings {
		verdict := adjudicateFinding(finding)
		if verdict == "" {
			continue
		}
		result.Findings = append(result.Findings, AdjudicatedFinding{
			Candidate:    finding,
			FinalVerdict: verdict,
		})
	}

	for _, finding := range result.Findings {
		if finding.FinalVerdict == ResultBlock {
			result.Adjudication.SemanticResult = ResultBlock
			result.Adjudication.Reasons = append(
				result.Adjudication.Reasons,
				fmt.Sprintf("%s satisfies the V1.1 blocking formula", finding.Candidate.ID),
			)
		}
	}
	if result.Adjudication.SemanticResult != ResultBlock {
		for _, finding := range result.Findings {
			if finding.FinalVerdict == ResultManualReview {
				result.Adjudication.SemanticResult = ResultManualReview
				result.Adjudication.Reasons = append(
					result.Adjudication.Reasons,
					fmt.Sprintf("%s requires manual review", finding.Candidate.ID),
				)
			}
		}
	}
	if result.Adjudication.SemanticResult == ResultPass {
		result.Adjudication.Reasons = []string{"no finding satisfies the V1.1 reporting threshold"}
	}
	return result
}

func IncompleteResult(request ReviewRequest, policy PolicyManifest, reasons ...string) ReviewResult {
	request.ChangedFiles = nonNil(request.ChangedFiles)
	request.AffectedEntries = nonNil(request.AffectedEntries)
	return ReviewResult{
		SchemaVersion:         1,
		PolicyVersion:         policy.PolicyVersion,
		Request:               request,
		ActivatedRuleFamilies: []string{},
		InactiveRuleFamilies:  []InactiveRuleFamily{},
		Findings:              []AdjudicatedFinding{},
		Execution:             Execution{},
		UninspectedScope:      []string{},
		MissingContext:        []string{},
		Adjudication: Adjudication{
			SemanticResult: ResultIncomplete,
			RolloutMode:    "report_only",
			CIAction:       "publish_report",
			Reasons:        uniqueSorted(reasons),
		},
	}
}

func adjudicateFinding(finding Finding) string {
	if finding.VerifierResult == "refuted" {
		return ""
	}
	if finding.VerifierResult == "insufficient" {
		return ResultManualReview
	}
	if satisfiesBlockFormula(finding) {
		if finding.VerifierResult == "confirmed" {
			return ResultBlock
		}
		return ResultManualReview
	}
	if !finding.FindingIsNotStylePreference {
		return ""
	}
	if finding.Severity == "S1" || finding.TriggerConfidence == "T1" {
		return ""
	}
	return ResultManualReview
}

func satisfiesBlockFormula(finding Finding) bool {
	return finding.Severity == "S3" &&
		finding.TriggerConfidence == "T3" &&
		(finding.EvidenceLevel == "E2" || finding.EvidenceLevel == "E3") &&
		finding.IntroducedOrWorsenedByChange &&
		finding.FindingIsNotStylePreference &&
		strings.TrimSpace(finding.TriggerCondition) != "" &&
		len(finding.CausalChain) > 0
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
