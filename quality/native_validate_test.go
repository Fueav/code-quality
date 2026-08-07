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

func TestValidateNativeResultEnforcesSingleProviderInvocation(t *testing.T) {
	result := validNativeResult()
	result.Execution.ProviderInvocations = 2
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("native result with two provider invocations was accepted")
	}
}

func TestValidateNativeResultSupportsBothNativeProvidersOnly(t *testing.T) {
	result := validNativeResult()
	result.Execution.Host = "claude-code"
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("Claude Code native result problems = %#v", problems)
	}
	result.Execution.Host = "custom-orchestrator"
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("unknown native review provider was accepted")
	}
}

func TestValidateNativeResultAllowsNoGoal(t *testing.T) {
	result := validNativeResult()
	result.ReviewGoal = ""
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("optional goal was treated as required: %#v", problems)
	}
}

func TestValidateNativeResultRejectsBlockWithoutIssues(t *testing.T) {
	result := validNativeResult()
	result.Findings = []NativeFinding{}
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("BLOCK without issues was accepted")
	}
}

func validNativeResult() NativeReviewResult {
	return NativeReviewResult{
		SchemaVersion: NativeResultSchemaVersion, EvaluationRubricVersion: EvaluationRubricVersion,
		Request: ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: "base", TargetCommit: "target",
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
		ReviewGoal: "review",
		Findings: []NativeFinding{{
			Title: "wrong value", Priority: 1, Reason: "The new branch returns the wrong value.", Suggestion: "Return the expected value.",
			CodeLocation: NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
		}},
		Execution: NativeExecution{
			Host: "codex", ReviewMode: "native_review", ExecutionProfile: ExecutionProfilePersonal,
			ReasoningEffort: "high", ProviderInvocations: 1,
			AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultBlock, RolloutMode: "release_gate", CIAction: "hold_release",
			Reasons: []string{"one finding"},
		},
	}
}
