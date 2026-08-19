package quality

import (
	"strings"
	"testing"
)

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
	result.Adjudication.CIAction = "continue_release"
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("PASS result with a P1 finding was accepted")
	}
}

func TestValidateNativeResultAllowsPassWithAdvisories(t *testing.T) {
	result := validNativeResult()
	result.Findings[0].Priority = 2
	identified, err := IdentifyNativeFinding(result.Findings[0])
	if err != nil {
		t.Fatal(err)
	}
	result.Findings[0] = identified
	result.NewFindings[0] = identified
	result.Adjudication.SemanticResult = ResultPass
	result.Adjudication.CIAction = "continue_release"
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("PASS with a P2 advisory problems = %#v", problems)
	}
}

func TestValidateNativeResultAllowsOneNativeAndTwoRestrictedAttempts(t *testing.T) {
	result := validNativeResult()
	result.Execution.ProviderInvocations = 2
	result.Execution.RestrictedAttempts = 1
	result.Execution.ProviderAttemptsTotal = 2
	first := 1
	result.Execution.AdoptedRestrictedAttempt = &first
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("native plus restricted result problems = %#v", problems)
	}
	result.Execution.AdoptedRestrictedAttempt = nil
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("restricted result without an adopted attempt was accepted")
	}
	result.Execution.AdoptedRestrictedAttempt = &first
	result.Execution.ProviderInvocations = 3
	result.Execution.RestrictedAttempts = 2
	result.Execution.ProviderAttemptsTotal = 3
	second := 2
	result.Execution.AdoptedRestrictedAttempt = &second
	result.Execution.Resumed = true
	digest := "session-v1:sha256:" + strings.Repeat("a", 64)
	result.Execution.ResumedSessionDigest = &digest
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("resumed native result problems = %#v", problems)
	}
	result.Execution.Resumed = false
	result.Execution.ResumedSessionDigest = nil
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("two restricted attempts without resume were accepted")
	}
	result.Execution.Resumed = true
	result.Execution.ResumedSessionDigest = &digest
	result.Execution.ProviderInvocations = 4
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("native result with a fourth provider invocation was accepted")
	}
}

func TestValidateNativeResultRejectsUntrustedAdapterDropMetadata(t *testing.T) {
	result := validNativeResult()
	result.Findings = []NativeFinding{}
	result.NewFindings = []NativeFinding{}
	result.Execution.ProviderInvocations = 2
	result.Execution.RestrictedAttempts = 1
	result.Execution.ProviderAttemptsTotal = 2
	first := 1
	result.Execution.AdoptedRestrictedAttempt = &first
	result.Execution.AdapterDrops = []AdapterDrop{{Index: 0, Reason: "model supplied reason"}}
	result.Adjudication.SemanticResult = ResultPass
	result.Adjudication.CIAction = "continue_release"
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("untrusted adapter drop metadata was accepted")
	}
}

func TestValidateNativeResultSupportsBothNativeProvidersOnly(t *testing.T) {
	result := validNativeResult()
	result.Execution.Host = "claude-code"
	result.Contract.ProviderHost = "claude-code"
	rebuildNativeResultIdentity(t, &result)
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
	rebuildNativeResultIdentity(t, &result)
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

func TestValidateNativeResultRejectsBlockWithAdvisoriesOnly(t *testing.T) {
	result := validNativeResult()
	result.Findings[0].Priority = 3
	if problems := ValidateNativeResult(result); len(problems) == 0 {
		t.Fatal("BLOCK with only a P3 advisory was accepted")
	}
}

func TestValidateNativeResultRejectsTamperedIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*NativeReviewResult){
		"review key": func(result *NativeReviewResult) { result.ReviewKey = "review-v1:sha256:" + strings.Repeat("f", 64) },
		"contract digest": func(result *NativeReviewResult) {
			result.ContractDigest = "contract-v1:sha256:" + strings.Repeat("f", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := validNativeResult()
			mutate(&result)
			if problems := ValidateNativeResult(result); len(problems) == 0 {
				t.Fatal("tampered identity was accepted")
			}
		})
	}
}

func validNativeResult() NativeReviewResult {
	request := ReviewRequest{
		Repository: "example/repo", TargetBranch: "main", BaseCommit: "base", TargetCommit: "target",
		DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	contract := NativeReviewContract{
		ToolVersion: SkillVersion, ResultSchemaVersion: NativeResultSchemaVersion,
		ProviderOutputSchema:  SHA256Digest([]byte("test-provider-schema")),
		PromptContractVersion: "3", EvaluationRubricVersion: EvaluationRubricVersion,
		EvaluationRubricDigest: SHA256Digest([]byte("test-rubric")),
		RestrictedPolicyDigest: SHA256Digest([]byte("test-policy")),
		RestrictedSchemaDigest: SHA256Digest([]byte("test-restricted-schema")),
		ProviderHost:           "codex", Model: "gpt-5.6-sol", ReasoningEffort: "high",
		ExecutionProfile: ExecutionProfilePersonal,
	}
	identity, err := BuildReviewIdentity(ReviewIdentityInput{
		Contract: contract, Request: request, ReviewGoal: "review", ReviewScope: ReviewScopeFull,
		BaseRef: "main", HeadRef: "feature", BaseTipCommit: "base", MergeBase: "base", CurrentHead: "target",
		DeltaChangedFiles: []string{},
	})
	if err != nil {
		panic(err)
	}
	finding, err := IdentifyNativeFinding(NativeFinding{
		Title: "wrong value", Priority: 1, Reason: "The new branch returns the wrong value.", Suggestion: "Return the expected value.",
		CodeLocation: NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
	})
	if err != nil {
		panic(err)
	}
	return NativeReviewResult{
		ReviewIdentity: identity,
		SchemaVersion:  NativeResultSchemaVersion, EvaluationRubricVersion: EvaluationRubricVersion,
		Request:    request,
		ReviewGoal: "review",
		Findings:   []NativeFinding{finding}, PreviousBlockingFindings: []NativeFinding{},
		PreviousFindingResolutions: []PreviousFindingResolution{}, NewFindings: []NativeFinding{finding},
		Execution: NativeExecution{
			Host: "codex", ReviewMode: "native_review", ExecutionProfile: ExecutionProfilePersonal,
			Model: "gpt-5.6-sol", ReasoningEffort: "high", ProviderInvocations: 1,
			NativeAttempts: 1, RestrictedAttempts: 0, ProviderAttemptsTotal: 1,
			AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultBlock, RolloutMode: "release_gate", CIAction: "hold_release",
			Reasons: []string{"one finding"},
		},
	}
}

func rebuildNativeResultIdentity(t *testing.T, result *NativeReviewResult) {
	t.Helper()
	identity, err := RecomputeReviewIdentity(*result)
	if err != nil {
		t.Fatal(err)
	}
	result.ReviewIdentity = identity
}
