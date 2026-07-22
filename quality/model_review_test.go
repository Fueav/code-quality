package quality

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeModelReviewRequiresSchemaFields(t *testing.T) {
	for _, raw := range []string{
		`{"activated_rule_families":["D1","D2","D3","D4"],"inactive_rule_families":[]}`,
		`{"activated_rule_families":["D1"],"inactive_rule_families":[],"findings":null,"uninspected_scope":[],"missing_context":[]}`,
		`{"activated_rule_families":["D1"],"inactive_rule_families":[],"findings":[],"uninspected_scope":[],"missing_context":[],"unknown":true}`,
	} {
		if _, err := DecodeModelReview(strings.NewReader(raw)); err == nil {
			t.Fatalf("schema-invalid model review was accepted: %s", raw)
		}
	}
}

func TestDecodeModelReviewRejectsOversizedInput(t *testing.T) {
	oversized := strings.Repeat(" ", maxModelReviewJSONBytes+1)
	if _, err := DecodeModelReview(strings.NewReader(oversized)); err == nil || !strings.Contains(err.Error(), "exceeds 10 MiB") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeModelReviewRequiresFindingAndLocationFields(t *testing.T) {
	review := validDecodeModelReview()
	raw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(document["findings"], &findings); err != nil {
		t.Fatal(err)
	}
	delete(findings[0], "trigger_condition")
	document["findings"], _ = json.Marshal(findings)
	raw, _ = json.Marshal(document)
	if _, err := DecodeModelReview(strings.NewReader(string(raw))); err == nil || !strings.Contains(err.Error(), "trigger_condition is required") {
		t.Fatalf("error = %v", err)
	}

	review = validDecodeModelReview()
	raw, _ = json.Marshal(review)
	_ = json.Unmarshal(raw, &document)
	_ = json.Unmarshal(document["findings"], &findings)
	var locations []map[string]json.RawMessage
	_ = json.Unmarshal(findings[0]["code_locations"], &locations)
	delete(locations[0], "line")
	findings[0]["code_locations"], _ = json.Marshal(locations)
	document["findings"], _ = json.Marshal(findings)
	raw, _ = json.Marshal(document)
	if _, err := DecodeModelReview(strings.NewReader(string(raw))); err == nil || !strings.Contains(err.Error(), "line is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeModelReviewRequiresInspectedContextFields(t *testing.T) {
	review := validDecodeModelReview()
	raw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var inspected []map[string]json.RawMessage
	if err := json.Unmarshal(document["inspected_context"], &inspected); err != nil {
		t.Fatal(err)
	}
	delete(inspected[0], "purpose")
	document["inspected_context"], _ = json.Marshal(inspected)
	raw, _ = json.Marshal(document)
	if _, err := DecodeModelReview(strings.NewReader(string(raw))); err == nil || !strings.Contains(err.Error(), "purpose is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeModelReviewAcceptsRequiredEmptyArrays(t *testing.T) {
	raw := `{"activated_rule_families":["D1","D2","D3","D4"],"inactive_rule_families":[],"findings":[],"uninspected_scope":[],"missing_context":[],"inspected_context":[]}`
	review, err := DecodeModelReview(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if review.Findings == nil || review.UninspectedScope == nil || review.MissingContext == nil || review.InspectedContext == nil {
		t.Fatalf("required arrays were not preserved: %#v", review)
	}
}

func validDecodeModelReview() ModelReview {
	return ModelReview{
		ActivatedRuleFamilies: []string{"D1"},
		InactiveRuleFamilies: []InactiveRuleFamily{
			{ID: "D2", Reason: "not affected"}, {ID: "D3", Reason: "not affected"}, {ID: "D4", Reason: "not affected"},
		},
		Findings: []Finding{{
			ID: "F-001", RuleID: "DES-003", ProposedVerdict: "MANUAL_REVIEW",
			Severity: "S2", TriggerConfidence: "T2", EvidenceLevel: "E2",
			IntroducedOrWorsenedByChange: true, FindingIsNotStylePreference: true,
			CodeLocations: []CodeLocation{{Path: "app.go", Line: 1}}, AffectedCallPath: []string{"entry"},
			TriggerCondition: "condition", CausalChain: []string{"cause"}, ProductionImpact: "impact",
			VerificationPerformed: []string{"trace"}, MinimalFix: "fix", Uncertainties: []string{}, VerifierResult: "not_run",
		}},
		UninspectedScope: []string{}, MissingContext: []string{},
		InspectedContext: []InspectedContext{{Path: "app.go", Purpose: "Trace the changed entry."}},
	}
}
