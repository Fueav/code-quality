package quality

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestIncompleteResultNormalizesRequestArrays(t *testing.T) {
	result := IncompleteResult(ReviewRequest{}, validPolicy(), "intake failed")
	if result.Request.ChangedFiles == nil || result.Request.AffectedEntries == nil {
		t.Fatalf("request arrays are null: %#v", result.Request)
	}
}

func TestAdjudicateMapsValidFindingToManualReview(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validFinding()), validPolicy())
	if result.Adjudication.SemanticResult != ResultManualReview {
		t.Fatalf("semantic result = %s, want MANUAL_REVIEW: %#v", result.Adjudication.SemanticResult, result.Adjudication.Reasons)
	}
	if len(result.Findings) != 1 || result.Findings[0].FinalVerdict != ResultManualReview {
		t.Fatalf("findings = %#v, want one MANUAL_REVIEW", result.Findings)
	}
	if result.Adjudication.RolloutMode != "report_only" || result.Adjudication.CIAction != "publish_report" {
		t.Fatalf("rollout contract changed: %#v", result.Adjudication)
	}
}

func TestAdjudicateDropsOnlyMalformedFindings(t *testing.T) {
	valid := validFinding()
	malformed := validFinding()
	malformed.ID = "F-002"
	malformed.ProductionImpact = ""
	result := Adjudicate(validRequest(), reviewWith(valid, malformed), validPolicy())
	if result.Adjudication.SemanticResult != ResultManualReview || len(result.Findings) != 1 {
		t.Fatalf("result = %#v, want one MANUAL_REVIEW", result)
	}
	if len(result.MissingContext) != 1 || !strings.Contains(result.MissingContext[0], "dropped finding F-002: production_impact is required") {
		t.Fatalf("missing context = %#v, want dropped finding reason", result.MissingContext)
	}
}

func TestAdjudicateAllMalformedFindingsPasses(t *testing.T) {
	malformed := validFinding()
	malformed.ProductionImpact = ""
	result := Adjudicate(validRequest(), reviewWith(malformed), validPolicy())
	if result.Adjudication.SemanticResult != ResultPass || len(result.Findings) != 0 {
		t.Fatalf("result = %#v, want PASS without findings", result)
	}
}

func TestAdjudicateInvalidInputIsIncomplete(t *testing.T) {
	request := validRequest()
	request.BaseCommit = ""
	review := reviewWith(validFinding())
	review.Execution.AgentCount = 3
	result := Adjudicate(request, review, validPolicy())
	if result.Adjudication.SemanticResult != ResultIncomplete {
		t.Fatalf("semantic result = %s, want INCOMPLETE", result.Adjudication.SemanticResult)
	}
	joined := strings.Join(result.Adjudication.Reasons, "\n")
	for _, expected := range []string{"base_commit is required", "agent_count"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("reasons do not contain %q: %s", expected, joined)
		}
	}
}

func TestAdjudicateRejectsDuplicateInspectedContext(t *testing.T) {
	review := reviewWith(validFinding())
	review.InspectedContext = append(review.InspectedContext, review.InspectedContext[0])
	result := Adjudicate(validRequest(), review, validPolicy())
	if result.Adjudication.SemanticResult != ResultIncomplete || !contains(result.Adjudication.Reasons, "duplicate paths") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodeReviewResultRejectsMissingRequiredFields(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validFinding()), validPolicy())
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(map[string]any){
		"top level": func(document map[string]any) {
			delete(document, "inspected_context")
		},
		"request": func(document map[string]any) {
			delete(document["request"].(map[string]any), "changed_files")
		},
		"execution nullable field": func(document map[string]any) {
			delete(document["execution"].(map[string]any), "duration_ms")
		},
		"adjudication": func(document map[string]any) {
			delete(document["adjudication"].(map[string]any), "ci_action")
		},
		"finding candidate": func(document map[string]any) {
			findings := document["findings"].([]any)
			delete(findings[0].(map[string]any)["candidate"].(map[string]any), "minimal_fix")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			mutate(document)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeReviewResult(bytes.NewReader(mutated)); err == nil || !strings.Contains(err.Error(), "is required") {
				t.Fatalf("error = %v, want required-field error", err)
			}
		})
	}
}

func TestDecodeReviewResultRejectsNullRequiredArrays(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validFinding()), validPolicy())
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["missing_context"] = nil
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReviewResult(bytes.NewReader(mutated)); err == nil || (!strings.Contains(err.Error(), "must be an array") && !strings.Contains(err.Error(), "cannot be null")) {
		t.Fatalf("error = %v, want null-array error", err)
	}
}

func TestDecodeReviewResultRejectsNullFindingArrays(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validFinding()), validPolicy())
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	findings := document["findings"].([]any)
	findings[0].(map[string]any)["candidate"].(map[string]any)["code_locations"] = nil
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReviewResult(bytes.NewReader(mutated)); err == nil || (!strings.Contains(err.Error(), "must be an array") && !strings.Contains(err.Error(), "cannot be null")) {
		t.Fatalf("error = %v, want null-array error", err)
	}
}

func TestDecodeReviewResultRejectsNullOrWrongTypeExecutionIdentity(t *testing.T) {
	result := IncompleteResult(validRequest(), validPolicy(), "trusted input was invalid")
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		field string
		value any
	}{
		{name: "null host", field: "host", value: nil},
		{name: "null skill version", field: "skill_version", value: nil},
		{name: "null agent count", field: "agent_count", value: nil},
		{name: "null verifier count", field: "verifier_count", value: nil},
		{name: "wrong host type", field: "host", value: 1},
		{name: "wrong skill version type", field: "skill_version", value: false},
		{name: "wrong agent count type", field: "agent_count", value: "0"},
		{name: "wrong verifier count type", field: "verifier_count", value: 0.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			document["execution"].(map[string]any)[test.field] = test.value
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeReviewResult(bytes.NewReader(mutated)); err == nil {
				t.Fatal("DecodeReviewResult accepted an invalid execution identity field")
			}
		})
	}
}

func TestDecodeReviewResultAllowsNullableMetricsAndEmptyIncompleteExecution(t *testing.T) {
	result := IncompleteResult(validRequest(), validPolicy(), "trusted input was invalid")
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReviewResult(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if errors := ValidateResult(decoded, validPolicy()); len(errors) != 0 {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestValidateResultRejectsTamperedVerdict(t *testing.T) {
	policy := validPolicy()
	result := Adjudicate(validRequest(), reviewWith(validFinding()), policy)
	result.Adjudication.SemanticResult = ResultPass
	if errors := ValidateResult(result, policy); !contains(errors, "result does not match deterministic adjudication") {
		t.Fatalf("errors = %#v, want tamper error", errors)
	}
}

func TestValidateResultRejectsMalformedIncompleteExecution(t *testing.T) {
	negative := -1
	result := IncompleteResultWithExecution(validRequest(), validPolicy(), Execution{
		Host:          "claude-code",
		SkillVersion:  "untrusted",
		AgentCount:    3,
		VerifierCount: 2,
		DurationMS:    &negative,
	}, "model output was malformed")
	errors := ValidateResult(result, validPolicy())
	for _, expected := range []string{"skill_version", "agent_count", "verifier_count", "metrics"} {
		if !contains(errors, expected) {
			t.Fatalf("errors = %#v, want %q", errors, expected)
		}
	}
}

func TestValidateResultRejectsIncompleteWithPartialReviewOutput(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validFinding()), validPolicy())
	result.Adjudication.SemanticResult = ResultIncomplete
	result.Adjudication.Reasons = []string{"forged incomplete status"}
	if errors := ValidateResult(result, validPolicy()); !contains(errors, "must not contain partial review output") {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestValidateResultAllowsIncompleteBeforeExecutionStarts(t *testing.T) {
	result := IncompleteResult(validRequest(), validPolicy(), "trusted input was invalid")
	if errors := ValidateResult(result, validPolicy()); len(errors) != 0 {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestPolicyMustMatchExactV12RuleContract(t *testing.T) {
	policy := validPolicy()
	policy.Rules[0].ID = "NEW-001"
	if errors := ValidatePolicy(policy); !contains(errors, "does not match the V1.2 rule contract") {
		t.Fatalf("errors = %#v, want V1.2 rule error", errors)
	}
}

func TestPolicyRequiresTwoAgentRuntimeLimit(t *testing.T) {
	policy := validPolicy()
	policy.AgentLimit = 3
	if errors := ValidatePolicy(policy); !contains(errors, "agent_limit must be 2") {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestRenderMarkdownIsDeterministic(t *testing.T) {
	first := validFinding()
	first.ID = "F-002"
	second := validFinding()
	second.ID = "F-001"
	second.RuleID = "COR-005"
	result := Adjudicate(validRequest(), reviewWith(first, second), validPolicy())
	markdown := RenderMarkdown(result)
	if strings.Index(markdown, "### F-001") > strings.Index(markdown, "### F-002") {
		t.Fatalf("findings are not sorted:\n%s", markdown)
	}
	if !strings.Contains(markdown, "**Result:** `MANUAL_REVIEW`") || !strings.Contains(markdown, "`internal/worker.go:42`") {
		t.Fatalf("markdown is incomplete:\n%s", markdown)
	}
}

func validRequest() ReviewRequest {
	return ReviewRequest{
		Repository:          "example/service",
		TargetBranch:        "main",
		BaseCommit:          strings.Repeat("a", 40),
		TargetCommit:        strings.Repeat("b", 40),
		DiffSelectionReason: "pull_request",
		ChangedFiles:        []string{"internal/worker.go"},
		AffectedEntries:     []string{"RunWorker"},
	}
}

func validFinding() Finding {
	return Finding{
		ID:               "F-001",
		RuleID:           "DES-003",
		CodeLocations:    []CodeLocation{{Path: "internal/worker.go", Line: 42}},
		ProductionImpact: "The worker accumulates permanently and exhausts remote quota.",
		MinimalFix:       "Restore bounded batch processing.",
	}
}

func reviewWith(findings ...Finding) ModelReview {
	return ModelReview{
		ActivatedRuleFamilies: []string{"D1"},
		InactiveRuleFamilies: []InactiveRuleFamily{
			{ID: "D2", Reason: "No business result change."},
			{ID: "D3", Reason: "No resource lifecycle change."},
			{ID: "D4", Reason: "No security or rollout change."},
		},
		Findings:         findings,
		UninspectedScope: []string{},
		MissingContext:   []string{},
		InspectedContext: []InspectedContext{{Path: "internal/worker.go", Purpose: "Trace the production call path."}},
		Execution: Execution{
			Host:         "claude-code",
			SkillVersion: SkillVersion,
			AgentCount:   1,
		},
	}
}

func validPolicy() PolicyManifest {
	ids := []string{
		"DES-001", "DES-002", "DES-003", "DES-004", "DES-005",
		"COR-001", "COR-002", "COR-003", "COR-004", "COR-005",
		"REL-001", "REL-002", "REL-003", "REL-004", "REL-005",
		"SEC-001", "SEC-002", "SEC-003", "CHG-001", "CHG-002",
	}
	rules := make([]PolicyRule, 0, len(ids))
	for _, id := range ids {
		dimension := requiredRules[id]
		rules = append(rules, PolicyRule{ID: id, Dimension: dimension, Status: "report_only"})
	}
	return PolicyManifest{
		SchemaVersion: 1,
		PolicyVersion: "1.2.0",
		Rubric:        "policy/v1.2/rubric.md",
		AgentLimit:    2,
		Rules:         rules,
	}
}

func contains(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}
