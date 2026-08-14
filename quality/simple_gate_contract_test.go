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

func TestStructuredNativeP0FindingBlocksRelease(t *testing.T) {
	final := []byte(`{"findings":[{"priority":0,"title":"Irreversible production data loss","code_location":{"path":"payment.go","start_line":12,"end_line":16},"reason":"The migration drops the only copy of committed payment records on the normal upgrade path.","suggestion":"Preserve the records and migrate them before removing the source storage."}]}`)
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: nativeContractRequest(), Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, final, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.SemanticResult() != ResultBlock || outcome.Summary().BlockingIssues != 1 || outcome.Summary().AdvisoryIssues != 0 {
		t.Fatalf("summary = %#v", outcome.Summary())
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

func TestStructuredNativeAdvisoriesContinueRelease(t *testing.T) {
	final := []byte(`{"findings":[{"priority":2,"title":"Conditional cleanup backlog","code_location":{"path":"payment.go","start_line":20,"end_line":20},"reason":"The bounded cleanup can lag only under sustained maximum throughput.","suggestion":"Track the backlog and raise cleanup capacity."},{"priority":3,"title":"Low-impact diagnostic gap","code_location":{"path":"payment.go","start_line":30,"end_line":30},"reason":"The new fallback omits a useful diagnostic field without changing behavior.","suggestion":"Include the diagnostic field in a follow-up."}]}`)
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: nativeContractRequest(), Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, final, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != ResultPass || len(result.Findings) != 2 {
		t.Fatalf("result = %#v, want PASS with two advisories", result)
	}
	summary := outcome.Summary()
	if summary.SchemaVersion != 3 || summary.Release != "CONTINUE" || summary.BlockingIssues != 0 || summary.AdvisoryIssues != 2 || len(summary.Issues) != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	for _, want := range []string{"PASS", "CONTINUE", "Blocking issues: 0", "Advisory issues: 2", "## Advisories"} {
		if !strings.Contains(outcome.Markdown(), want) {
			t.Errorf("summary is missing %q:\n%s", want, outcome.Markdown())
		}
	}
}

func TestStructuredNativeMixedPrioritiesBlockOnlyOnP0P1(t *testing.T) {
	final := []byte(`{"findings":[{"priority":1,"title":"Duplicate charge on retry","code_location":{"path":"payment.go","start_line":12,"end_line":16},"reason":"The charge happens before the idempotency claim.","suggestion":"Claim the key before calling the payment provider."},{"priority":2,"title":"Conditional cleanup backlog","code_location":{"path":"payment.go","start_line":20,"end_line":20},"reason":"The bounded cleanup can lag only under sustained maximum throughput.","suggestion":"Track the backlog and raise cleanup capacity."}]}`)
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: nativeContractRequest(), Host: "codex", ExecutionProfile: ExecutionProfileProductionCI,
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}, final, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary := outcome.Summary()
	if outcome.SemanticResult() != ResultBlock || summary.BlockingIssues != 1 || summary.AdvisoryIssues != 1 || len(summary.Issues) != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	markdown := outcome.Markdown()
	for _, want := range []string{"## Issues to fix before release", "## Advisories", "Duplicate charge on retry", "Conditional cleanup backlog"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("summary is missing %q:\n%s", want, markdown)
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
