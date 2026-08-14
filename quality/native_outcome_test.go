package quality

import (
	"encoding/json"
	"errors"
	"strconv"
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

func TestRestrictedAdjudicationSilentlyRemovesNonFloorBlocker(t *testing.T) {
	outcome := validBlockingNativeOutcome(t, "Extremely rare topology corner")
	blocking := outcome.BlockingFindings()
	filtered, err := outcome.ApplyRestrictedAdjudication([]RestrictedFindingDecision{{FindingID: blocking[0].ID, Retain: false}})
	if err != nil {
		t.Fatal(err)
	}
	result := filtered.Result()
	if result.Adjudication.SemanticResult != ResultPass || result.Execution.ProviderInvocations != 2 ||
		len(result.Findings) != 0 || len(result.NewFindings) != 0 || len(result.Execution.AdapterDrops) != 1 {
		t.Fatalf("filtered result = %#v", result)
	}
	if result.Execution.AdapterDrops[0].Reason != RestrictedAdjudicationDropReason {
		t.Fatalf("adapter drop = %#v", result.Execution.AdapterDrops)
	}
	var publicJSON strings.Builder
	if err := filtered.EncodeJSON(&publicJSON); err != nil {
		t.Fatal(err)
	}
	summaryJSON, err := json.Marshal(filtered.Summary())
	if err != nil {
		t.Fatal(err)
	}
	for surface, contents := range map[string]string{
		"result": publicJSON.String(), "markdown": filtered.Markdown(), "summary": string(summaryJSON),
	} {
		if strings.Contains(contents, "Extremely rare topology corner") || strings.Contains(contents, "only under an unsupported topology") {
			t.Fatalf("%s leaked a rejected corner case: %s", surface, contents)
		}
	}
}

func TestRestrictedAdjudicationRetainsOnlyProvenBlocker(t *testing.T) {
	outcome := validBlockingNativeOutcome(t, "Deterministic money loss")
	if err := outcome.ValidatePublication(); err == nil {
		t.Fatal("intermediate native BLOCK was publishable before restricted adjudication")
	}
	blocking := outcome.BlockingFindings()
	filtered, err := outcome.ApplyRestrictedAdjudication([]RestrictedFindingDecision{{FindingID: blocking[0].ID, Retain: true}})
	if err != nil {
		t.Fatal(err)
	}
	result := filtered.Result()
	if result.Adjudication.SemanticResult != ResultBlock || result.Execution.ProviderInvocations != 2 ||
		len(result.Findings) != 1 || len(result.Execution.AdapterDrops) != 0 {
		t.Fatalf("retained result = %#v", result)
	}
	if err := filtered.ValidatePublication(); err != nil {
		t.Fatalf("restricted BLOCK is not publishable: %v", err)
	}
}

func TestRestrictedAdjudicationFailureHoldsWithoutCandidateProse(t *testing.T) {
	outcome := validBlockingNativeOutcome(t, "Do not expose this candidate")
	failed, err := outcome.RestrictedAdjudicationFailure()
	if err != nil {
		t.Fatal(err)
	}
	result := failed.Result()
	if result.Adjudication.SemanticResult != ResultError || result.Adjudication.CIAction != "hold_release" ||
		result.Execution.ProviderInvocations != 2 || len(result.Findings) != 0 {
		t.Fatalf("failure result = %#v", result)
	}
	if strings.Contains(failed.Markdown(), "Do not expose this candidate") {
		t.Fatalf("failure markdown leaked candidate: %s", failed.Markdown())
	}
}

func validBlockingNativeOutcome(t *testing.T, title string) NativeOutcome {
	t.Helper()
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: "base", TargetCommit: "target",
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
		Model: "gpt-5.6-sol", ReasoningEffort: "max",
	}, []byte(`{"findings":[{"priority":1,"title":`+strconv.Quote(title)+`,"code_location":{"path":"app.go","start_line":2,"end_line":2},"reason":"The path fails only under an unsupported topology.","suggestion":"Change the path."}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
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
