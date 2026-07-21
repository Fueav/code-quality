package quality

import (
	"strings"
	"testing"
)

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

func TestValidateResultRejectsTamperedVerdict(t *testing.T) {
	policy := validPolicy()
	result := Adjudicate(validRequest(), reviewWith(validBlockingFinding()), policy)
	result.Adjudication.SemanticResult = ResultPass
	if errors := ValidateResult(result, policy); !contains(errors, "result does not match deterministic adjudication") {
		t.Fatalf("errors = %#v, want tamper error", errors)
	}
}

func TestPolicyMustMatchExactV11RuleContract(t *testing.T) {
	policy := validPolicy()
	policy.Rules[0].ID = "NEW-001"
	if errors := ValidatePolicy(policy); !contains(errors, "does not match the V1.1 rule contract") {
		t.Fatalf("errors = %#v, want V1.1 rule error", errors)
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
		Execution: Execution{
			AgentCount:    1 + verifiers,
			VerifierCount: verifiers,
			InputTokens:   100,
			OutputTokens:  50,
			DurationMS:    1000,
			RetryCount:    0,
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
		PolicyVersion: "1.1.0",
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
