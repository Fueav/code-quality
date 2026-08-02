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
	"runtime"
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
	if result.Adjudication.SemanticResult != quality.ResultManualReview || len(result.Findings) != 1 {
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

func TestAdaptFindingsRejectsParentTraversalBeforeSymlinkResolution(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nested"), filepath.Join(repository, "escape")); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "reject escaped traversal", Body: "The location crosses the symlink boundary before returning to app.go.", Priority: 1,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: repository + string(filepath.Separator) + "escape" + string(filepath.Separator) + ".." + string(filepath.Separator) + "app.go",
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"app.go"})
	if len(findings) != 0 || len(drops) != 1 || drops[0].Reason != "code location contains parent traversal" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsPreservesChangedSymlinkPath(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "versions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "versions", "v2"), []byte("enabled=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logicalPath := filepath.Join(repository, "config", "current")
	if err := os.Symlink(filepath.Join("..", "versions", "v2"), logicalPath); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve active configuration", Body: "The changed link selects the wrong version.", Priority: 2,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: logicalPath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"config/current"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "config/current" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsFallsBackFromUnchangedAliasToChangedTarget(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "versions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "versions", "v2"), []byte("enabled=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(repository, "config", "current")
	if err := os.Symlink(filepath.Join("..", "versions", "v2"), aliasPath); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve active configuration", Body: "The changed target enables the wrong behavior.", Priority: 2,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: aliasPath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"versions/v2"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "versions/v2" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsResolvesDanglingAliasToDeletedChangedTarget(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "versions"), 0o700); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(repository, "config", "current")
	if err := os.Symlink(filepath.Join("..", "versions", "v2"), aliasPath); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve deleted target handling", Body: "The unchanged alias still exposes the deleted target.", Priority: 2,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: aliasPath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"versions/v2"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "versions/v2" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsRejectsChangedDanglingSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(filepath.Join(repository, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(repository, "config", "current")
	if err := os.Symlink(filepath.Join(root, "outside", "missing"), aliasPath); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "reject escaped target", Body: "The changed link points outside the checkout.", Priority: 1,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: aliasPath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"config/current"})
	if len(findings) != 0 || len(drops) != 1 || drops[0].Reason != "code location is outside the isolated checkout" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsRejectsDanglingSymlinkTargetTraversalEscape(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	config := filepath.Join(repository, "config")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nested"), filepath.Join(config, "pivot")); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(config, "current")
	target := "pivot" + string(filepath.Separator) + ".." + string(filepath.Separator) + "missing"
	if err := os.Symlink(target, aliasPath); err != nil {
		t.Fatal(err)
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "reject escaped dangling target", Body: "The chained target resolves outside the checkout before reaching its missing leaf.", Priority: 1,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: aliasPath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"config/current"})
	if len(findings) != 0 || len(drops) != 1 || drops[0].Reason != "code location is outside the isolated checkout" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsRejectsTraversalAfterUnresolvableComponent(t *testing.T) {
	for _, test := range []struct {
		name      string
		makePivot func(string) error
	}{
		{name: "missing component", makePivot: func(string) error { return nil }},
		{name: "non-directory component", makePivot: func(path string) error {
			return os.WriteFile(path, []byte("not a directory\n"), 0o600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			config := filepath.Join(repository, "config")
			if err := os.MkdirAll(config, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "changed.go"), []byte("package config\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := test.makePivot(filepath.Join(config, "pivot")); err != nil {
				t.Fatal(err)
			}
			aliasPath := filepath.Join(config, "current")
			target := "pivot" + string(filepath.Separator) + ".." + string(filepath.Separator) + "changed.go"
			if err := os.Symlink(target, aliasPath); err != nil {
				t.Fatal(err)
			}

			findings, drops := adaptFindings([]nativeFinding{{
				Title: "reject an unresolvable alias", Body: "The filesystem cannot traverse this alias to the changed file.", Priority: 2,
				CodeLocation: nativeCodeLocation{
					AbsoluteFilePath: aliasPath,
					LineRange:        nativeLineRange{Start: 1, End: 1},
				},
			}}, repository, []string{"config/changed.go"})
			if len(findings) != 0 || len(drops) != 1 || drops[0].Reason != "code location cannot be canonicalized" {
				t.Fatalf("findings = %#v, drops = %#v", findings, drops)
			}
		})
	}
}

func TestAdaptFindingsPreservesCaseInsensitivePathIdentity(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "RepositoryCase")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	changedPath := filepath.Join(repository, "ChangedFile.go")
	if err := os.WriteFile(changedPath, []byte("package repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(root, "repositorycase", "changedfile.go")
	actualInfo, err := os.Lstat(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateInfo, err := os.Lstat(candidatePath)
	if err != nil || !os.SameFile(actualInfo, candidateInfo) {
		t.Skip("test volume is case-sensitive")
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve filesystem identity", Body: "The model used different casing for the same changed file.", Priority: 2,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: candidatePath,
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"ChangedFile.go"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "ChangedFile.go" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestAdaptFindingsPreservesDeletedCaseInsensitivePathIdentity(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "RepositoryCase")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRepository := filepath.Join(root, "repositorycase")
	actualInfo, err := os.Lstat(repository)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Lstat(aliasRepository)
	if err != nil || !os.SameFile(actualInfo, aliasInfo) {
		t.Skip("test volume is case-sensitive")
	}

	findings, drops := adaptFindings([]nativeFinding{{
		Title: "preserve deleted filesystem identity", Body: "The model used different casing for the deleted changed file.", Priority: 2,
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: filepath.Join(aliasRepository, "deletedfile.go"),
			LineRange:        nativeLineRange{Start: 1, End: 1},
		},
	}}, repository, []string{"DeletedFile.go"})
	if len(findings) != 1 || len(drops) != 0 || findings[0].CodeLocation.Path != "DeletedFile.go" {
		t.Fatalf("findings = %#v, drops = %#v", findings, drops)
	}
}

func TestCaseSensitivityProbeStopsAtMountBoundary(t *testing.T) {
	mountPath := filepath.Join(string(filepath.Separator), "dev")
	mountInfo, err := os.Lstat(mountPath)
	if err != nil {
		t.Skip("/dev is unavailable")
	}
	alternateMountInfo, err := os.Lstat(filepath.Join(string(filepath.Separator), "Dev"))
	if err != nil || !os.SameFile(mountInfo, alternateMountInfo) {
		t.Skip("parent filesystem does not expose a case-insensitive /dev lookup")
	}
	if _, err := os.Lstat(filepath.Join(mountPath, "fd")); err != nil {
		t.Skip("/dev/fd is unavailable")
	}
	if _, err := os.Lstat(filepath.Join(mountPath, "Fd")); !errors.Is(err, os.ErrNotExist) {
		t.Skip("mounted /dev filesystem is not observably case-sensitive")
	}
	if pathUsesCaseInsensitiveIdentity(mountPath) {
		t.Fatal("case-sensitive mounted filesystem inherited its parent's case semantics")
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

func TestAgentOutputParsesObservedMarkdownFormats(t *testing.T) {
	for name, input := range map[string]string{
		"findings heading": `## Findings

- [P2] Guard the pagination end calculation against overflow — [filtered_pagination.go:79](/private/tmp/review/types/query/filtered_pagination.go:79)

  Offset and Limit are user-controlled, so their sum can wrap.
`,
		"numbered bold": `Findings, ordered by severity:

1. **[P1] Refuse to overwrite existing drafts** — [prompt.go:292](/private/tmp/review/x/gov/client/cli/prompt.go:292)

   The command silently truncates previously edited drafts.
`,
		"inline bold": `Found one actionable defect.

- **[P2] Reject invalid integer prompt input** — [prompt.go:97](/private/tmp/review/x/gov/client/cli/prompt.go:97). Invalid input is silently replaced with zero.
`,
		"standalone localized list": `- [P1] 保留调用方上下文 — [client.go:17](/private/tmp/review/client.go:17)

  新分支丢失了调用方的取消信号。
`,
		"angle bracket destination": `## Findings

- [P2] Preserve paths containing spaces — [client.go:12](</private/tmp/My Repo/client.go:12>)

  The current parser rejects the closing angle bracket.
`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || result.Findings[0].Priority < 1 || result.Findings[0].Body == "" {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestAgentOutputAcceptsAllCommonMarkListMarkers(t *testing.T) {
	for name, marker := range map[string]string{"plus bullet": "+", "parenthesized order": "1)"} {
		t.Run(name, func(t *testing.T) {
			input := marker + " [P2] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)\n\n" +
				"  The new branch drops caller cancellation.\n"
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

func TestAgentOutputAcceptsCommonMarkTopLevelIndentation(t *testing.T) {
	for indentation := 1; indentation <= 3; indentation++ {
		t.Run(fmt.Sprintf("%d spaces", indentation), func(t *testing.T) {
			prefix := strings.Repeat(" ", indentation)
			input := prefix + "- [P2] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)\n\n" +
				prefix + "  The new branch drops caller cancellation.\n"
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || result.Findings[0].Body != "The new branch drops caller cancellation." {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestNativeOutputAcceptsCommonMarkTopLevelIndentation(t *testing.T) {
	for indentation := 1; indentation <= 3; indentation++ {
		t.Run(fmt.Sprintf("%d spaces", indentation), func(t *testing.T) {
			prefix := strings.Repeat(" ", indentation)
			input := prefix + "Review comment:\n\n" +
				prefix + "- [P1] Preserve cancellation — /private/tmp/review/client.go:17\n" +
				prefix + "  The new branch drops caller cancellation.\n"
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || result.Findings[0].Body != "The new branch drops caller cancellation." {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestIndentedAgentPriorityBulletRemainsBodyText(t *testing.T) {
	input := `- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)

  The new branch drops caller cancellation.
  - [P2] Quoted counterexample — [other.go:8](/private/tmp/review/other.go:8)
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Body, "Quoted counterexample") {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestIndentedNativePriorityBulletRemainsBodyText(t *testing.T) {
	input := `Review comment:

- [P1] Preserve cancellation — /private/tmp/review/client.go:17
  The new branch drops caller cancellation.
  - [P2] Quoted counterexample — /private/tmp/review/other.go:8
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Body, "Quoted counterexample") {
		t.Fatalf("findings = %#v", result.Findings)
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
	if err := os.WriteFile(path, []byte("No findings.\n"), 0o600); err != nil {
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

func TestNativeOutputRejectsNoFindingsWithTrailingProse(t *testing.T) {
	input := "No actionable defects found.\n\nHowever, this branch leaks credentials.\n"
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("unparsed prose after no-findings sentinel was accepted as PASS evidence")
	}
}

func TestNativeOutputAcceptsHeadedNoFindings(t *testing.T) {
	for _, heading := range []string{"## Findings", "Review findings:", "Review comments:"} {
		t.Run(heading, func(t *testing.T) {
			result, err := parseNativeReviewText(heading + "\n\nNo findings.\n")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 0 {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestNativeOutputAcceptsHeadedNoFindingsAfterIntroduction(t *testing.T) {
	input := "Review complete.\n\n## Findings\n\nNo findings.\n"
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestNativeOutputRejectsFencedContentBeforeHeadedNoFindings(t *testing.T) {
	input := "## Findings\n\n```text\nassessment\n```\n\nNo findings.\n"
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("fenced assessment before a no-findings sentinel was skipped")
	}
}

func TestNativeOutputAcceptsCommonMarkIndentedNoFindings(t *testing.T) {
	for indentation := 1; indentation <= 3; indentation++ {
		t.Run(fmt.Sprintf("%d spaces", indentation), func(t *testing.T) {
			prefix := strings.Repeat(" ", indentation)
			result, err := parseNativeReviewText(prefix + "## Findings\n\n" + prefix + "No findings.\n")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 0 {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestNativeOutputRejectsIndentedNoFindingsExamples(t *testing.T) {
	for name, input := range map[string]string{
		"direct indented sentinel": "    No findings.\n",
		"indented sentinel":        "Example:\n\n    No findings.\n",
		"indented heading":         "Example:\n\n    ## Findings\nNo findings.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNativeReviewText(input); err == nil {
				t.Fatal("indented example was accepted as a top-level no-findings result")
			}
		})
	}
}

func TestNativeOutputRejectsHeadedNoFindingsWithTrailingProse(t *testing.T) {
	input := "## Findings\n\nNo findings.\n\nHowever, this branch leaks credentials.\n"
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("unparsed prose after headed no-findings sentinel was accepted as PASS evidence")
	}
}

func TestNativeOutputRejectsCandidatesBeforeHeadedNoFindings(t *testing.T) {
	input := `- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)

  The new branch drops caller cancellation.

## Findings

No findings.
`
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("later no-findings section erased an earlier candidate")
	}
}

func TestNativeHeadingFallsBackToAgentFindingGrammar(t *testing.T) {
	input := `Review comments:

- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)

  The new branch drops the caller's cancellation signal.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Preserve cancellation" ||
		result.Findings[0].CodeLocation.AbsoluteFilePath != "/private/tmp/review/client.go" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestNativeOutputRejectsMixedFindingGrammars(t *testing.T) {
	input := `Review comments:

- [P1] Preserve cancellation — /private/tmp/review/client.go:17
  The new branch drops caller cancellation.
* [P2] Close the response body — [client.go:31](/private/tmp/review/client.go:31)
  The success path leaks the response body.
`
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("mixed finding grammars returned a partial finding set")
	}
}

func TestNativeOutputRejectsCandidateBeforeSelectedHeading(t *testing.T) {
	input := `- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)
  The new branch drops caller cancellation.

Review comments:

- [P2] Close the response body — /private/tmp/review/client.go:31
  The success path leaks the response body.
`
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("candidate before the selected native heading was dropped")
	}
}

func TestAgentOutputIgnoresFencedFindingExamples(t *testing.T) {
	input := "Example output:\n\n```markdown\n" +
		"- [P1] Example only — [example.go:4](/private/tmp/review/example.go:4)\n" +
		"  This is not an actual finding.\n```\n\n" +
		"- [P2] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)\n\n" +
		"  The new branch drops caller cancellation.\n"
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Preserve cancellation" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestTopLevelFenceDoesNotAcceptRelativeClosingIndent(t *testing.T) {
	input := "   ```markdown\n" +
		"- [P1] Example only — [example.go:4](/private/tmp/review/example.go:4)\n" +
		"  This is not an actual finding.\n" +
		"      ```\n" +
		"- [P2] Still fenced — [client.go:17](/private/tmp/review/client.go:17)\n" +
		"  This also remains example content.\n"
	if result, err := parseNativeReviewText(input); err == nil {
		t.Fatalf("six-space close escaped a top-level fence: %#v", result.Findings)
	}
}

func TestListFenceDoesNotAcceptTopLevelClosingIndent(t *testing.T) {
	input := "- [P1] Preserve the real guard — [client.go:17](/private/tmp/review/client.go:17)\n" +
		"  ```markdown\n" +
		"  example body\n" +
		"```\n" +
		"- [P2] Example only — [example.go:4](/private/tmp/review/example.go:4)\n" +
		"  This remains top-level fenced content.\n"
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Preserve the real guard" {
		t.Fatalf("zero-indent close escaped a list-contained fence: %#v", result.Findings)
	}
}

func TestFencedBodyClosingIndentIsRelativeToList(t *testing.T) {
	input := "- [P1] Preserve the first guard — [client.go:17](/private/tmp/review/client.go:17)\n" +
		"  ```go\n    allowed := true\n    ```\n" +
		"- [P2] Preserve the second guard — [client.go:31](/private/tmp/review/client.go:31)\n" +
		"  The second branch drops its guard.\n"
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 2 || result.Findings[1].Title != "Preserve the second guard" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestFindingBodiesPreserveIndentedFencedBlocks(t *testing.T) {
	for name, input := range map[string]string{
		"agent": "- [P2] Preserve the guard — [client.go:17](/private/tmp/review/client.go:17)\n" +
			"  ```go\n  if allowed { return }\n  ```\n",
		"native": "Review comment:\n\n- [P2] Preserve the guard — /private/tmp/review/client.go:17\n" +
			"  ```go\n  if allowed { return }\n  ```\n",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Body, "if allowed") {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestFindingBodiesDoNotResumeAfterTrailingAssessment(t *testing.T) {
	for name, input := range map[string]string{
		"agent": "- [P2] Preserve the guard — [client.go:17](/private/tmp/review/client.go:17)\n" +
			"  The branch drops its guard.\nOverall assessment: needs repair.\n" +
			"  ```text\n  trailing appendix\n  ```\n",
		"native": "Review comment:\n\n- [P2] Preserve the guard — /private/tmp/review/client.go:17\n" +
			"  The branch drops its guard.\nOverall assessment: needs repair.\n" +
			"  ```text\n  trailing appendix\n  ```\n",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := parseNativeReviewText(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Findings) != 1 || strings.Contains(result.Findings[0].Body, "trailing appendix") {
				t.Fatalf("findings = %#v", result.Findings)
			}
		})
	}
}

func TestAgentFindingAcceptsParenthesesInAngleBracketDestination(t *testing.T) {
	input := `## Findings

- [P2] Preserve copied checkout paths — [client.go:12](</tmp/My Repo (copy)/client.go:12>)

  The parser must retain findings from valid angle-bracket Markdown destinations.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].CodeLocation.AbsoluteFilePath != "/tmp/My Repo (copy)/client.go" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingAcceptsEscapedClosingAngleInDestination(t *testing.T) {
	input := `## Findings

- [P2] Preserve escaped angle paths — [a>b.go:9](</repo/a\>b.go:9>)

  The parser must retain a legal escaped closing angle in an angle-bracket destination.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].CodeLocation.AbsoluteFilePath != "/repo/a>b.go" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingAcceptsBalancedParenthesesInOrdinaryDestination(t *testing.T) {
	input := `## Findings

- [P2] Preserve route-group findings — [page.tsx](/repo/app/(auth)/page.tsx:42)

  The parser must retain findings for ordinary Markdown links with balanced parentheses.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].CodeLocation.AbsoluteFilePath != "/repo/app/(auth)/page.tsx" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingDecodesEscapedMarkdownDestination(t *testing.T) {
	input := `## Findings

- [P2] Preserve escaped route groups — [page.tsx](/repo/app/\(auth\)/page.tsx:42)

  The parser must map the escaped CommonMark destination to the changed path.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].CodeLocation.AbsoluteFilePath != "/repo/app/(auth)/page.tsx" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingAcceptsLinkedTitleLocation(t *testing.T) {
	input := `Found one actionable defect.

- [P1] [Bind freeze operations to the verified inode](/private/tmp/review/freeze.go:72)
  The path can be replaced after hashing.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Bind freeze operations to the verified inode" ||
		result.Findings[0].CodeLocation.AbsoluteFilePath != "/private/tmp/review/freeze.go" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingAcceptsWholeFindingBoldEmphasis(t *testing.T) {
	input := `## Findings

- **[P1] Preserve cancellation — [app.go:12](/repo/app.go:12)**

  The new branch drops caller cancellation.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Title != "Preserve cancellation" ||
		result.Findings[0].CodeLocation.AbsoluteFilePath != "/repo/app.go" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingStopsBeforeTrailingTopLevelSection(t *testing.T) {
	input := `## Findings

- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)

  The new branch drops caller cancellation.

## Verification

All focused tests pass.
`
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].Body != "The new branch drops caller cancellation." {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestAgentFindingRejectsTrailingNoFindingsContradiction(t *testing.T) {
	input := `## Findings

- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)

  The new branch drops caller cancellation.

No findings.
`
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("trailing no-findings contradiction was accepted as finding body")
	}
}

func TestAgentFindingAllowsPriorityMarkerInsideIndentedBody(t *testing.T) {
	input := "- **[P1] Preserve earlier findings** — [run.go:411](/private/tmp/review/run.go:411)\n\n" +
		"  Output containing a structured `[P1]` candidate must not be erased.\n"
	result, err := parseNativeReviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || !strings.Contains(result.Findings[0].Body, "`[P1]`") {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestNativeFindingRejectsTrailingNoFindingsContradiction(t *testing.T) {
	input := `Review comment:

- [P1] Preserve cancellation — /private/tmp/review/client.go:17
  The new branch drops caller cancellation.

No findings.
`
	if _, err := parseNativeReviewText(input); err == nil {
		t.Fatal("native trailing no-findings contradiction was accepted")
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

func TestReadNativeOutputRejectsAmbiguousZeroCandidateNarrative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	if err := os.WriteFile(path, []byte("The derived timeout context preserves parent cancellation, earlier deadlines, and request-scoped values while enforcing the approved two-second limit.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readNativeOutput(path); err == nil {
		t.Fatal("ambiguous prose was accepted as PASS evidence")
	}
}

func TestReadNativeOutputAcceptsStandaloneAgentFinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-review.txt")
	if err := os.WriteFile(path, []byte("- [P1] Preserve cancellation — [client.go:17](/private/tmp/review/client.go:17)\n\n  The new context drops caller cancellation.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := readNativeOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || result.Findings[0].CodeLocation.AbsoluteFilePath != "/private/tmp/review/client.go" {
		t.Fatalf("findings = %#v", result.Findings)
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
