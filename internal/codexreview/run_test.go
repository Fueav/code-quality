package codexreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestNativeReviewInvocationUsesOneCustomTarget(t *testing.T) {
	prepared, request := nativeFixture(t)
	options := Options{
		Prepared: prepared, Request: request, Goal: "protect settlement correctness",
		Model: "gpt-5.6-sol", ReasoningEffort: "high",
	}
	invocation := buildReviewInvocation(options)
	args := strings.Join(invocation.Args, " ")
	if !strings.Contains(args, "exec --sandbox read-only --ignore-user-config --ignore-rules --ephemeral review") {
		t.Fatalf("native review args = %q", args)
	}
	if !strings.Contains(args, "--json") {
		t.Fatalf("native review does not retain machine-readable usage events: %q", args)
	}
	for _, forbidden := range []string{"--base", "--commit", "--uncommitted", "--output-schema"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("custom target is combined with %s: %q", forbidden, args)
		}
	}
	for _, required := range []string{request.BaseCommit, request.TargetCommit, "protect settlement correctness", "optional focus", "not a review boundary"} {
		if !strings.Contains(invocation.Stdin, required) {
			t.Fatalf("prompt is missing %q:\n%s", required, invocation.Stdin)
		}
	}
	for _, forbidden := range []string{"Potential risk directions", "security boundaries", "reliability lifecycle", "first nonblank line exactly"} {
		if strings.Contains(invocation.Stdin, forbidden) {
			t.Fatalf("prompt contains automatic direction or private protocol %q:\n%s", forbidden, invocation.Stdin)
		}
	}
}

func TestAdaptFindingsAcceptsCanonicalEquivalentCheckoutRoots(t *testing.T) {
	root := t.TempDir()
	realRepository := filepath.Join(root, "real-repository")
	aliasRepository := filepath.Join(root, "alias-repository")
	if err := os.MkdirAll(realRepository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRepository, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRepository, aliasRepository); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve behavior", Body: "The changed branch returns the wrong value.", Priority: 1,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: filepath.Join(realRepository, "app.go"),
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, aliasRepository, []string{"app.go"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "app.go" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsRejectsCanonicalSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "linked")); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "outside candidate", Body: "This location resolves outside the checkout.", Priority: 1,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: filepath.Join(repository, "linked", "app.go"),
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"linked/app.go"})
	if len(findings) != 0 || len(drops) != 1 || drops[0].Reason != "code location is outside the isolated checkout" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestReadCodexUsageUsesLastCompletedTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	contents := strings.Join([]string{
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`,
		`{"type":"item.completed","item":{"type":"agent_message"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":30,"output_tokens":7}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	input, output, err := readCodexUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if input == nil || *input != 30 || output == nil || *output != 7 {
		t.Fatalf("input = %v, output = %v", input, output)
	}
}

func TestReadCodexUsageRejectsAllZeroCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	contents := `{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	input, output, err := readCodexUsage(path)
	if err == nil || !strings.Contains(err.Error(), "zero token usage") || input != nil || output != nil {
		t.Fatalf("input = %v, output = %v, error = %v", input, output, err)
	}
}

func TestCollectRunMetricsMarksZeroCountersUnavailable(t *testing.T) {
	prepared, request := nativeFixture(t)
	stdout := reviewsession.NewLayout(prepared.SessionDir).NativeStdoutPath
	contents := `{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	if err := os.WriteFile(stdout, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := collectRunMetrics(Options{Prepared: prepared, Request: request}, 25*time.Millisecond, 42)
	if metrics.UsageAvailable || metrics.InputTokens != nil || metrics.OutputTokens != nil ||
		!strings.Contains(metrics.UsageError, "zero token usage") {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestNativeReviewPromptOmitsUnsuppliedGoal(t *testing.T) {
	prepared, request := nativeFixture(t)
	options := Options{Prepared: prepared, Request: request, ReasoningEffort: "high"}
	if err := normalizeOptions(&options); err != nil {
		t.Fatal(err)
	}
	if options.Goal != "" {
		t.Fatalf("wrapper invented goal %q", options.Goal)
	}
	prompt := buildReviewPrompt(options)
	for _, forbidden := range []string{"Review goal", "optional focus", "Find actionable defects introduced"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt contains invented goal text %q:\n%s", forbidden, prompt)
		}
	}
}

func TestReadNativeOutputParsesNativeReviewText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	file := "/private/tmp/review/input/repository/client.go"
	contents := `The refactor discards the request context and prevents cancellation.

Review comment:

- [P0] Propagate the caller context to the downstream call — ` + file + `:24-24
  When the downstream response never arrives, context.Background is never canceled.
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("findings = %#v", result.Findings)
	}
	finding := result.Findings[0]
	if finding.Title != "Propagate the caller context to the downstream call" || finding.Priority != 0 || finding.Body != "When the downstream response never arrives, context.Background is never canceled." {
		t.Fatalf("finding = %#v", finding)
	}
	if finding.CodeLocation.AbsoluteFilePath != file || finding.CodeLocation.LineRange.Start != 24 || finding.CodeLocation.LineRange.End != 24 {
		t.Fatalf("location = %#v", finding.CodeLocation)
	}
}

func TestReadNativeOutputAcceptsExplicitNoFindingsText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	if err := os.WriteFile(path, []byte("No findings.\n\nThe change preserves the existing deadline behavior.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestReadNativeOutputParsesMultipleFindingsAndTrailingAssessment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	contents := `Review comment:

- [P1] Preserve cancellation — /private/tmp/review/client.go:17-18
  The new context drops caller cancellation.
- [P2] Close the response body — /private/tmp/review/client.go:31
  The changed success path leaks the response body.

Overall assessment: the change needs both fixes before release.
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("findings = %#v", result.Findings)
	}
	if result.Findings[1].CodeLocation.LineRange.Start != 31 || result.Findings[1].CodeLocation.LineRange.End != 31 {
		t.Fatalf("second location = %#v", result.Findings[1].CodeLocation)
	}
}

func TestReadNativeOutputReplaysV1FullReviewComments(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	path := filepath.Join(
		filepath.Dir(source),
		"..", "..", "reports", "2026-07-30-native-review-feasibility-v1", "evidence",
		"S04", "goal_native", "output", "native-review.txt",
	)

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 2 {
		t.Fatalf("findings = %#v", result.Findings)
	}
	if result.Findings[0].Title != "Avoid cloning the full database on every order" {
		t.Fatalf("first finding = %#v", result.Findings[0])
	}
}

func TestNativeOutputAcceptsObservedCandidateHeadingGrammar(t *testing.T) {
	for _, heading := range []string{
		"Review comment:",
		"Review comments:",
		"Full review comment:",
		"Full review comments:",
	} {
		t.Run(heading, func(t *testing.T) {
			input := heading + "\n\n- [P1] Preserve ownership — /private/tmp/review/app.go:12\n  The new branch drops the tenant check.\n"
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestReadNativeOutputAcceptsNativeZeroCandidateNarrative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	if err := os.WriteFile(path, []byte("The derived timeout context preserves parent cancellation, earlier deadlines, and request-scoped values while enforcing the approved two-second limit.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestReadNativeOutputRejectsOrphanFindingHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	if err := os.WriteFile(path, []byte("- [P1] Preserve cancellation — /private/tmp/review/client.go:17-18\n  The new context drops caller cancellation.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readNativeOutput(path); err == nil {
		t.Fatal("orphan native finding header was accepted as zero candidates")
	}
}

func TestNativeOutputRejectsEmptyResults(t *testing.T) {
	for name, input := range map[string]string{
		"empty output":                 "\n\t\n",
		"empty candidate section":      "Review comment:\n",
		"empty full candidate section": "Full review comments:\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNativeReviewText(input); err == nil {
				t.Fatal("invalid native output was accepted as zero candidates")
			}
		})
	}
}

func TestNativeOutputRejectsNoFindingsFollowedByAnyCandidateHeading(t *testing.T) {
	for _, heading := range []string{
		"Review comment:",
		"Review comments:",
		"Full review comment:",
		"Full review comments:",
	} {
		t.Run(heading, func(t *testing.T) {
			if _, err := parseNativeReviewText("No findings.\n\n" + heading + "\n"); err == nil {
				t.Fatal("contradictory native output was accepted")
			}
		})
	}
}

func TestZeroFindingsCompletesAfterOneNativeCall(t *testing.T) {
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{"The target preserves cancellation and enforces the approved timeout.\n"}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultPass || result.Execution.ModelCalls != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(executor.invocations) != 1 {
		t.Fatalf("model calls = %d", len(executor.invocations))
	}
}

func TestNativeFindingsCompleteAfterOneNativeCall(t *testing.T) {
	prepared, request := nativeFixture(t)
	path := filepath.Join(prepared.RepositoryDir, "app.go")
	executor := &scriptedExecutor{outputs: []string{nativeFindingOutput(path, "timeout is ignored", "The new call can wait forever.", 1)}}

	result, err := Run(context.Background(), Options{
		Prepared: prepared, Request: request, Goal: "review the change",
		ReasoningEffort: "high", EvaluationRubricVersion: "1.2.0", Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultManualReview || len(result.Findings) != 1 || result.Execution.ModelCalls != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(executor.invocations) != 1 {
		t.Fatalf("model calls = %d", len(executor.invocations))
	}
}

func TestCandidateOutsideCheckoutCannotProducePass(t *testing.T) {
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{
		nativeFindingOutput("/tmp/outside.go", "wrong result", "The new branch returns the opposite value.", 1),
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
		NativeReviewPath: filepath.Join(output, "native-review.txt"),
	}
	request := quality.ReviewRequest{
		Repository: "example/repo", TargetBranch: "main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	return prepared, request
}

func nativeFindingOutput(path, title, body string, priority int) string {
	return fmt.Sprintf("Review comment:\n\n- [P%d] %s — %s:2-2\n  %s\n", priority, title, path, body)
}
