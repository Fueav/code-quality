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

func TestAdjudicateAppliesCompleteBlockingFormula(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), validPolicy())
	if result.Adjudication.SemanticResult != ResultBlock {
		t.Fatalf("semantic result = %s, want BLOCK: %#v", result.Adjudication.SemanticResult, result.Adjudication.Reasons)
	}
	if len(result.Findings) != 1 || result.Findings[0].FinalVerdict != ResultBlock {
		t.Fatalf("findings = %#v, want one BLOCK", result.Findings)
	}
	if result.Adjudication.RolloutMode != "report_only" || result.Adjudication.CIAction != "publish_report" {
		t.Fatalf("rollout contract changed: %#v", result.Adjudication)
	}
}

func TestAdjudicateDowngradesIncompleteBlockCandidates(t *testing.T) {
	tests := map[string]func(*Finding){
		"severity":           func(f *Finding) { f.Severity = "S2" },
		"trigger confidence": func(f *Finding) { f.TriggerConfidence = "T2" },
		"evidence":           func(f *Finding) { f.EvidenceLevel = "E1" },
		"change attribution": func(f *Finding) { f.IntroducedOrWorsenedByChange = false },
		"verifier uncertain": func(f *Finding) { f.VerifierResult = "insufficient" },
		"verifier not run":   func(f *Finding) { f.VerifierResult = "not_run" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			finding := validBlockingFinding()
			mutate(&finding)
			result := Adjudicate(validRequest(), reviewWith(finding), validPolicy())
			if result.Adjudication.SemanticResult != ResultManualReview {
				t.Fatalf("semantic result = %s, want MANUAL_REVIEW", result.Adjudication.SemanticResult)
			}
		})
	}
}

func TestAdjudicateDropsRefutedLowConfidenceAndStyleFindings(t *testing.T) {
	refuted := validBlockingFinding()
	refuted.VerifierResult = "refuted"
	lowConfidence := validBlockingFinding()
	lowConfidence.ID = "F-002"
	lowConfidence.TriggerConfidence = "T1"
	lowConfidence.VerifierResult = "not_run"
	style := validBlockingFinding()
	style.ID = "F-003"
	style.FindingIsNotStylePreference = false
	style.VerifierResult = "not_run"
	result := Adjudicate(validRequest(), reviewWith(refuted, lowConfidence, style), validPolicy())
	if result.Adjudication.SemanticResult != ResultPass || len(result.Findings) != 0 {
		t.Fatalf("result = %#v, want PASS without findings", result)
	}
}

func TestAdjudicateInvalidInputIsIncomplete(t *testing.T) {
	request := validRequest()
	request.BaseCommit = ""
	review := reviewWith(validBlockingFinding())
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
	review := reviewWith(validBlockingFinding())
	review.InspectedContext = append(review.InspectedContext, review.InspectedContext[0])
	result := Adjudicate(validRequest(), review, validPolicy())
	if result.Adjudication.SemanticResult != ResultIncomplete || !contains(result.Adjudication.Reasons, "duplicate paths") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodeReviewResultRejectsMissingRequiredFields(t *testing.T) {
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), validPolicy())
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
			delete(findings[0].(map[string]any)["candidate"].(map[string]any), "causal_chain")
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
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), validPolicy())
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
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), validPolicy())
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	findings := document["findings"].([]any)
	findings[0].(map[string]any)["candidate"].(map[string]any)["uncertainties"] = nil
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReviewResult(bytes.NewReader(mutated)); err == nil || !strings.Contains(err.Error(), "cannot be null") {
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
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), policy)
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
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), validPolicy())
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

func TestPolicyMustMatchExactV11RuleContract(t *testing.T) {
	policy := validPolicy()
	policy.Rules[0].ID = "NEW-001"
	if errors := ValidatePolicy(policy); !contains(errors, "does not match the V1.1 rule contract") {
		t.Fatalf("errors = %#v, want V1.1 rule error", errors)
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
	first := validBlockingFinding()
	first.ID = "F-002"
	second := validBlockingFinding()
	second.ID = "F-001"
	second.RuleID = "COR-005"
	result := Adjudicate(validRequest(), reviewWith(first, second), validPolicy())
	markdown := RenderMarkdown(result)
	if strings.Index(markdown, "### F-001") > strings.Index(markdown, "### F-002") {
		t.Fatalf("findings are not sorted:\n%s", markdown)
	}
	if !strings.Contains(markdown, "**Result:** `BLOCK`") || !strings.Contains(markdown, "`internal/worker.go:42`") {
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

func validBlockingFinding() Finding {
	return Finding{
		ID:                           "F-001",
		RuleID:                       "DES-003",
		ProposedVerdict:              "BLOCK",
		Severity:                     "S3",
		TriggerConfidence:            "T3",
		EvidenceLevel:                "E2",
		IntroducedOrWorsenedByChange: true,
		FindingIsNotStylePreference:  true,
		CodeLocations:                []CodeLocation{{Path: "internal/worker.go", Line: 42}},
		AffectedCallPath:             []string{"cmd/service.main", "worker.RunWorker"},
		TriggerCondition:             "Every one of 500000 records invokes the remote API.",
		CausalChain:                  []string{"The changed loop iterates every record.", "Each iteration performs one remote request.", "The job exceeds its scheduling interval."},
		ProductionImpact:             "The worker accumulates permanently and exhausts remote quota.",
		VerificationPerformed:        []string{"Traced cmd/service.main to worker.RunWorker."},
		MinimalFix:                   "Restore bounded batch processing.",
		Uncertainties:                []string{},
		VerifierResult:               "confirmed",
	}
}

func reviewWith(findings ...Finding) ModelReview {
	verifiers := 0
	for _, finding := range findings {
		if finding.VerifierResult != "not_run" {
			verifiers = 1
			break
		}
	}
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
			Host:          "claude-code",
			SkillVersion:  "0.1.1",
			AgentCount:    1 + verifiers,
			VerifierCount: verifiers,
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
		PolicyVersion: "1.1.1",
		Rubric:        "policy/v1.1/rubric.md",
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
