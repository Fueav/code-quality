package quality

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewIdentityIsDeterministicAndBindsNormalizedInputs(t *testing.T) {
	input := v8IdentityInput(ReviewScopeFull)
	first, err := BuildReviewIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReviewIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReviewKey != second.ReviewKey || first.ContractDigest != second.ContractDigest {
		t.Fatalf("identity is not deterministic: %#v != %#v", first, second)
	}
	if !strings.HasPrefix(first.ReviewKey, "review-v1:sha256:") || !strings.HasPrefix(first.ContractDigest, "contract-v1:sha256:") {
		t.Fatalf("identity prefixes = %q / %q", first.ReviewKey, first.ContractDigest)
	}

	mutations := map[string]func(*ReviewIdentityInput){
		"repository": func(value *ReviewIdentityInput) { value.Request.Repository = "acme/other" },
		"base ref":   func(value *ReviewIdentityInput) { value.BaseRef = "release" },
		"head ref":   func(value *ReviewIdentityInput) { value.HeadRef = "other-feature" },
		"base tip":   func(value *ReviewIdentityInput) { value.BaseTipCommit = strings.Repeat("b", 40) },
		"merge base": func(value *ReviewIdentityInput) { value.MergeBase = strings.Repeat("c", 40) },
		"current head": func(value *ReviewIdentityInput) {
			value.CurrentHead = strings.Repeat("d", 40)
			value.Request.TargetCommit = value.CurrentHead
		},
		"changed files": func(value *ReviewIdentityInput) {
			value.Request.ChangedFiles = append(value.Request.ChangedFiles, "pkg/other.go")
		},
		"review goal":      func(value *ReviewIdentityInput) { value.ReviewGoal = "a different intent" },
		"tool version":     func(value *ReviewIdentityInput) { value.Contract.ToolVersion = "9.9.9" },
		"provider":         func(value *ReviewIdentityInput) { value.Contract.ProviderHost = "claude-code" },
		"model":            func(value *ReviewIdentityInput) { value.Contract.Model = "different-model" },
		"reasoning effort": func(value *ReviewIdentityInput) { value.Contract.ReasoningEffort = "high" },
		"profile":          func(value *ReviewIdentityInput) { value.Contract.ExecutionProfile = ExecutionProfilePersonal },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := input
			changed.Request = cloneReviewRequest(input.Request)
			mutate(&changed)
			identity, err := BuildReviewIdentity(changed)
			if err != nil {
				t.Fatal(err)
			}
			if identity.ReviewKey == first.ReviewKey {
				t.Fatalf("review key did not bind %s", name)
			}
		})
	}
}

func TestFindingIdentityIgnoresProviderOrderButBindsContent(t *testing.T) {
	first, err := IdentifyNativeFinding(v8Finding("pkg/service.go", 7, 1, "Second defect"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdentifyNativeFinding(v8Finding("pkg/fix.go", 3, 2, "First defect"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.ID, "finding-v1:sha256:") || first.ID == second.ID {
		t.Fatalf("finding ids = %q / %q", first.ID, second.ID)
	}

	request := v8IdentityInput(ReviewScopeFull).Request
	identity, err := BuildReviewIdentity(v8IdentityInput(ReviewScopeFull))
	if err != nil {
		t.Fatal(err)
	}
	encode := func(findings []NativeFinding) []byte {
		providerFindings := cloneNativeFindings(findings)
		for index := range providerFindings {
			providerFindings[index].ID = ""
		}
		raw, marshalErr := json.Marshal(map[string]any{"findings": providerFindings})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return raw
	}
	left, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: request, ProviderRequest: request, Identity: identity,
		ReviewGoal: inputReviewGoal(ReviewScopeFull),
	}, encode([]NativeFinding{first, second}), nil)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: request, ProviderRequest: request, Identity: identity,
		ReviewGoal: inputReviewGoal(ReviewScopeFull),
	}, encode([]NativeFinding{second, first}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustEncodeOutcome(t, left), mustEncodeOutcome(t, right)) {
		t.Fatalf("provider order changed the result:\n%s\n%s", mustEncodeOutcome(t, left), mustEncodeOutcome(t, right))
	}
}

func TestIncrementalOutcomeResolvesPreviousBlockerAndRetainsNewFinding(t *testing.T) {
	previous, err := IdentifyNativeFinding(v8Finding("pkg/service.go", 10, 1, "Old blocker"))
	if err != nil {
		t.Fatal(err)
	}
	newFinding := v8Finding("pkg/fix.go", 4, 2, "New advisory")
	input := v8IdentityInput(ReviewScopeIncremental)
	identity, err := BuildReviewIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	deltaRequest := input.Request
	deltaRequest.BaseCommit = *input.PreviousHead
	deltaRequest.ChangedFiles = append([]string(nil), input.DeltaChangedFiles...)
	raw := map[string]any{
		"previous_finding_resolutions": []any{map[string]any{
			"finding_id": previous.ID, "status": ResolutionResolved,
			"reason": "The causal path was removed.", "current_finding": nil,
		}},
		"new_findings": []NativeFinding{newFinding},
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: input.Request, ProviderRequest: deltaRequest, Identity: identity,
		PreviousBlockingFindings: []NativeFinding{previous},
		ReviewGoal:               input.ReviewGoal,
	}, encoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != ResultPass || len(result.Findings) != 1 || result.Findings[0].Priority != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.PreviousFindingResolutions) != 1 || result.PreviousFindingResolutions[0].Status != ResolutionResolved || result.PreviousFindingResolutions[0].CurrentFinding != nil {
		t.Fatalf("resolutions = %#v", result.PreviousFindingResolutions)
	}
	if len(result.NewFindings) != 1 || result.Findings[0].ID == "" || result.NewFindings[0].ID != result.Findings[0].ID {
		t.Fatalf("new/current findings = %#v / %#v", result.NewFindings, result.Findings)
	}
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("validation = %v", problems)
	}
}

func TestRestrictedAdjudicationDismissesUnresolvedPreviousBlocker(t *testing.T) {
	previous, err := IdentifyNativeFinding(v8Finding("pkg/service.go", 10, 1, "Rare old blocker"))
	if err != nil {
		t.Fatal(err)
	}
	input := v8IdentityInput(ReviewScopeIncremental)
	identity, err := BuildReviewIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	deltaRequest := input.Request
	deltaRequest.BaseCommit = *input.PreviousHead
	deltaRequest.ChangedFiles = append([]string(nil), input.DeltaChangedFiles...)
	raw, err := json.Marshal(map[string]any{
		"previous_finding_resolutions": []any{map[string]any{
			"finding_id": previous.ID, "status": ResolutionUnresolved,
			"reason": "The pattern still exists.",
			"current_finding": map[string]any{
				"title": previous.Title, "priority": previous.Priority, "code_location": previous.CodeLocation,
				"reason": previous.Reason, "suggestion": previous.Suggestion,
			},
		}},
		"new_findings": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := ClassifyFrozenNativeReview(NativeOutcomeOptions{
		Request: input.Request, ProviderRequest: deltaRequest, Identity: identity,
		PreviousBlockingFindings: []NativeFinding{previous}, ReviewGoal: input.ReviewGoal,
	}, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := outcome.ApplyRestrictedAdjudication([]RestrictedFindingDecision{{FindingID: previous.ID, Retain: false}})
	if err != nil {
		t.Fatal(err)
	}
	result := filtered.Result()
	if result.Adjudication.SemanticResult != ResultPass || len(result.Findings) != 0 ||
		len(result.PreviousFindingResolutions) != 1 || result.PreviousFindingResolutions[0].Status != ResolutionDismissed ||
		result.PreviousFindingResolutions[0].CurrentFinding != nil || result.Execution.ProviderInvocations != 2 {
		t.Fatalf("dismissed result = %#v", result)
	}
	if problems := ValidateNativeResult(result); len(problems) != 0 {
		t.Fatalf("dismissed validation = %#v", problems)
	}
}

func TestIncrementalOutcomeRejectsMissingDuplicateAndUnknownResolutions(t *testing.T) {
	previous, err := IdentifyNativeFinding(v8Finding("pkg/service.go", 10, 1, "Old blocker"))
	if err != nil {
		t.Fatal(err)
	}
	input := v8IdentityInput(ReviewScopeIncremental)
	identity, err := BuildReviewIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	deltaRequest := input.Request
	deltaRequest.BaseCommit = *input.PreviousHead
	deltaRequest.ChangedFiles = append([]string(nil), input.DeltaChangedFiles...)
	resolution := func(id string) map[string]any {
		return map[string]any{"finding_id": id, "status": ResolutionResolved, "reason": "fixed", "current_finding": nil}
	}
	cases := map[string][]any{
		"missing":   {},
		"duplicate": {resolution(previous.ID), resolution(previous.ID)},
		"unknown":   {resolution("finding-v1:sha256:" + strings.Repeat("f", 64))},
	}
	for name, resolutions := range cases {
		t.Run(name, func(t *testing.T) {
			raw, marshalErr := json.Marshal(map[string]any{"previous_finding_resolutions": resolutions, "new_findings": []any{}})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			outcome, classifyErr := ClassifyFrozenNativeReview(NativeOutcomeOptions{
				Request: input.Request, ProviderRequest: deltaRequest, Identity: identity,
				PreviousBlockingFindings: []NativeFinding{previous},
				ReviewGoal:               input.ReviewGoal,
			}, raw, nil)
			if classifyErr != nil {
				t.Fatal(classifyErr)
			}
			if result := outcome.Result(); result.Adjudication.SemanticResult != ResultError || result.Execution.ProviderInvocations != 1 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func v8IdentityInput(scope string) ReviewIdentityInput {
	previousKey := "review-v1:sha256:" + strings.Repeat("1", 64)
	previousHead := strings.Repeat("2", 40)
	input := ReviewIdentityInput{
		Contract: NativeReviewContract{
			ToolVersion: SkillVersion, ResultSchemaVersion: NativeResultSchemaVersion,
			ProviderOutputSchema:  "sha256:" + strings.Repeat("3", 64),
			PromptContractVersion: "3", EvaluationRubricVersion: EvaluationRubricVersion,
			ProviderHost: "codex", Model: "gpt-5.6-sol", ReasoningEffort: "max",
			ExecutionProfile: ExecutionProfileProductionCI,
		},
		Request: ReviewRequest{
			Repository: "acme/service", TargetBranch: "production",
			BaseCommit: strings.Repeat("4", 40), TargetCommit: strings.Repeat("5", 40),
			DiffSelectionReason: "explicit_ref_range", ChangedFiles: []string{"pkg/fix.go", "pkg/service.go"},
			AffectedEntries: []string{},
		},
		ReviewGoal: "protect ownership reconciliation", ReviewScope: scope,
		BaseRef: "production", HeadRef: "deploy", BaseTipCommit: strings.Repeat("6", 40),
		MergeBase: strings.Repeat("4", 40), CurrentHead: strings.Repeat("5", 40),
		DeltaChangedFiles: []string{},
	}
	if scope == ReviewScopeIncremental {
		input.ParentReviewKey = &previousKey
		input.PreviousHead = &previousHead
		input.DeltaChangedFiles = []string{"pkg/fix.go"}
	}
	return input
}

func inputReviewGoal(scope string) string {
	return v8IdentityInput(scope).ReviewGoal
}

func v8Finding(path string, line, priority int, title string) NativeFinding {
	return NativeFinding{
		Title: title, Priority: priority,
		CodeLocation: NativeCodeLocation{Path: path, StartLine: line, EndLine: line},
		Reason:       "A concrete production behavior is wrong.", Suggestion: "Apply the smallest contract-preserving fix.",
	}
}

func mustEncodeOutcome(t *testing.T, outcome NativeOutcome) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := outcome.EncodeJSON(&output); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
