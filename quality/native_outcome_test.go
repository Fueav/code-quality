package quality

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyFrozenNativeReviewPreservesReleaseGateContract(t *testing.T) {
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
		{name: "empty findings", final: `{"findings":[]}`, want: ResultPass},
		{name: "unstructured native output", final: "- [P1] wrong value\n", want: ResultError},
		{name: "empty output", final: " \r\n", want: ResultError},
		{name: "failed process", final: `{"findings":[]}`, processErr: errors.New("exit 7"), want: ResultError},
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
				t.Fatalf("runtime outcome invented a finding: %#v", result)
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
	}, []byte(`{"findings":[{"priority":1,"title":"Wrong value","code_location":{"path":"app.go","start_line":2,"end_line":2},"reason":"The changed branch returns the wrong value.","suggestion":"Return the expected value."}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	if err := outcome.EncodeJSON(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded.String(), `"semantic_result": "BLOCK"`) {
		t.Fatalf("JSON outcome = %s", encoded.String())
	}
	for _, forbidden := range []string{`"affected_entries": null`, `"delta_changed_files": null`, `"previous_blocking_findings": null`, `"previous_finding_resolutions": null`, `"new_findings": null`} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("JSON outcome contains a null required array %s: %s", forbidden, encoded.String())
		}
	}
	markdown := outcome.Markdown()
	if !strings.Contains(markdown, "BLOCK") || !strings.Contains(markdown, "HOLD") {
		t.Fatalf("Markdown outcome = %s", markdown)
	}

	mutated := outcome.Result()
	mutated.Execution.ProviderInvocations = 2
	if outcome.Result().Execution.ProviderInvocations != 1 {
		t.Fatal("exported wire result mutated the validated outcome")
	}
}
