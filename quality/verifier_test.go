package quality

import "testing"

func TestPotentialBlockFindingsIsDisabled(t *testing.T) {
	block := validBlockingFinding()
	block.VerifierResult = "not_run"
	manual := block
	manual.ID = "F-002"
	manual.ProposedVerdict = ResultManualReview
	review := reviewWith(manual, block)
	candidates := PotentialBlockFindings(review)
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestValidateMainReviewRejectsForgedVerification(t *testing.T) {
	review := reviewWith(validBlockingFinding())
	if errors := ValidateMainReview(review, validPolicy()); !contains(errors, "verifier_result must be not_run") {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestVerifierReviewMustCoverCandidatesExactly(t *testing.T) {
	first := validBlockingFinding()
	first.VerifierResult = "not_run"
	second := first
	second.ID = "F-002"
	decisions := VerifierReview{Decisions: []VerifierDecision{
		{FindingID: "F-001", Result: "confirmed", VerificationSummary: "Reproduced the complete path.", Uncertainties: []string{}},
		{FindingID: "F-001", Result: "refuted", VerificationSummary: "Duplicate.", Uncertainties: []string{}},
		{FindingID: "F-999", Result: "confirmed", VerificationSummary: "Unknown.", Uncertainties: []string{}},
	}}
	errors := ValidateVerifierReview(decisions, []Finding{first, second})
	for _, expected := range []string{"duplicated", "not a potential BLOCK", "missing for F-002"} {
		if !contains(errors, expected) {
			t.Fatalf("errors = %#v, want %q", errors, expected)
		}
	}
}

func TestMergeVerifierReviewControlsBlocking(t *testing.T) {
	finding := validBlockingFinding()
	finding.VerifierResult = "not_run"
	main := reviewWith(finding)
	main.Execution = Execution{Host: "claude-code", SkillVersion: "0.1.1", AgentCount: 1}
	verifier := VerifierReview{Decisions: []VerifierDecision{{
		FindingID: "F-001", Result: "confirmed",
		VerificationSummary: "Confirmed the entry, trigger, and production scale.",
		Uncertainties:       []string{},
	}}}
	merged := MergeVerifierReview(main, verifier)
	merged.Execution = Execution{Host: "claude-code", SkillVersion: "0.1.1", AgentCount: 2, VerifierCount: 1}
	result := Adjudicate(validRequest(), merged, validPolicy())
	if result.Adjudication.SemanticResult != ResultManualReview {
		t.Fatalf("result = %#v", result)
	}
	if got := merged.Findings[0].VerificationPerformed[len(merged.Findings[0].VerificationPerformed)-1]; got != "Verifier: Confirmed the entry, trigger, and production scale." {
		t.Fatalf("verification evidence = %q", got)
	}
}
