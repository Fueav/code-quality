package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestNativeSessionOwnsModeSpecificArtifactsAndCleanup(t *testing.T) {
	repository, base, target := nativeSessionRepository(t)
	request := quality.ReviewRequest{
		Repository: "example/repo", TargetBranch: "main", BaseCommit: base, TargetCommit: target,
		DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	session, err := PrepareNative(context.Background(), Options{
		RepositoryRoot: repository,
		OutputRoot:     filepath.Join(t.TempDir(), "sessions"),
		Host:           "codex",
		Request:        request,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ChangedFiles[0] = "mutated.go"
	if got := session.Request().ChangedFiles; len(got) != 1 || got[0] != "app.go" {
		t.Fatalf("native session request changed through its caller: %#v", got)
	}
	artifacts := session.Artifacts()
	for _, path := range []string{
		artifacts.FinalMessagePath(), artifacts.JSONLPath(), artifacts.StderrPath(),
		artifacts.FreezeManifestPath(), artifacts.MetricsPath(), artifacts.ResultPath(), artifacts.MarkdownPath(),
	} {
		if !strings.HasPrefix(path, session.Directory()+string(filepath.Separator)) {
			t.Fatalf("artifact escaped native session: %q", path)
		}
	}
	for _, legacy := range []string{"rubric.md", "workflow.md", "model-review.schema.json", "evidence-context.json"} {
		if _, err := os.Lstat(filepath.Join(session.Directory(), "input", legacy)); !os.IsNotExist(err) {
			t.Fatalf("native session contains legacy artifact %s: %v", legacy, err)
		}
	}
	if err := session.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(session.RepositoryDirectory()); !os.IsNotExist(err) {
		t.Fatalf("isolated checkout remains after cleanup: %v", err)
	}
	if _, err := os.Stat(session.Directory()); err != nil {
		t.Fatalf("retained session was removed: %v", err)
	}
}

func nativeSessionRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repository := t.TempDir()
	runGitForNativeSession(t, repository, "init", "-q")
	runGitForNativeSession(t, repository, "config", "user.email", "fixture@example.invalid")
	runGitForNativeSession(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForNativeSession(t, repository, "add", "app.go")
	runGitForNativeSession(t, repository, "commit", "-qm", "base")
	base := strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\nfunc Run() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForNativeSession(t, repository, "add", "app.go")
	runGitForNativeSession(t, repository, "commit", "-qm", "target")
	target := strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
	return repository, base, target
}

func runGitForNativeSession(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
