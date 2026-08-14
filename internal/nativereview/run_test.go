package nativereview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestFullCodexInvocationRestoresNormalCapabilities(t *testing.T) {
	session, request := nativeFixture(t)
	options := nativeRunOptions{
		Session: session, Goal: "protect settlement correctness",
	}
	if err := normalizeRunOptions(&options); err != nil {
		t.Fatal(err)
	}
	invocation := buildReviewInvocation(options)
	want := []string{
		"exec", "--sandbox", "workspace-write",
		"--config", "sandbox_workspace_write.network_access=true",
		"--model", "gpt-5.6-sol", "--output-schema", session.OutputSchemaPath(), "--config", `model_reasoning_effort="max"`,
		"--json", "--output-last-message", session.Artifacts().FinalMessagePath(), "-",
	}
	if strings.Join(invocation.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("full Codex args = %#v, want %#v", invocation.args, want)
	}
	for _, forbidden := range []string{"review", "--ignore-user-config", "--ignore-rules", "--ephemeral", "read-only"} {
		for _, argument := range invocation.args {
			if argument == forbidden {
				t.Fatalf("full Codex invocation contains %q: %#v", forbidden, invocation.args)
			}
		}
	}
	wantPrompt := fmt.Sprintf(
		"Review the changes introduced by %s relative to %s for concrete defects introduced or worsened by this change.\nUser-supplied context: %q\nUse the priority definitions embedded in the configured JSON Schema. Exclude pure style, naming, preference, ordinary maintainability, and scale speculation without evidence.\nReturn only the structured findings document required by the configured JSON Schema. Use repository-relative changed-file paths and the smallest useful line range. If no concrete defect exists, return an empty findings array.\n",
		request.TargetCommit, request.BaseCommit, "protect settlement correctness",
	)
	if invocation.stdin != wantPrompt {
		t.Fatalf("prompt = %q, want %q", invocation.stdin, wantPrompt)
	}
	if options.Model != "gpt-5.6-sol" || options.ReasoningEffort != "max" {
		t.Fatalf("defaults = model %q, effort %q", options.Model, options.ReasoningEffort)
	}
}

func TestProductionCICodexInvocationIsReadOnlyAndIgnoresCustomizations(t *testing.T) {
	session, _ := nativeFixture(t)
	options := nativeRunOptions{Session: session, ExecutionProfile: quality.ExecutionProfileProductionCI}
	if err := normalizeRunOptions(&options); err != nil {
		t.Fatal(err)
	}
	invocation := buildReviewInvocation(options)
	joined := strings.Join(invocation.args, "\x00")
	for _, required := range []string{"read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--disable", "hooks"} {
		if !strings.Contains(joined, required) {
			t.Errorf("production Codex args are missing %q: %#v", required, invocation.args)
		}
	}
	for _, forbidden := range []string{"workspace-write", "sandbox_workspace_write.network_access=true"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("production Codex args contain %q: %#v", forbidden, invocation.args)
		}
	}
	if !strings.Contains(invocation.stdin, "Do not modify files") {
		t.Fatalf("production prompt = %q", invocation.stdin)
	}
}

func TestRestrictedCodexInvocationIsAlwaysReadOnlyAndPolicyBound(t *testing.T) {
	session, _ := nativeFixture(t)
	finding, err := quality.IdentifyNativeFinding(quality.NativeFinding{
		Title: "money loss", Priority: 1, Reason: "The changed path loses money.", Suggestion: "Preserve the transfer.",
		CodeLocation: quality.NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := []byte("trusted production-floor policy")
	invocation := NewCodexProvider("").buildRestrictedInvocation(restrictedInvocationOptions{
		Session: session, Plan: restrictedFixturePlan(session), Findings: []quality.NativeFinding{finding}, Model: "gpt-5.6-sol",
		ReasoningEffort: "max", Policy: policy, OutputSchema: []byte(`{"type":"object"}`),
	})
	joined := strings.Join(invocation.args, "\x00")
	for _, required := range []string{
		"--sandbox\x00read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--disable\x00hooks",
		"--output-schema\x00" + session.RestrictedAdjudicationSchemaPath(),
		"developer_instructions=\"trusted production-floor policy\"",
		"--output-last-message\x00" + session.Artifacts().RestrictedFinalMessagePath(),
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("restricted Codex invocation is missing %q: %#v", required, invocation.args)
		}
	}
	for _, forbidden := range []string{"workspace-write", "sandbox_workspace_write.network_access=true"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("restricted Codex invocation contains %q: %#v", forbidden, invocation.args)
		}
	}
	if !strings.Contains(invocation.stdin, finding.ID) || !strings.Contains(invocation.stdin, "P0/P1") {
		t.Fatalf("restricted prompt = %q", invocation.stdin)
	}
}

func TestEvidenceCaptureReturnsChildFailure(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "input", "repository")
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	err = runCodexProcess(context.Background(), reviewInvocation{
		executable: executable,
		args:       []string{"-c", "exit 7"},
		directory:  repository,
		paths: capturePaths{
			jsonl: filepath.Join(output, "stdout"), stderr: filepath.Join(output, "stderr"),
		},
	})
	if err == nil {
		t.Fatal("failing child process unexpectedly succeeded")
	}
}

func TestEvidenceCapturePassesInheritedFiles(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	inheritedPath := filepath.Join(root, "inherited")
	if err := os.WriteFile(inheritedPath, []byte("inherited lease\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inherited, err := os.Open(inheritedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	stdoutPath := filepath.Join(root, "stdout")
	err = runCodexProcess(context.Background(), reviewInvocation{
		executable: exec.Command("sh").Path,
		args:       []string{"-c", "cat <&3"},
		directory:  repository,
		paths:      capturePaths{jsonl: stdoutPath, stderr: filepath.Join(root, "stderr")},
		extraFiles: []*os.File{inherited},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil || string(raw) != "inherited lease\n" {
		t.Fatalf("inherited output = %q, error = %v", raw, err)
	}
}

func TestEvidenceCaptureQuiescesDescendantWriters(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "input", "repository")
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODE_QUALITY_EVIDENCE_CAPTURE_HELPER", "parent")
	stdoutPath := filepath.Join(output, "stdout")
	err := runCodexProcess(context.Background(), reviewInvocation{
		executable: os.Args[0],
		args:       []string{"-test.run=^TestEvidenceCaptureDescendantWriterHelper$"},
		directory:  repository,
		paths:      capturePaths{jsonl: stdoutPath, stderr: filepath.Join(output, "stderr")},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "descendant completed\n") {
		t.Fatalf("executor returned before descendant output: %q", raw)
	}
	before, err := os.Stat(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	after, err := os.Stat(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("stdout changed after executor returned: before=%v after=%v", before, after)
	}
}

func TestEvidenceCaptureDescendantWriterHelper(t *testing.T) {
	switch os.Getenv("CODE_QUALITY_EVIDENCE_CAPTURE_HELPER") {
	case "":
		return
	case "parent":
		command := exec.Command(os.Args[0], "-test.run=^TestEvidenceCaptureDescendantWriterHelper$")
		command.Env = append(os.Environ(), "CODE_QUALITY_EVIDENCE_CAPTURE_HELPER=descendant")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "parent completed")
		os.Exit(0)
	case "descendant":
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "descendant completed")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestRunFreezesRawArtifactsBeforeClassification(t *testing.T) {
	session, _ := nativeFixture(t)
	raw := nativeFindingOutput("app.go", "preserve the result", "The changed branch returns the wrong value.", 1)
	executable, _ := fakeCodexExecutable(t, raw)

	outcome, err := runNativeSession(context.Background(), nativeRunOptions{Session: session, Provider: NewCodexProvider(executable)})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultBlock || len(result.Findings) != 1 ||
		len(result.Execution.AdapterDrops) != 0 {
		t.Fatalf("result = %#v", result)
	}
	artifacts := session.Artifacts()
	var manifest struct {
		SchemaVersion int `json:"schema_version"`
		Artifacts     []struct {
			Name    string `json:"name"`
			Present bool   `json:"present"`
			Bytes   int    `json:"bytes"`
			SHA256  string `json:"sha256"`
		} `json:"artifacts"`
	}
	encoded, err := os.ReadFile(artifacts.FreezeManifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Artifacts) != 3 {
		t.Fatalf("freeze manifest = %#v", manifest)
	}
	digest := sha256.Sum256([]byte(raw))
	wantSHA := hex.EncodeToString(digest[:])
	if manifest.Artifacts[0].Name != "final_message" || !manifest.Artifacts[0].Present ||
		manifest.Artifacts[0].Bytes != len(raw) || manifest.Artifacts[0].SHA256 != wantSHA {
		t.Fatalf("frozen final message = %#v", manifest.Artifacts[0])
	}
	info, err := os.Stat(artifacts.FinalMessagePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("raw review remains writable: %o", info.Mode().Perm())
	}
	if err := os.WriteFile(artifacts.FinalMessagePath(), []byte("changed"), 0o600); err == nil {
		t.Fatal("frozen raw review was overwritten")
	}
}

func TestFreezeAndUsageStreamCompleteLargeJSONL(t *testing.T) {
	paths := captureFixture(t)
	if err := os.WriteFile(paths.finalMessage, []byte("No findings.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fillerLine := "{\"type\":\"item.completed\"}\n"
	filler := strings.Repeat(fillerLine, int(maxNativeOutputBytes)/len(fillerLine)+2)
	usageLine := "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":321,\"output_tokens\":54}}\n"
	if err := os.WriteFile(paths.jsonl, []byte(filler+usageLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.stderr, []byte("warning\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	frozen, err := freezeNativeArtifacts(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.FinalMessage) == 0 || int64(frozen.Manifest.Artifacts[1].Bytes) <= maxNativeOutputBytes {
		t.Fatalf("freeze manifest = %#v", frozen.Manifest)
	}
	if frozen.InputTokens == nil || *frozen.InputTokens != 321 ||
		frozen.OutputTokens == nil || *frozen.OutputTokens != 54 || frozen.UsageError != nil {
		t.Fatalf("usage = %v/%v, error = %v", frozen.InputTokens, frozen.OutputTokens, frozen.UsageError)
	}
}

func TestFreezeSnapshotsAwayFromAlreadyOpenWriter(t *testing.T) {
	paths := captureFixture(t)
	original := []byte("original evidence\n")
	if err := os.WriteFile(paths.finalMessage, original, 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(paths.finalMessage, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	frozen, err := freezeNativeArtifacts(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt([]byte("mutated evidence!\n"), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(paths.finalMessage)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("published evidence changed through an old writer: %q", got)
	}
	digest := sha256.Sum256(got)
	if frozen.Manifest.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("manifest digest = %q, published digest = %x", frozen.Manifest.Artifacts[0].SHA256, digest)
	}
}

func TestLockedArtifactRejectsPathReplacementDuringHash(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "native-review.stdout.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	const size = int64(128 << 20)
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o400); err != nil {
		t.Fatal(err)
	}
	bytesRead, digest, err := hashFile(file)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	replacementFile, err := os.OpenFile(replacement, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacementFile.Truncate(size); err != nil {
		replacementFile.Close()
		t.Fatal(err)
	}
	if err := replacementFile.Chmod(0o400); err != nil {
		replacementFile.Close()
		t.Fatal(err)
	}
	if err := replacementFile.Close(); err != nil {
		t.Fatal(err)
	}
	replaced := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		replaced <- os.Rename(replacement, path)
	}()

	validationErr := validateLockedArtifact(path, file, bytesRead, digest)
	if err := <-replaced; err != nil {
		t.Fatal(err)
	}
	if validationErr == nil {
		t.Fatal("path replacement during retained-inode hashing was accepted")
	}
}

func TestSnapshotTemporaryPathIsNeverWritable(t *testing.T) {
	directory := t.TempDir()
	temporary, err := createReadOnlyTemp(directory, ".native-review-snapshot-")
	if err != nil {
		t.Fatal(err)
	}
	defer temporary.Close()
	defer os.Remove(temporary.Name())
	info, err := temporary.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("temporary mode = %o", info.Mode().Perm())
	}
	if writer, err := os.OpenFile(temporary.Name(), os.O_WRONLY, 0); err == nil {
		writer.Close()
		t.Fatal("snapshot temporary inode exposed a writable pathname descriptor")
	}
	if _, err := temporary.WriteString("evidence\n"); err != nil {
		t.Fatalf("creator descriptor lost write access: %v", err)
	}
}

func TestRunMetricsStayBoundToFrozenStdout(t *testing.T) {
	_, request := nativeFixture(t)
	paths := captureFixture(t)
	if err := os.WriteFile(paths.finalMessage, []byte("No findings.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := `{"type":"turn.completed","usage":{"input_tokens":30,"output_tokens":7}}` + "\n"
	if err := os.WriteFile(paths.jsonl, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.stderr, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	frozen, err := freezeNativeArtifacts(paths)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(paths.jsonl), "replacement-stdout")
	other := `{"type":"turn.completed","usage":{"input_tokens":999,"output_tokens":888}}` + "\n"
	if err := os.WriteFile(replacement, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, paths.jsonl); err != nil {
		t.Fatal(err)
	}

	metrics := collectRunMetrics(request, 25*time.Millisecond, 42, frozen)
	if !metrics.UsageAvailable || metrics.InputTokens == nil || *metrics.InputTokens != 30 ||
		metrics.OutputTokens == nil || *metrics.OutputTokens != 7 {
		t.Fatalf("metrics escaped frozen stdout: %#v", metrics)
	}
}

func TestDecodeCodexUsageUsesLastCompletedTurn(t *testing.T) {
	contents := strings.Join([]string{
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`,
		`{"type":"item.completed","item":{"type":"agent_message"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":30,"output_tokens":7}}`,
	}, "\n") + "\n"
	input, output, err := decodeCodexUsageReader(strings.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if input == nil || *input != 30 || output == nil || *output != 7 {
		t.Fatalf("input = %v, output = %v", input, output)
	}
}

func TestDecodeCodexUsageRejectsAllZeroCounters(t *testing.T) {
	contents := `{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	input, output, err := decodeCodexUsageReader(strings.NewReader(contents))
	if err == nil || !strings.Contains(err.Error(), "zero token usage") || input != nil || output != nil {
		t.Fatalf("input = %v, output = %v, error = %v", input, output, err)
	}
}

func TestCollectRunMetricsMarksZeroCountersUnavailable(t *testing.T) {
	_, request := nativeFixture(t)
	paths := captureFixture(t)
	stdout := paths.jsonl
	contents := `{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	if err := os.WriteFile(stdout, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	frozen, err := freezeNativeArtifacts(paths)
	if err != nil {
		t.Fatal(err)
	}
	metrics := collectRunMetrics(request, 25*time.Millisecond, 42, frozen)
	if metrics.UsageAvailable || metrics.InputTokens != nil || metrics.OutputTokens != nil ||
		!strings.Contains(metrics.UsageError, "zero token usage") {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestNativeReviewPromptOmitsUnsuppliedGoal(t *testing.T) {
	session, request := nativeFixture(t)
	options := nativeRunOptions{Session: session}
	if err := normalizeRunOptions(&options); err != nil {
		t.Fatal(err)
	}
	if options.Goal != "" {
		t.Fatalf("wrapper invented goal %q", options.Goal)
	}
	want := fmt.Sprintf("Review the changes introduced by %s relative to %s for concrete defects introduced or worsened by this change.\nUse the priority definitions embedded in the configured JSON Schema. Exclude pure style, naming, preference, ordinary maintainability, and scale speculation without evidence.\nReturn only the structured findings document required by the configured JSON Schema. Use repository-relative changed-file paths and the smallest useful line range. If no concrete defect exists, return an empty findings array.\n", request.TargetCommit, request.BaseCommit)
	if prompt := buildReviewPrompt(options.Session.Request(), options.Goal, false); prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestFreezeManifestDoesNotOverwriteExistingEvidence(t *testing.T) {
	paths := captureFixture(t)
	for _, artifact := range []string{paths.finalMessage, paths.jsonl, paths.stderr} {
		if err := os.WriteFile(artifact, []byte("raw"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.freezeManifest, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := freezeNativeArtifacts(paths); err == nil {
		t.Fatal("existing freeze manifest was overwritten")
	}
	raw, err := os.ReadFile(paths.freezeManifest)
	if err != nil || string(raw) != "sentinel" {
		t.Fatalf("freeze manifest = %q, error = %v", raw, err)
	}
}

func TestFreezeManifestRejectsTemporaryPathReplacement(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "native-review-freeze.json")
	manifest := NativeFreezeManifest{SchemaVersion: 1, Artifacts: []FrozenArtifact{}}
	validate := func() error {
		matches, err := filepath.Glob(filepath.Join(directory, ".native-review-freeze-*"))
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return fmt.Errorf("temporary manifests = %v", matches)
		}
		replacement := filepath.Join(directory, "replacement-manifest")
		if err := os.WriteFile(replacement, []byte("forged manifest\n"), 0o400); err != nil {
			return err
		}
		return os.Rename(replacement, matches[0])
	}

	if err := writeDurableFreezeManifest(manifestPath, manifest, validate); err == nil {
		t.Fatal("replacement manifest inode was published")
	}
	if _, err := os.Lstat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forged manifest remains published: %v", err)
	}
}

func TestFreezeManifestRevalidatesEvidenceImmediatelyBeforePublication(t *testing.T) {
	directory := t.TempDir()
	manifestPath := filepath.Join(directory, "native-review-freeze.json")
	manifest := NativeFreezeManifest{SchemaVersion: 1, Artifacts: []FrozenArtifact{}}
	validationCalls := 0
	validate := func() error {
		validationCalls++
		if validationCalls == 2 {
			return errors.New("late raw artifact change")
		}
		return nil
	}

	if err := writeDurableFreezeManifest(manifestPath, manifest, validate); err == nil {
		t.Fatal("manifest was published without a final evidence revalidation")
	}
	if validationCalls != 2 {
		t.Fatalf("evidence validation calls = %d, want 2", validationCalls)
	}
	if _, err := os.Lstat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest was published after late evidence change: %v", err)
	}
}

func TestAbsentArtifactValidationRejectsLateCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.stderr.log")
	if err := validateAbsentArtifactPaths([]string{path}); err != nil {
		t.Fatalf("absent path was rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("late output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateAbsentArtifactPaths([]string{path}); err == nil {
		t.Fatal("late raw artifact creation was accepted")
	}
}

func TestZeroFindingsCompletesAfterOneNativeCall(t *testing.T) {
	session, _ := nativeFixture(t)
	executable, countPath := fakeCodexExecutable(t, `{"findings":[]}`)

	outcome, err := runNativeSession(context.Background(), nativeRunOptions{
		Session: session, Goal: "review the change", ReasoningEffort: "high", Provider: NewCodexProvider(executable),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultPass || result.Execution.ProviderInvocations != 1 {
		t.Fatalf("result = %#v", result)
	}
	if count := readInvocationCount(t, countPath); count != "1" {
		t.Fatalf("provider invocations = %s", count)
	}
}

func TestWhitespaceOnlyOutputIsError(t *testing.T) {
	session, _ := nativeFixture(t)
	executable, _ := fakeCodexExecutable(t, " \r\n\t\n")

	outcome, err := runNativeSession(context.Background(), nativeRunOptions{Session: session, Provider: NewCodexProvider(executable)})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultError || len(result.Findings) != 0 ||
		result.Execution.ProviderInvocations != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestStructuredNativeReviewOutputBlocksRelease(t *testing.T) {
	session, _ := nativeFixture(t)
	executable, countPath := fakeCodexExecutable(t, nativeFindingOutput("app.go", "timeout is ignored", "The new call can wait forever.", 1))

	outcome, err := runNativeSession(context.Background(), nativeRunOptions{
		Session: session, Goal: "review the change", ReasoningEffort: "high", Provider: NewCodexProvider(executable),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultBlock || len(result.Findings) != 1 ||
		len(result.Execution.AdapterDrops) != 0 || result.Execution.ProviderInvocations != 1 {
		t.Fatalf("result = %#v", result)
	}
	if count := readInvocationCount(t, countPath); count != "1" {
		t.Fatalf("provider invocations = %s", count)
	}
}

func TestReviewOutputOutsideChangedFilesFailsClosed(t *testing.T) {
	session, _ := nativeFixture(t)
	executable, _ := fakeCodexExecutable(t,
		nativeFindingOutput("/tmp/outside.go", "wrong result", "The new branch returns the opposite value.", 1))

	outcome, err := runNativeSession(context.Background(), nativeRunOptions{
		Session: session, Goal: "review the change", ReasoningEffort: "high", Provider: NewCodexProvider(executable),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := outcome.Result()
	if result.Adjudication.SemanticResult != quality.ResultError || result.Execution.ProviderInvocations != 1 ||
		len(result.Findings) != 0 || len(result.Execution.AdapterDrops) != 0 {
		t.Fatalf("document-level result = %#v", result)
	}
}

func nativeFixture(t *testing.T) (reviewsession.NativeSession, quality.ReviewRequest) {
	t.Helper()
	repository := t.TempDir()
	initializeGitRepository(t, repository)
	runFixtureGit(t, repository, "config", "user.email", "fixture@example.invalid")
	runFixtureGit(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "add", "app.go")
	runFixtureGit(t, repository, "commit", "-qm", "base")
	base := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\nfunc Run() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "add", "app.go")
	runFixtureGit(t, repository, "commit", "-qm", "target")
	target := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	request := quality.ReviewRequest{
		Repository: "example/repo", TargetBranch: "main",
		BaseCommit: base, TargetCommit: target,
		DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	session, err := reviewsession.PrepareNative(context.Background(), reviewsession.Options{
		RepositoryRoot: repository, OutputRoot: filepath.Join(t.TempDir(), "sessions"),
		Host: "codex", Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Cleanup() })
	return session, request
}

func initializeGitRepository(t *testing.T, repository string) {
	t.Helper()
	output, err := exec.Command("git", "-C", repository, "init", "-q").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
}

func runFixtureGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repository}, arguments...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func captureFixture(t *testing.T) capturePaths {
	t.Helper()
	output := filepath.Join(t.TempDir(), "output")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	return capturePaths{
		finalMessage:   filepath.Join(output, "native-review.txt"),
		jsonl:          filepath.Join(output, "native-review.stdout.log"),
		stderr:         filepath.Join(output, "native-review.stderr.log"),
		freezeManifest: filepath.Join(output, "native-review-freeze.json"),
		metrics:        filepath.Join(output, "native-run-metrics.json"),
	}
}

func fakeCodexExecutable(t *testing.T, finalMessage string) (string, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "codex")
	finalPath := path + ".final"
	countPath := path + ".count"
	if err := os.WriteFile(finalPath, []byte(finalMessage), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
output=''
previous=''
for argument in "$@"; do
  if [ "$previous" = '--output-last-message' ]; then output="$argument"; fi
  previous="$argument"
done
test -n "$output"
cat >/dev/null
count=0
if [ -f "$0.count" ]; then count=$(cat "$0.count"); fi
count=$((count + 1))
printf '%s\n' "$count" > "$0.count"
cat "$0.final" > "$output"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":123,"output_tokens":45}}'
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, countPath
}

func readInvocationCount(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func nativeFindingOutput(path, title, body string, priority int) string {
	return fmt.Sprintf(`{"findings":[{"priority":%d,"title":%q,"code_location":{"path":%q,"start_line":2,"end_line":2},"reason":%q,"suggestion":"Apply the smallest safe fix."}]}`, priority, title, path, body)
}
