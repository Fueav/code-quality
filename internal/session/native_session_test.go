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
		artifacts.RestrictedFinalMessagePath(), artifacts.RestrictedJSONLPath(), artifacts.RestrictedStderrPath(),
		artifacts.RestrictedFreezeManifestPath(), artifacts.RestrictedMetricsPath(),
	} {
		if !strings.HasPrefix(path, session.Directory()+string(filepath.Separator)) {
			t.Fatalf("artifact escaped native session: %q", path)
		}
	}
	for _, path := range []string{session.RestrictedAdjudicationPolicyPath(), session.RestrictedAdjudicationSchemaPath()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o400 {
			t.Fatalf("restricted adjudication input %s is not frozen: info=%v err=%v", path, info, err)
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

func TestNativeSessionAcceptsClaudeCodeWithoutChangingCheckoutIsolation(t *testing.T) {
	repository, base, target := nativeSessionRepository(t)
	session, err := PrepareNative(context.Background(), Options{
		RepositoryRoot: repository,
		OutputRoot:     filepath.Join(t.TempDir(), "sessions"),
		Host:           "claude-code",
		Request: quality.ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: base, TargetCommit: target,
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Cleanup() })
	if session.RepositoryDirectory() == repository {
		t.Fatal("Claude native review reused the caller's checkout")
	}
	raw, err := os.ReadFile(filepath.Join(session.Directory(), "input", "session-metadata.json"))
	if err != nil || !strings.Contains(string(raw), `"host": "claude-code"`) ||
		!strings.Contains(string(raw), `"runtime_mode": "claude_code_native_review"`) {
		t.Fatalf("Claude session metadata = %s, error = %v", raw, err)
	}
}

func TestClaudeCheckoutDoesNotFallBackToIndependentClone(t *testing.T) {
	repository, _, target := nativeSessionRepository(t)
	layout := NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.InputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.RepositoryDir, []byte("block worktree creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err := prepareCheckout(context.Background(), repository, target, layout, false)
	if err == nil || !strings.Contains(err.Error(), "shared-clone fallback is disabled") {
		t.Fatalf("Claude checkout error = %v", err)
	}
	if mode != CheckoutModeWorktree {
		t.Fatalf("Claude checkout reported degraded mode %q", mode)
	}
	info, statErr := os.Lstat(layout.RepositoryDir)
	if statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("Claude checkout replaced the blocker with an independent clone: info=%v error=%v", info, statErr)
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
