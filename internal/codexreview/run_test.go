package codexreview

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
	prepared, request := nativeFixture(t)
	options := Options{
		Prepared: prepared, Request: request, Goal: "protect settlement correctness",
	}
	if err := normalizeOptions(&options); err != nil {
		t.Fatal(err)
	}
	invocation := buildReviewInvocation(options)
	want := []string{
		"exec", "--sandbox", "workspace-write",
		"--config", "sandbox_workspace_write.network_access=true",
		"--model", "gpt-5.6-sol", "--config", `model_reasoning_effort="max"`,
		"--json", "--output-last-message", prepared.NativeReviewPath, "-",
	}
	if strings.Join(invocation.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("full Codex args = %#v, want %#v", invocation.Args, want)
	}
	for _, forbidden := range []string{"review", "--ignore-user-config", "--ignore-rules", "--ephemeral", "read-only", "--output-schema"} {
		for _, argument := range invocation.Args {
			if argument == forbidden {
				t.Fatalf("full Codex invocation contains %q: %#v", forbidden, invocation.Args)
			}
		}
	}
	wantPrompt := fmt.Sprintf(
		"Review the changes introduced by %s relative to %s for actionable defects.\nUser-supplied context: %q\n",
		request.TargetCommit, request.BaseCommit, "protect settlement correctness",
	)
	if invocation.Stdin != wantPrompt {
		t.Fatalf("prompt = %q, want %q", invocation.Stdin, wantPrompt)
	}
	if options.Model != "gpt-5.6-sol" || options.ReasoningEffort != "max" {
		t.Fatalf("defaults = model %q, effort %q", options.Model, options.ReasoningEffort)
	}
}

func TestDiscoveryChildMarkerLifecycle(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "input", "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath, err := installDiscoveryChildMarker(repository)
	if err != nil {
		t.Fatal(err)
	}
	if nested, err := IsDiscoveryChildRepository(repository); err != nil || !nested {
		t.Fatalf("nested = %t, error = %v", nested, err)
	}
	if info, err := os.Lstat(markerPath); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
		t.Fatalf("marker mode = %v, error = %v", info, err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if nested, err := IsDiscoveryChildRepository(repository); err != nil || nested {
		t.Fatalf("nested after cleanup = %t, error = %v", nested, err)
	}
}

func TestDiscoveryChildMarkerDoesNotOverwriteExistingFile(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "input", "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := discoveryChildMarkerPath(repository)
	if err := os.WriteFile(markerPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installDiscoveryChildMarker(repository); err == nil {
		t.Fatal("existing discovery marker was overwritten")
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil || string(raw) != "sentinel" {
		t.Fatalf("marker = %q, error = %v", raw, err)
	}
}

func TestDiscoveryChildWorkingDirectoryUsesCanonicalGitRoot(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "session", "repository")
	workingDirectory := filepath.Join(repository, "nested")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	initializeGitRepository(t, repository)
	markerPath, err := installDiscoveryChildMarker(repository)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(markerPath) })
	alias := filepath.Join(t.TempDir(), "linked-repository")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}

	nested, err := IsDiscoveryChildWorkingDirectory(filepath.Join(alias, "nested"))
	if err != nil || !nested {
		t.Fatalf("nested = %t, error = %v", nested, err)
	}
}

func TestDiscoveryChildWorkingDirectoryIgnoresRepositoryOwnedMarker(t *testing.T) {
	repository := t.TempDir()
	workingDirectory := filepath.Join(repository, "nested")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	initializeGitRepository(t, repository)
	if err := os.WriteFile(filepath.Join(repository, DiscoveryChildMarkerName), []byte(discoveryChildMarkerContents), 0o400); err != nil {
		t.Fatal(err)
	}

	nested, err := IsDiscoveryChildWorkingDirectory(workingDirectory)
	if err != nil || nested {
		t.Fatalf("nested = %t, error = %v", nested, err)
	}
}

func TestProcessExecutorRemovesDiscoveryMarkerAfterFailure(t *testing.T) {
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
	err = (ProcessExecutor{}).Run(context.Background(), Invocation{
		Executable: executable,
		Args:       []string{"-c", "exit 7"},
		Dir:        repository,
		StdoutPath: filepath.Join(output, "stdout"),
		StderrPath: filepath.Join(output, "stderr"),
	})
	if err == nil {
		t.Fatal("failing child process unexpectedly succeeded")
	}
	if _, err := os.Lstat(discoveryChildMarkerPath(repository)); !os.IsNotExist(err) {
		t.Fatalf("discovery child marker remains after failure: %v", err)
	}
}

func TestProcessExecutorPropagatesDiscoveryIdentity(t *testing.T) {
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
	stdoutPath := filepath.Join(output, "stdout")
	err = (ProcessExecutor{}).Run(context.Background(), Invocation{
		Executable: executable,
		Args:       []string{"-c", `printf '%s' "$CODE_QUALITY_NATIVE_DISCOVERY_MARKER"`},
		Dir:        repository,
		StdoutPath: stdoutPath,
		StderrPath: filepath.Join(output, "stderr"),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != discoveryChildMarkerPath(repository) {
		t.Fatalf("inherited discovery marker = %q, want %q", raw, discoveryChildMarkerPath(repository))
	}
}

func TestProcessExecutorIdentitySurvivesFilteredEnvironment(t *testing.T) {
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
	t.Setenv("CODE_QUALITY_PROCESS_EXECUTOR_HELPER", "filtered-discovery")
	stdoutPath := filepath.Join(output, "stdout")
	err = (ProcessExecutor{}).Run(context.Background(), Invocation{
		Executable: executable,
		Args: []string{
			"-c", `unset CODE_QUALITY_NATIVE_DISCOVERY_MARKER; exec "$1" -test.run=^TestProcessExecutorDescendantWriterHelper$`,
			"sh", os.Args[0],
		},
		Dir:        repository,
		StdoutPath: stdoutPath,
		StderrPath: filepath.Join(output, "stderr"),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "filtered discovery child detected\n" {
		t.Fatalf("filtered discovery output = %q", raw)
	}
}

func TestProcessExecutorQuiescesDescendantWriters(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "input", "repository")
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODE_QUALITY_PROCESS_EXECUTOR_HELPER", "parent")
	stdoutPath := filepath.Join(output, "stdout")
	err := (ProcessExecutor{}).Run(context.Background(), Invocation{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestProcessExecutorDescendantWriterHelper$"},
		Dir:        repository,
		StdoutPath: stdoutPath,
		StderrPath: filepath.Join(output, "stderr"),
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

func TestProcessExecutorDescendantWriterHelper(t *testing.T) {
	switch os.Getenv("CODE_QUALITY_PROCESS_EXECUTOR_HELPER") {
	case "":
		return
	case "parent":
		command := exec.Command(os.Args[0], "-test.run=^TestProcessExecutorDescendantWriterHelper$")
		command.Env = append(os.Environ(), "CODE_QUALITY_PROCESS_EXECUTOR_HELPER=descendant")
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
	case "filtered-discovery":
		if _, exists := os.LookupEnv(DiscoveryChildMarkerEnvironment); exists {
			t.Fatal("discovery marker environment was not filtered")
		}
		nested, err := IsDiscoveryChildProcess()
		if err != nil || !nested {
			t.Fatalf("nested = %t, error = %v", nested, err)
		}
		fmt.Fprintln(os.Stdout, "filtered discovery child detected")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestRunFreezesRawArtifactsBeforeClassification(t *testing.T) {
	prepared, request := nativeFixture(t)
	raw := agentFindingOutput(filepath.Join(prepared.RepositoryDir, "app.go"), "preserve the result", "The changed branch returns the wrong value.", 1)
	executor := &scriptedExecutor{outputs: []string{raw}}

	result, err := Run(context.Background(), Options{Prepared: prepared, Request: request, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultManualReview || len(result.Findings) != 0 ||
		len(result.Execution.AdapterDrops) != 0 {
		t.Fatalf("result = %#v", result)
	}
	layout := reviewsession.NewLayout(prepared.SessionDir)
	var manifest struct {
		SchemaVersion int `json:"schema_version"`
		Artifacts     []struct {
			Name    string `json:"name"`
			Present bool   `json:"present"`
			Bytes   int    `json:"bytes"`
			SHA256  string `json:"sha256"`
		} `json:"artifacts"`
	}
	encoded, err := os.ReadFile(layout.NativeFreezePath)
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
	info, err := os.Stat(prepared.NativeReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("raw review remains writable: %o", info.Mode().Perm())
	}
	if err := os.WriteFile(prepared.NativeReviewPath, []byte("changed"), 0o600); err == nil {
		t.Fatal("frozen raw review was overwritten")
	}
}

func TestFreezeAndUsageStreamCompleteLargeJSONL(t *testing.T) {
	layout := reviewsession.NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.NativeReviewPath, []byte("No findings.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fillerLine := "{\"type\":\"item.completed\"}\n"
	filler := strings.Repeat(fillerLine, int(maxNativeOutputBytes)/len(fillerLine)+2)
	usageLine := "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":321,\"output_tokens\":54}}\n"
	if err := os.WriteFile(layout.NativeStdoutPath, []byte(filler+usageLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.NativeStderrPath, []byte("warning\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	frozen, err := freezeNativeArtifacts(layout)
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
	layout := reviewsession.NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("original evidence\n")
	if err := os.WriteFile(layout.NativeReviewPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(layout.NativeReviewPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	frozen, err := freezeNativeArtifacts(layout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt([]byte("mutated evidence!\n"), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(layout.NativeReviewPath)
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
	prepared, request := nativeFixture(t)
	layout := reviewsession.NewLayout(prepared.SessionDir)
	if err := os.WriteFile(layout.NativeReviewPath, []byte("No findings.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := `{"type":"turn.completed","usage":{"input_tokens":30,"output_tokens":7}}` + "\n"
	if err := os.WriteFile(layout.NativeStdoutPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.NativeStderrPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	frozen, err := freezeNativeArtifacts(layout)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(layout.OutputDir, "replacement-stdout")
	other := `{"type":"turn.completed","usage":{"input_tokens":999,"output_tokens":888}}` + "\n"
	if err := os.WriteFile(replacement, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, layout.NativeStdoutPath); err != nil {
		t.Fatal(err)
	}

	metrics := collectRunMetrics(Options{Prepared: prepared, Request: request}, 25*time.Millisecond, 42, frozen)
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
	prepared, request := nativeFixture(t)
	layout := reviewsession.NewLayout(prepared.SessionDir)
	stdout := layout.NativeStdoutPath
	contents := `{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}` + "\n"
	if err := os.WriteFile(stdout, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	frozen, err := freezeNativeArtifacts(layout)
	if err != nil {
		t.Fatal(err)
	}
	metrics := collectRunMetrics(Options{Prepared: prepared, Request: request}, 25*time.Millisecond, 42, frozen)
	if metrics.UsageAvailable || metrics.InputTokens != nil || metrics.OutputTokens != nil ||
		!strings.Contains(metrics.UsageError, "zero token usage") {
		t.Fatalf("metrics = %#v", metrics)
	}
}

func TestNativeReviewPromptOmitsUnsuppliedGoal(t *testing.T) {
	prepared, request := nativeFixture(t)
	options := Options{Prepared: prepared, Request: request}
	if err := normalizeOptions(&options); err != nil {
		t.Fatal(err)
	}
	if options.Goal != "" {
		t.Fatalf("wrapper invented goal %q", options.Goal)
	}
	want := fmt.Sprintf("Review the changes introduced by %s relative to %s for actionable defects.\n", request.TargetCommit, request.BaseCommit)
	if prompt := buildReviewPrompt(options); prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestNativeDocumentClassifierOnlyPassesExactNoFindingsSentinels(t *testing.T) {
	for _, input := range []string{
		"No findings.\n",
		"No actionable findings.\r\n",
		"  No actionable defects found.  \n",
	} {
		if !isExplicitNoFindingsDocument(input) {
			t.Fatalf("exact no-findings document was not accepted: %q", input)
		}
	}

	for _, input := range []string{
		"",
		"## Findings\n\nNo findings.\n",
		"No findings.\n\nHowever, the new branch leaks data.\n",
		"The change looks correct.\n",
		"- [P1] Preserve cancellation — [client.go:17](/repo/client.go:17)\n",
	} {
		if isExplicitNoFindingsDocument(input) {
			t.Fatalf("non-sentinel document was accepted as PASS: %q", input)
		}
	}
}

func TestFreezeManifestDoesNotOverwriteExistingEvidence(t *testing.T) {
	prepared, _ := nativeFixture(t)
	layout := reviewsession.NewLayout(prepared.SessionDir)
	for _, artifact := range []string{layout.NativeReviewPath, layout.NativeStdoutPath, layout.NativeStderrPath} {
		if err := os.WriteFile(artifact, []byte("raw"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(layout.NativeFreezePath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := freezeNativeArtifacts(layout); err == nil {
		t.Fatal("existing freeze manifest was overwritten")
	}
	raw, err := os.ReadFile(layout.NativeFreezePath)
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
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{"No actionable defects found.\n"}}

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

func TestWhitespaceOnlyOutputIsIncomplete(t *testing.T) {
	prepared, request := nativeFixture(t)
	executor := &scriptedExecutor{outputs: []string{" \r\n\t\n"}}

	result, err := Run(context.Background(), Options{Prepared: prepared, Request: request, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultIncomplete || len(result.Findings) != 0 ||
		result.Execution.ModelCalls != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestNativeReviewOutputRequiresDocumentLevelManualReview(t *testing.T) {
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
	if result.Adjudication.SemanticResult != quality.ResultManualReview || len(result.Findings) != 0 ||
		len(result.Execution.AdapterDrops) != 0 || result.Execution.ModelCalls != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(executor.invocations) != 1 {
		t.Fatalf("model calls = %d", len(executor.invocations))
	}
}

func TestReviewOutputLocationDoesNotChangeDocumentClassification(t *testing.T) {
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
	if result.Adjudication.SemanticResult != quality.ResultManualReview || result.Execution.ModelCalls != 1 ||
		len(result.Findings) != 0 || len(result.Execution.AdapterDrops) != 0 {
		t.Fatalf("document-level result = %#v", result)
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
	if err := os.WriteFile(invocation.StdoutPath, []byte(`{"type":"turn.completed","usage":{"input_tokens":123,"output_tokens":45}}`+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(invocation.StderrPath, nil, 0o600); err != nil {
		return err
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

func initializeGitRepository(t *testing.T, repository string) {
	t.Helper()
	output, err := exec.Command("git", "-C", repository, "init", "-q").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
}

func nativeFindingOutput(path, title, body string, priority int) string {
	return fmt.Sprintf("Review comment:\n\n- [P%d] %s — %s:2-2\n  %s\n", priority, title, path, body)
}

func agentFindingOutput(path, title, body string, priority int) string {
	return fmt.Sprintf("## Findings\n\n- [P%d] %s — [app.go:2](%s:2)\n\n  %s\n", priority, title, path, body)
}
