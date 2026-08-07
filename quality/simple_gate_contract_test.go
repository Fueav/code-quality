package quality

import (
	"strings"
	"testing"
)

func TestStructuredNativeFindingBlocksRelease(t *testing.T) {
	request := nativeContractRequest()
	final := []byte(`{"findings":[{"priority":1,"title":"Duplicate charge on retry","code_location":{"path":"payment.go","start_line":12,"end_line":16},"reason":"The charge happens before the idempotency claim.","suggestion":"Claim the key before calling the payment provider."}]}`)
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: request, Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, final, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != ResultBlock || len(result.Findings) != 1 {
		t.Fatalf("result = %#v, want one blocking issue", result)
	}
	finding := result.Findings[0]
	if finding.Reason == "" || finding.Suggestion == "" || finding.CodeLocation.Path != "payment.go" {
		t.Fatalf("finding = %#v", finding)
	}
	markdown := outcome.Markdown()
	for _, want := range []string{"BLOCK", "HOLD", "Blocking issues: 1", "Duplicate charge on retry", "payment.go:12-16"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("summary is missing %q:\n%s", want, markdown)
		}
	}
}

func TestStructuredNativeEmptyFindingsPassReleaseGate(t *testing.T) {
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: nativeContractRequest(), Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, []byte(`{"findings":[]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcome.SemanticResult(); got != ResultPass {
		t.Fatalf("result = %s, want PASS", got)
	}
	for _, want := range []string{"PASS", "CONTINUE", "Blocking issues: 0"} {
		if !strings.Contains(outcome.Markdown(), want) {
			t.Errorf("summary is missing %q:\n%s", want, outcome.Markdown())
		}
	}
}

func TestInvalidStructuredNativeOutputHoldsRelease(t *testing.T) {
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: nativeContractRequest(), Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, []byte(`not-json`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcome.SemanticResult(); got != ResultError {
		t.Fatalf("result = %s, want ERROR", got)
	}
	if !strings.Contains(outcome.Markdown(), "HOLD") {
		t.Fatalf("error summary does not hold release:\n%s", outcome.Markdown())
	}
}

func nativeContractRequest() ReviewRequest {
	return ReviewRequest{
		Repository: "example/service", TargetBranch: "main",
		BaseCommit: strings.Repeat("1", 40), TargetCommit: strings.Repeat("2", 40),
		DiffSelectionReason: "test", ChangedFiles: []string{"payment.go"}, AffectedEntries: []string{},
	}
}
