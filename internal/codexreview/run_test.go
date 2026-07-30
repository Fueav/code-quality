package codexreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestSelectDirectionsIsDeterministicAndBounded(t *testing.T) {
	request := quality.ReviewRequest{ChangedFiles: []string{"internal/auth/token.go", "internal/worker.go"}}
	diff := []byte("+if token == \"\" { return errUnauthorized }\n+case <-time.After(timeout):\n")

	first := selectDirections(request, diff)
	second := selectDirections(request, diff)
	if len(first) < 1 || len(first) > 3 {
		t.Fatalf("direction count = %d", len(first))
	}
	if directionsKey(first) != directionsKey(second) {
		t.Fatalf("directions are not deterministic: %#v != %#v", first, second)
	}
	joined := directionsKey(first)
	for _, expected := range []string{"security-boundaries", "reliability-lifecycle"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("directions %q do not include %q", joined, expected)
		}
	}
}

func TestNativeReviewInvocationUsesOneCustomTarget(t *testing.T) {
	prepared, request := nativeFixture(t)
	options := Options{
		Prepared: prepared, Request: request, Goal: "protect settlement correctness",
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}
	directions := []quality.ReviewDirection{{ID: "data-business-correctness", Prompt: "Trace value and state transitions."}}

	invocation := buildReviewInvocation(options, directions)
	args := strings.Join(invocation.Args, " ")
	if !strings.Contains(args, "exec --sandbox read-only --ignore-user-config --ignore-rules --ephemeral review") {
		t.Fatalf("native review args = %q", args)
	}
	for _, forbidden := range []string{"--base", "--commit", "--uncommitted"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("custom target is combined with %s: %q", forbidden, args)
		}
	}
	for _, required := range []string{request.BaseCommit, request.TargetCommit, "protect settlement correctness", "hints only", "outside these hints"} {
		if !strings.Contains(invocation.Stdin, required) {
			t.Fatalf("prompt is missing %q:\n%s", required, invocation.Stdin)
		}
	}
}

func TestZeroFindingsSkipsVerifier(t *testing.T) {
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{`{"findings":[]}`}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultPass || result.Execution.ModelCalls != 1 || result.Execution.VerifierStatus != quality.VerifierNotNeeded {
		t.Fatalf("result = %#v", result)
	}
	if len(executor.invocations) != 1 {
		t.Fatalf("model calls = %d", len(executor.invocations))
	}
}

func TestVerifierCanOnlyFilterExistingCandidates(t *testing.T) {
	prepared, request := nativeFixture(t)
	path := filepath.Join(prepared.RepositoryDir, "app.go")
	executor := &scriptedExecutor{outputs: []string{
		`{"findings":[{"title":"timeout is ignored","body":"The new call can wait forever.","priority":1,"confidence_score":0.91,"code_location":{"absolute_file_path":"` + path + `","line_range":{"start":2,"end":2}}}]}`,
		`{"decisions":[{"index":0,"keep":false,"reason":"The target already enforces the timeout."}]}`,
	}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultPass || len(result.Findings) != 0 || result.Execution.ModelCalls != 2 || result.Execution.VerifierStatus != quality.VerifierComplete {
		t.Fatalf("result = %#v", result)
	}
	if len(executor.invocations) != 2 {
		t.Fatalf("model calls = %d", len(executor.invocations))
	}
	for _, required := range []string{"only the numbered candidates", "Do not add", "Do not rewrite"} {
		if !strings.Contains(executor.invocations[1].Stdin, required) {
			t.Fatalf("verifier prompt is missing %q:\n%s", required, executor.invocations[1].Stdin)
		}
	}
}

func TestVerifierFailureKeepsNativeCandidates(t *testing.T) {
	prepared, request := nativeFixture(t)
	path := filepath.Join(prepared.RepositoryDir, "app.go")
	executor := &scriptedExecutor{
		outputs: []string{`{"findings":[{"title":"wrong result","body":"The new branch returns the opposite value.","priority":1,"confidence_score":0.95,"code_location":{"absolute_file_path":"` + path + `","line_range":{"start":2,"end":2}}}]}`},
		errors:  []error{nil, errors.New("verifier unavailable")},
	}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Adjudication.SemanticResult != quality.ResultManualReview || result.Execution.VerifierStatus != quality.VerifierFailedOpen {
		t.Fatalf("result = %#v", result)
	}
}

func TestMalformedVerifierDecisionFailsOpen(t *testing.T) {
	prepared, request := nativeFixture(t)
	path := filepath.Join(prepared.RepositoryDir, "app.go")
	executor := &scriptedExecutor{outputs: []string{
		`{"findings":[{"title":"wrong result","body":"The new branch returns the opposite value.","priority":1,"confidence_score":0.95,"code_location":{"absolute_file_path":"` + path + `","line_range":{"start":2,"end":2}}}]}`,
		`{"decisions":[{"index":0,"reason":"Missing the required keep field."}]}`,
	}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Execution.VerifierStatus != quality.VerifierFailedOpen {
		t.Fatalf("malformed verifier did not fail open: %#v", result)
	}
}

func TestCandidateOutsideCheckoutCannotProducePass(t *testing.T) {
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{
		`{"findings":[{"title":"wrong result","body":"The new branch returns the opposite value.","priority":1,"confidence_score":0.95,"code_location":{"absolute_file_path":"/tmp/outside.go","line_range":{"start":2,"end":2}}}]}`,
	}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultIncomplete || result.Execution.ModelCalls != 1 || len(result.Execution.AdapterDrops) != 1 {
		t.Fatalf("out-of-scope candidate result = %#v", result)
	}
}

type scriptedExecutor struct {
	outputs     []string
	errors      []error
	invocations []Invocation
}

func (executor *scriptedExecutor) Run(_ context.Context, invocation Invocation) error {
	index := len(executor.invocations)
	executor.invocations = append(executor.invocations, invocation)
	if index < len(executor.errors) && executor.errors[index] != nil {
		return executor.errors[index]
	}
	if index >= len(executor.outputs) {
		return errors.New("unexpected invocation")
	}
	return os.WriteFile(invocation.OutputPath, []byte(executor.outputs[index]), 0o600)
}

func nativeFixture(t *testing.T) (reviewsession.Prepared, quality.ReviewRequest) {
	t.Helper()
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	output := filepath.Join(directory, "output")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\nfunc Run() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(directory, "trusted.diff")
	if err := os.WriteFile(diffPath, []byte("+++ b/app.go\n+func Run() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared := reviewsession.Prepared{
		SessionDir: directory, RepositoryDir: repository, DiffPath: diffPath,
		NativeReviewSchemaPath: filepath.Join(directory, "native.schema.json"),
		VerifierSchemaPath:     filepath.Join(directory, "verifier.schema.json"),
		NativeReviewPath:       filepath.Join(output, "native-review.json"),
		VerifierOutputPath:     filepath.Join(output, "candidate-verdicts.json"),
	}
	request := quality.ReviewRequest{
		Repository: "example/repo", TargetBranch: "main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	return prepared, request
}

func directionsKey(directions []quality.ReviewDirection) string {
	values := make([]string, 0, len(directions))
	for _, direction := range directions {
		values = append(values, direction.ID)
	}
	return strings.Join(values, ",")
}
