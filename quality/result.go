package quality

import (
	"encoding/json"
	"fmt"
	"reflect"
)

func ValidateResult(result ReviewResult, policy PolicyManifest) []string {
	var errors []string
	if result.SchemaVersion != 1 {
		errors = append(errors, "result schema_version must be 1")
	}
	if result.PolicyVersion != policy.PolicyVersion {
		errors = append(errors, "result policy_version does not match the embedded policy")
	}
	if result.Adjudication.RolloutMode != "report_only" {
		errors = append(errors, "rollout_mode must be report_only")
	}
	if result.Adjudication.CIAction != "publish_report" {
		errors = append(errors, "ci_action must be publish_report")
	}
	if result.Adjudication.SemanticResult == ResultIncomplete {
		if len(result.Adjudication.Reasons) == 0 {
			errors = append(errors, "INCOMPLETE result requires reasons")
		}
		return uniqueSorted(errors)
	}

	modelReview := ModelReview{
		ActivatedRuleFamilies: result.ActivatedRuleFamilies,
		InactiveRuleFamilies:  result.InactiveRuleFamilies,
		Findings:              make([]Finding, 0, len(result.Findings)),
		UninspectedScope:      result.UninspectedScope,
		MissingContext:        result.MissingContext,
		Execution:             result.Execution,
	}
	for _, finding := range result.Findings {
		modelReview.Findings = append(modelReview.Findings, finding.Candidate)
	}
	expected := Adjudicate(result.Request, modelReview, policy)
	if expected.Adjudication.SemanticResult == ResultIncomplete {
		errors = append(errors, expected.Adjudication.Reasons...)
		return uniqueSorted(errors)
	}
	if !equalSemanticResult(result, expected) {
		errors = append(errors, "result does not match deterministic adjudication")
	}
	return uniqueSorted(errors)
}

func equalSemanticResult(actual, expected ReviewResult) bool {
	actualJSON, actualErr := canonicalSemanticJSON(actual)
	expectedJSON, expectedErr := canonicalSemanticJSON(expected)
	return actualErr == nil && expectedErr == nil && reflect.DeepEqual(actualJSON, expectedJSON)
}

func canonicalSemanticJSON(result ReviewResult) (any, error) {
	value := struct {
		Findings     []AdjudicatedFinding `json:"findings"`
		Adjudication Adjudication         `json:"adjudication"`
	}{
		Findings:     result.Findings,
		Adjudication: result.Adjudication,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal semantic result: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode semantic result: %w", err)
	}
	return decoded, nil
}
