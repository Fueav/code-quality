package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestFullClaudeInvocationPreservesNormalCapabilities(t *testing.T) {
	session, request := nativeFixture(t)
	options := nativeRunOptions{
		Session:  session,
		Goal:     "protect settlement correctness",
		Provider: NewClaudeProvider(""),
	}
	if err := normalizeRunOptions(&options); err != nil {
		t.Fatal(err)
	}
	invocation := buildReviewInvocation(options)
	wantPrompt := "Review the changes introduced by " + request.TargetCommit + " relative to " + request.BaseCommit + " for concrete defects introduced or worsened by this change.\n" +
		"User-supplied context: \"protect settlement correctness\"\n" +
		"Report actionable findings only. Do not modify files, commit, push, deploy, or change external state.\n" +
		"Use the priority definitions embedded in the configured JSON Schema. Exclude pure style, naming, preference, ordinary maintainability, and scale speculation without evidence.\n" +
		"Return only the structured findings document required by the configured JSON Schema. Use repository-relative changed-file paths and the smallest useful line range. If no concrete defect exists, return an empty findings array.\n"
	want := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--permission-mode", "auto", "--model", "opus", "--effort", "max", "--json-schema", string(options.OutputSchema), wantPrompt,
	}
	if invocation.executable != "claude" || strings.Join(invocation.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("full Claude invocation = %q %#v, want claude %#v", invocation.executable, invocation.args, want)
	}
	if invocation.directory != session.RepositoryDirectory() || invocation.stdin != "" {
		t.Fatalf("Claude isolation = directory %q, stdin %q", invocation.directory, invocation.stdin)
	}
	for _, forbidden := range []string{
		"--safe-mode", "--bare", "--disable-slash-commands", "--strict-mcp-config", "--tools",
		"--setting-sources", "--max-turns", "--max-budget-usd", "plan", "dontAsk",
	} {
		for _, argument := range invocation.args {
			if argument == forbidden || strings.HasPrefix(argument, forbidden+"=") {
				t.Fatalf("full Claude invocation contains capability restriction %q: %#v", forbidden, invocation.args)
			}
		}
	}
	if options.Model != "opus" || options.ReasoningEffort != "max" {
		t.Fatalf("Claude defaults = model %q, effort %q", options.Model, options.ReasoningEffort)
	}
}

func TestProductionCIClaudeInvocationUsesReadOnlyPlanAndNoCustomizations(t *testing.T) {
	session, _ := nativeFixture(t)
	options := nativeRunOptions{
		Session: session, Provider: NewClaudeProvider(""),
		ExecutionProfile: quality.ExecutionProfileProductionCI,
	}
	if err := normalizeRunOptions(&options); err != nil {
		t.Fatal(err)
	}
	invocation := buildReviewInvocation(options)
	joined := strings.Join(invocation.args, "\x00")
	for _, required := range []string{"--permission-mode\x00plan", "--safe-mode", "--strict-mcp-config"} {
		if !strings.Contains(joined, required) {
			t.Errorf("production Claude args are missing %q: %#v", required, invocation.args)
		}
	}
	if strings.Contains(joined, "--permission-mode\x00auto") {
		t.Fatalf("production Claude args enable auto mode: %#v", invocation.args)
	}
}

func TestRestrictedClaudeInvocationUsesPolicyAsSystemPrompt(t *testing.T) {
	session, _ := nativeFixture(t)
	finding, err := quality.IdentifyNativeFinding(quality.NativeFinding{
		Title: "money loss", Priority: 1, Reason: "The changed path loses money.", Suggestion: "Preserve the transfer.",
		CodeLocation: quality.NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := []byte("trusted production-floor policy")
	schema := []byte(`{"type":"object"}`)
	invocation := NewClaudeProvider("").buildRestrictedInvocation(restrictedInvocationOptions{
		Session: session, Plan: restrictedFixturePlan(session), Findings: []quality.NativeFinding{finding}, Model: "opus", ReasoningEffort: "max",
		Policy: policy, OutputSchema: schema,
	})
	joined := strings.Join(invocation.args, "\x00")
	for _, required := range []string{
		"--permission-mode\x00plan", "--safe-mode", "--strict-mcp-config",
		"--system-prompt\x00trusted production-floor policy", "--json-schema\x00" + string(schema), finding.ID,
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("restricted Claude invocation is missing %q: %#v", required, invocation.args)
		}
	}
	if strings.Contains(joined, "--permission-mode\x00auto") {
		t.Fatalf("restricted Claude invocation enables auto mode: %#v", invocation.args)
	}
}

func TestClaudeTranscriptExtractsFinalResultAndUsage(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"system","subtype":"init","tools":["Read","Grep"],"mcp_servers":[{"name":"github","status":"connected"}]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"No findings.","usage":{"input_tokens":321,"output_tokens":54}}`,
	}, "\n") + "\n"
	transcript, err := decodeClaudeTranscript(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(transcript.FinalMessage) != "No findings." || transcript.InputTokens == nil || *transcript.InputTokens != 321 ||
		transcript.OutputTokens == nil || *transcript.OutputTokens != 54 || transcript.UsageError != nil {
		t.Fatalf("Claude transcript = %#v", transcript)
	}
}

func TestClaudeTranscriptRejectsMissingDuplicateAndErrorResults(t *testing.T) {
	tests := map[string]string{
		"missing":   `{"type":"assistant"}` + "\n",
		"duplicate": `{"type":"result","subtype":"success","result":"first","usage":{"input_tokens":1,"output_tokens":1}}` + "\n" + `{"type":"result","subtype":"success","result":"second","usage":{"input_tokens":1,"output_tokens":1}}` + "\n",
		"error":     `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"failed"}` + "\n",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeClaudeTranscript(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid Claude result stream was accepted")
			}
		})
	}
}

func TestNativeProcessInheritsCallerEnvironment(t *testing.T) {
	root := t.TempDir()
	value := "normal-user-context-visible"
	t.Setenv("CODE_QUALITY_CLAUDE_CONTEXT_TEST", value)
	stdoutPath := filepath.Join(root, "stdout")
	err := runNativeProcess(context.Background(), reviewInvocation{
		executable: exec.Command("sh").Path,
		args:       []string{"-c", `printf '%s' "$CODE_QUALITY_CLAUDE_CONTEXT_TEST"`},
		directory:  root,
		paths: capturePaths{
			jsonl:  stdoutPath,
			stderr: filepath.Join(root, "stderr"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil || !bytes.Equal(raw, []byte(value)) {
		t.Fatalf("inherited environment = %q, error = %v", raw, err)
	}
}

func TestClaudeRunMaterializesAndFreezesTranscriptResult(t *testing.T) {
	session, _ := nativeFixture(t)
	executable := fakeClaudeExecutable(t, strings.Join([]string{
		`{"type":"system","subtype":"init","tools":["Read","Grep"],"mcp_servers":[{"name":"github","status":"connected"}]}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"{\"findings\":[]}","usage":{"input_tokens":211,"output_tokens":17}}`,
	}, "\n")+"\n", 0)
	outcome, err := runNativeSession(context.Background(), nativeRunOptions{
		Session: session, Provider: NewClaudeProvider(executable),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultPass || result.Execution.Host != "claude-code" ||
		result.Execution.ProviderInvocations != 1 {
		t.Fatalf("Claude outcome = %#v", result)
	}
	artifacts := session.Artifacts()
	final, err := os.ReadFile(artifacts.FinalMessagePath())
	if err != nil || string(final) != `{"findings":[]}` {
		t.Fatalf("materialized final = %q, error = %v", final, err)
	}
	info, err := os.Stat(artifacts.FinalMessagePath())
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("frozen final mode = %v, error = %v", info, err)
	}
	metricsRaw, err := os.ReadFile(artifacts.MetricsPath())
	if err != nil {
		t.Fatal(err)
	}
	var metrics NativeRunMetrics
	if err := json.Unmarshal(metricsRaw, &metrics); err != nil {
		t.Fatal(err)
	}
	if !metrics.UsageAvailable || metrics.InputTokens == nil || *metrics.InputTokens != 211 ||
		metrics.OutputTokens == nil || *metrics.OutputTokens != 17 {
		t.Fatalf("Claude metrics = %#v", metrics)
	}
}

func TestClaudeFrozenFinalMustMatchFrozenTranscript(t *testing.T) {
	paths := captureFixture(t)
	if err := os.WriteFile(paths.finalMessage, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.jsonl, []byte(`{"type":"result","subtype":"success","is_error":false,"result":"No findings.","usage":{"input_tokens":8,"output_tokens":2}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.stderr, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	frozen, err := freezeNativeArtifacts(paths, NewClaudeProvider(""))
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ProtocolError == nil || !strings.Contains(frozen.ProtocolError.Error(), "does not match") {
		t.Fatalf("mismatched Claude evidence protocol error = %v", frozen.ProtocolError)
	}
}

func TestClaudeProtocolFailureBecomesErrorWithoutFallback(t *testing.T) {
	session, _ := nativeFixture(t)
	executable := fakeClaudeExecutable(t,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"auto permission mode unavailable"}`+"\n", 0)
	outcome, err := runNativeSession(context.Background(), nativeRunOptions{
		Session: session, Provider: NewClaudeProvider(executable),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultError || result.Execution.Host != "claude-code" ||
		!strings.Contains(strings.Join(result.Adjudication.Reasons, " "), "Claude result event is not successful") {
		t.Fatalf("Claude protocol failure = %#v", result)
	}
}

func fakeClaudeExecutable(t *testing.T, stream string, exitCode int) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "claude")
	streamPath := filepath.Join(directory, "stream.jsonl")
	if err := os.WriteFile(streamPath, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nset -eu\ncat \"" + streamPath + "\"\nexit " + fmt.Sprint(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
