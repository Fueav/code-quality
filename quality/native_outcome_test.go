package quality

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyFrozenNativeReviewPreservesThinThreeStateContract(t *testing.T) {
	request := ReviewRequest{
		Repository: "example/repo", TargetBranch: "main",
		BaseCommit: "base", TargetCommit: "target", DiffSelectionReason: "test",
		ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	tests := []struct {
		name       string
		final      string
		processErr error
		want       string
	}{
		{name: "exact sentinel", final: "No findings.\n", want: ResultPass},
		{name: "nonempty native output", final: "- [P1] wrong value\n", want: ResultManualReview},
		{name: "empty output", final: " \r\n", want: ResultIncomplete},
		{name: "failed process", final: "No findings.\n", processErr: errors.New("exit 7"), want: ResultIncomplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
				Request: request, ReviewGoal: "protect behavior", Model: "gpt-5.6-sol", ReasoningEffort: "max",
			}, []byte(test.final), test.processErr)
			if err != nil {
				t.Fatal(err)
			}
			result := outcome.Result()
			if result.Adjudication.SemanticResult != test.want || result.Execution.ProviderInvocations != 1 {
				t.Fatalf("result = %#v", result)
			}
			if len(result.Findings) != 0 || len(result.Execution.AdapterDrops) != 0 {
				t.Fatalf("runtime outcome invented structured interpretation: %#v", result)
			}
			if problems := ValidateNativeResult(result); len(problems) != 0 {
				t.Fatalf("runtime outcome is invalid: %#v", problems)
			}
		})
	}
}

func TestNativeOutcomeKeepsOneValidatedFactForJSONAndMarkdown(t *testing.T) {
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: "base", TargetCommit: "target",
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
		Model: "gpt-5.6-sol", ReasoningEffort: "max",
	}, []byte("native finding text\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if err := outcome.EncodeJSON(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), `"semantic_result": "MANUAL_REVIEW"`) {
		t.Fatalf("JSON outcome = %s", encoded.String())
	}
	markdown := outcome.Markdown()
	if !strings.Contains(markdown, "`MANUAL_REVIEW`") || !strings.Contains(markdown, "native-review.txt") {
		t.Fatalf("Markdown outcome = %s", markdown)
	}

	mutated := outcome.Result()
	mutated.Execution.ProviderInvocations = 2
	if outcome.Result().Execution.ProviderInvocations != 1 {
		t.Fatal("exported wire result mutated the validated outcome")
	}
}
