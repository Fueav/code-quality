package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestTargetCheckoutRejectsSourceAndIndexDrift(t *testing.T) {
	cases := map[string]func(*testing.T, NativeSession){
		"changed bytes": func(t *testing.T, session NativeSession) {
			writeCheckoutTestFile(t, session, "app.go", "package repaired\n")
		},
		"deleted file": func(t *testing.T, session NativeSession) {
			if err := os.Remove(filepath.Join(session.RepositoryDirectory(), "app.go")); err != nil {
				t.Fatal(err)
			}
		},
		"replacement symlink": func(t *testing.T, session NativeSession) {
			path := filepath.Join(session.RepositoryDirectory(), "app.go")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(session.SourceRepositoryRoot(), "app.go"), path); err != nil {
				t.Fatal(err)
			}
		},
		"executable mode": func(t *testing.T, session NativeSession) {
			if err := os.Chmod(filepath.Join(session.RepositoryDirectory(), "app.go"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"assume unchanged": func(t *testing.T, session NativeSession) {
			runGitForNativeSession(t, session.RepositoryDirectory(), "update-index", "--assume-unchanged", "app.go")
			writeCheckoutTestFile(t, session, "app.go", "package hidden\n")
			assertGitDirtyFlagHidesCheckoutChange(t, session)
		},
		"skip worktree": func(t *testing.T, session NativeSession) {
			runGitForNativeSession(t, session.RepositoryDirectory(), "update-index", "--skip-worktree", "app.go")
			writeCheckoutTestFile(t, session, "app.go", "package hidden\n")
			assertGitDirtyFlagHidesCheckoutChange(t, session)
		},
		"index only": func(t *testing.T, session NativeSession) {
			baseBlob := strings.TrimSpace(runGitForNativeSession(t, session.RepositoryDirectory(), "rev-parse", session.Request().BaseCommit+":app.go"))
			runGitForNativeSession(t, session.RepositoryDirectory(), "update-index", "--cacheinfo", "100644,"+baseBlob+",app.go")
		},
		"staged new file": func(t *testing.T, session NativeSession) {
			writeCheckoutTestFile(t, session, "new.go", "package changed\n")
			runGitForNativeSession(t, session.RepositoryDirectory(), "add", "new.go")
		},
		"wrong head": func(t *testing.T, session NativeSession) {
			runGitForNativeSession(t, session.RepositoryDirectory(), "checkout", "--detach", session.Request().BaseCommit)
		},
		"attached head": func(t *testing.T, session NativeSession) {
			runGitForNativeSession(t, session.RepositoryDirectory(), "switch", "-c", "unexpected-review-branch")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			session := checkoutIntegrityFixture(t)
			mutate(t, session)
			if err := session.VerifyTargetCheckout(context.Background()); err == nil {
				t.Fatal("changed checkout was accepted as the frozen target")
			}
		})
	}
}

func TestTargetCheckoutAllowsUntrackedBuildProductsAndUnchangedIndexFlags(t *testing.T) {
	session := checkoutIntegrityFixture(t)
	writeCheckoutTestFile(t, session, "test-output.log", "local build output\n")
	runGitForNativeSession(t, session.RepositoryDirectory(), "update-index", "--assume-unchanged", "app.go")
	if err := session.VerifyTargetCheckout(context.Background()); err != nil {
		t.Fatal(err)
	}
	runGitForNativeSession(t, session.RepositoryDirectory(), "update-index", "--no-assume-unchanged", "--skip-worktree", "app.go")
	if err := session.VerifyTargetCheckout(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTargetCheckoutSupportsSHA256GitObjects(t *testing.T) {
	t.Setenv("GIT_DEFAULT_HASH", "sha256")
	session := checkoutIntegrityFixture(t)
	if len(session.Request().TargetCommit) != 64 {
		t.Fatal("fixture did not create a SHA-256 Git repository")
	}
	if err := session.VerifyTargetCheckout(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeCheckoutTestFile(t, session, "app.go", "package changed\n")
	if err := session.VerifyTargetCheckout(context.Background()); err == nil {
		t.Fatal("SHA-256 checkout source drift was accepted")
	}
}

func TestTargetCheckoutValidatesCommittedSymlinksWithoutFollowingThem(t *testing.T) {
	repository, base, _ := nativeSessionRepository(t)
	if err := os.Symlink("../unavailable-outside-file", filepath.Join(repository, "link")); err != nil {
		t.Fatal(err)
	}
	name := "odd\tname\n.go"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForNativeSession(t, repository, "add", "link", name)
	runGitForNativeSession(t, repository, "commit", "-qm", "tracked symlink and unusual filename")
	target := strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
	session := prepareCheckoutIntegrityFixture(t, repository, base, target)
	if err := session.VerifyTargetCheckout(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(session.RepositoryDirectory(), "link")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../different-file", path); err != nil {
		t.Fatal(err)
	}
	if err := session.VerifyTargetCheckout(context.Background()); err == nil {
		t.Fatal("changed tracked symlink target was accepted")
	}
}

func TestTargetCheckoutRejectsSymlinkedTrackedParent(t *testing.T) {
	repository, base, _ := nativeSessionRepository(t)
	if err := os.Mkdir(filepath.Join(repository, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "nested", "entry.go"), []byte("package entry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForNativeSession(t, repository, "add", "nested/entry.go")
	runGitForNativeSession(t, repository, "commit", "-qm", "nested source")
	target := strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
	session := prepareCheckoutIntegrityFixture(t, repository, base, target)
	path := filepath.Join(session.RepositoryDirectory(), "nested")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repository, "nested"), path); err != nil {
		t.Fatal(err)
	}
	if err := session.VerifyTargetCheckout(context.Background()); err == nil {
		t.Fatal("tracked parent symlink was accepted")
	}
}

func TestPrepareAndReopenNativeRejectCheckoutFilterDrift(t *testing.T) {
	for _, phase := range []string{"prepare", "reopen"} {
		t.Run(phase, func(t *testing.T) {
			repository, base, _ := nativeSessionRepository(t)
			if err := os.WriteFile(filepath.Join(repository, ".gitattributes"), []byte("app.go filter=drift\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGitForNativeSession(t, repository, "add", ".gitattributes")
			runGitForNativeSession(t, repository, "commit", "-qm", "checkout filter")
			target := strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
			var existing NativeSession
			if phase == "reopen" {
				existing = prepareCheckoutIntegrityFixture(t, repository, base, target)
			}
			runGitForNativeSession(t, repository, "config", "filter.drift.smudge", "sed s/true/false/g")
			var err error
			if phase == "reopen" {
				_, err = ReopenNative(context.Background(), existing.Directory())
			} else {
				_, err = PrepareNative(context.Background(), checkoutIntegrityOptions(t, repository, base, target))
			}
			if err == nil || !strings.Contains(err.Error(), "tracked file bytes differ") {
				t.Fatalf("checkout filter drift was not rejected: %v", err)
			}
		})
	}
}

func TestTargetCheckoutRejectsPopulatedSubmodule(t *testing.T) {
	repository, base, target := nativeSessionRepository(t)
	runGitForNativeSession(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+target+",module")
	runGitForNativeSession(t, repository, "commit", "-qm", "gitlink")
	target = strings.TrimSpace(runGitForNativeSession(t, repository, "rev-parse", "HEAD"))
	session := prepareCheckoutIntegrityFixture(t, repository, base, target)
	writeCheckoutTestFile(t, session, "module/unverified.go", "package module\n")
	if err := session.VerifyTargetCheckout(context.Background()); err == nil || !strings.Contains(err.Error(), "submodule cannot be verified") {
		t.Fatalf("populated submodule was not rejected: %v", err)
	}
}

func checkoutIntegrityFixture(t *testing.T) NativeSession {
	t.Helper()
	repository, base, target := nativeSessionRepository(t)
	return prepareCheckoutIntegrityFixture(t, repository, base, target)
}

func prepareCheckoutIntegrityFixture(t *testing.T, repository, base, target string) NativeSession {
	t.Helper()
	session, err := PrepareNative(context.Background(), checkoutIntegrityOptions(t, repository, base, target))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Cleanup() })
	return session
}

func checkoutIntegrityOptions(t *testing.T, repository, base, target string) Options {
	t.Helper()
	return Options{
		RepositoryRoot: repository,
		OutputRoot:     filepath.Join(t.TempDir(), "sessions"),
		Host:           "codex",
		Request: quality.ReviewRequest{
			Repository: "example/repo", TargetBranch: "main", BaseCommit: base, TargetCommit: target,
			DiffSelectionReason: "test", ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
		},
	}
}

func writeCheckoutTestFile(t *testing.T, session NativeSession, path, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(session.RepositoryDirectory(), path), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertGitDirtyFlagHidesCheckoutChange(t *testing.T, session NativeSession) {
	t.Helper()
	if status := runGitForNativeSession(t, session.RepositoryDirectory(), "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("fixture must hide the file change from Git's dirty check, got %q", status)
	}
}
