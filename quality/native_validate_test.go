package quality

import "testing"

func TestValidateNativeResultEnforcesChangedFileAndSemanticContract(t *testing.T) {
	result := validNativeResult()
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("valid result problems = %#v", problems)
	}
	result.Findings[0].CodeLocation.Path = "unchanged.go"
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("finding outside changed files was accepted")
	}
	result = validNativeResult()
	result.Adjudication.SemanticResult = ResultPass
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("PASS result with findings was accepted")
	}
}

func TestValidateNativeResultEnforcesVerifierCallCount(t *testing.T) {
	result := validNativeResult()
	result.Execution.ModelCalls = 1
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("completed verifier with one model call was accepted")
	}
}

func validNativeResult() NativeReviewResult {
	return NativeReviewResult{
		SchemaVersion: NativeResultSchemaVersion, EvaluationRubricVersion: EvaluationRubricVersion,
		Request: ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: "base", TargetCommit: "target",
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
		ReviewGoal: "review", Directions: []ReviewDirection{{ID: "correctness", Prompt: "Trace behavior."}},
		Findings: []NativeFinding{{
			Title: "wrong value", Body: "The new branch returns the wrong value.", Priority: 1, ConfidenceScore: 0.9,
			CodeLocation: NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
		}},
		Execution: NativeExecution{
			Host: "codex", ReviewMode: "native_review", ReasoningEffort: "high", ModelCalls: 2,
			VerifierStatus: VerifierComplete, AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultManualReview, RolloutMode: "report_only", CIAction: "publish_report",
			Reasons: []string{"one finding"},
		},
	}
}
