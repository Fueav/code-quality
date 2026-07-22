package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

const maxReviewResultJSONBytes = 20 << 20

var requiredReviewResultFields = []string{
	"schema_version", "policy_version", "request", "activated_rule_families", "inactive_rule_families",
	"findings", "execution", "uninspected_scope", "missing_context", "inspected_context", "adjudication",
}

func DecodeReviewResult(reader io.Reader) (ReviewResult, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxReviewResultJSONBytes+1))
	if err != nil {
		return ReviewResult{}, err
	}
	if len(raw) > maxReviewResultJSONBytes {
		return ReviewResult{}, errors.New("review result exceeds 20 MiB")
	}
	if err := validateReviewResultShape(raw); err != nil {
		return ReviewResult{}, err
	}
	return DecodeStrict[ReviewResult](bytes.NewReader(raw))
}

func validateReviewResultShape(raw []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	if err := requireFields(document, "review result", requiredReviewResultFields...); err != nil {
		return err
	}
	for _, field := range []string{"activated_rule_families", "inactive_rule_families", "findings", "uninspected_scope", "missing_context", "inspected_context"} {
		if !isJSONArray(document[field]) {
			return fmt.Errorf("review result.%s must be an array", field)
		}
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(document["request"], &request); err != nil {
		return fmt.Errorf("decode review result.request: %w", err)
	}
	if err := requireFields(request, "review result.request", "repository", "target_branch", "base_commit", "target_commit", "diff_selection_reason", "changed_files", "affected_entries"); err != nil {
		return err
	}
	for _, field := range []string{"changed_files", "affected_entries"} {
		if !isJSONArray(request[field]) {
			return fmt.Errorf("review result.request.%s must be an array", field)
		}
	}
	var execution map[string]json.RawMessage
	if err := json.Unmarshal(document["execution"], &execution); err != nil {
		return fmt.Errorf("decode review result.execution: %w", err)
	}
	if err := requireFields(execution, "review result.execution", "host", "skill_version", "agent_count", "verifier_count"); err != nil {
		return err
	}
	if err := requireNullableFields(execution, "review result.execution", "input_tokens", "output_tokens", "duration_ms", "retry_count"); err != nil {
		return err
	}
	var adjudication map[string]json.RawMessage
	if err := json.Unmarshal(document["adjudication"], &adjudication); err != nil {
		return fmt.Errorf("decode review result.adjudication: %w", err)
	}
	if err := requireFields(adjudication, "review result.adjudication", "semantic_result", "rollout_mode", "ci_action", "reasons"); err != nil {
		return err
	}
	if !isJSONArray(adjudication["reasons"]) {
		return errors.New("review result.adjudication.reasons must be an array")
	}
	var inspected []map[string]json.RawMessage
	if err := json.Unmarshal(document["inspected_context"], &inspected); err != nil {
		return fmt.Errorf("decode review result.inspected_context: %w", err)
	}
	for index, item := range inspected {
		if err := requireFields(item, fmt.Sprintf("review result.inspected_context[%d]", index), "path", "purpose"); err != nil {
			return err
		}
	}
	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(document["findings"], &findings); err != nil {
		return fmt.Errorf("decode review result.findings: %w", err)
	}
	for index, item := range findings {
		prefix := fmt.Sprintf("review result.findings[%d]", index)
		if err := requireFields(item, prefix, "candidate", "final_verdict"); err != nil {
			return err
		}
		var candidate map[string]json.RawMessage
		if err := json.Unmarshal(item["candidate"], &candidate); err != nil {
			return fmt.Errorf("decode %s.candidate: %w", prefix, err)
		}
		if err := requireFields(candidate, prefix+".candidate", requiredFindingFields...); err != nil {
			return err
		}
		for _, field := range []string{"code_locations", "affected_call_path", "causal_chain", "verification_performed", "uncertainties"} {
			if !isJSONArray(candidate[field]) {
				return fmt.Errorf("%s.candidate.%s must be an array", prefix, field)
			}
		}
	}
	return nil
}

func requireNullableFields(document map[string]json.RawMessage, prefix string, fields ...string) error {
	if document == nil {
		return errors.New(prefix + " must be an object")
	}
	for _, field := range fields {
		if _, exists := document[field]; !exists {
			return fmt.Errorf("%s.%s is required", prefix, field)
		}
	}
	return nil
}

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
		errors = append(errors, validateExecution(result.Execution, policy, true)...)
		if len(result.ActivatedRuleFamilies) != 0 || len(result.InactiveRuleFamilies) != 0 || len(result.Findings) != 0 || len(result.UninspectedScope) != 0 || len(result.MissingContext) != 0 || len(result.InspectedContext) != 0 {
			errors = append(errors, "INCOMPLETE result must not contain partial review output")
		}
		return uniqueSorted(errors)
	}

	modelReview := ModelReview{
		ActivatedRuleFamilies: result.ActivatedRuleFamilies,
		InactiveRuleFamilies:  result.InactiveRuleFamilies,
		Findings:              make([]Finding, 0, len(result.Findings)),
		UninspectedScope:      result.UninspectedScope,
		MissingContext:        result.MissingContext,
		InspectedContext:      result.InspectedContext,
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
