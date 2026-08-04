package codexreview

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeReviewTransactionAcquiresLeaseBeforeDiscovery(t *testing.T) {
	lease := &recordingLease{}
	_, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: filepath.Join(t.TempDir(), "missing"),
		AcquireLease: func() (io.Closer, *os.File, error) {
			lease.acquired = true
			return lease, nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "resolve review scope") {
		t.Fatalf("error = %v", err)
	}
	if !lease.acquired || !lease.closed {
		t.Fatalf("lease lifecycle = %#v", lease)
	}
}

func TestNativeReviewTransactionPublishesAndCleansBeforeLeaseRelease(t *testing.T) {
	repository, base, target := transactionRepository(t)
	outputRoot := filepath.Join(t.TempDir(), "sessions")
	fakeCodex := transactionCodex(t, false)
	lease := &recordingLease{onClose: func() {
		matches, err := filepath.Glob(filepath.Join(outputRoot, "review-*", "input", "repository"))
		if err != nil {
			t.Error(err)
		}
		for _, match := range matches {
			if _, err := os.Lstat(match); !os.IsNotExist(err) {
				t.Errorf("lease released before checkout cleanup: %s", match)
			}
		}
	}}
	transaction, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: repository, Base: base, Target: target, DiffReason: "test",
		OutputRoot: outputRoot, CodexBinary: fakeCodex,
		AcquireLease: func() (io.Closer, *os.File, error) {
			lease.acquired = true
			return lease, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lease.closed || transaction.ExitCode != 0 || transaction.Summary.ModelCalls != 1 {
		t.Fatalf("transaction = %#v, lease = %#v", transaction, lease)
	}
	for _, path := range []string{transaction.Summary.ResultPath, transaction.Summary.MarkdownPath, transaction.Summary.FreezePath, transaction.Summary.MetricsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained transaction artifact %q: %v", path, err)
		}
	}
}

func TestNativeReviewTransactionCleansBeforeLeaseReleaseOnPublishFailure(t *testing.T) {
	repository, base, target := transactionRepository(t)
	outputRoot := filepath.Join(t.TempDir(), "sessions")
	lease := &recordingLease{onClose: func() {
		matches, err := filepath.Glob(filepath.Join(outputRoot, "review-*", "input", "repository"))
		if err != nil {
			t.Error(err)
		}
		for _, match := range matches {
			if _, err := os.Lstat(match); !os.IsNotExist(err) {
				t.Errorf("lease released before failure cleanup: %s", match)
			}
		}
	}}
	_, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: repository, Base: base, Target: target, DiffReason: "test",
		OutputRoot: outputRoot, CodexBinary: transactionCodex(t, true),
		AcquireLease: func() (io.Closer, *os.File, error) {
			lease.acquired = true
			return lease, nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "publish native review") {
		t.Fatalf("error = %v", err)
	}
	if !lease.closed {
		t.Fatal("lease was not released after failure cleanup")
	}
}

func TestNativeReviewTransactionPublishesIncompleteProcessFailure(t *testing.T) {
	repository, base, target := transactionRepository(t)
	transaction, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: repository, Base: base, Target: target, DiffReason: "test",
		OutputRoot: filepath.Join(t.TempDir(), "sessions"), CodexBinary: transactionFailingCodex(t),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.ExitCode != 1 || transaction.Summary.Status != "INCOMPLETE" || transaction.Summary.SemanticResult != "INCOMPLETE" || transaction.Summary.ModelCalls != 1 {
		t.Fatalf("transaction = %#v", transaction)
	}
	if _, err := os.Stat(transaction.Summary.ResultPath); err != nil {
		t.Fatalf("incomplete result was not published: %v", err)
	}
}

type recordingLease struct {
	acquired bool
	closed   bool
	onClose  func()
}

func (lease *recordingLease) Close() error {
	lease.closed = true
	if lease.onClose != nil {
		lease.onClose()
	}
	return nil
}

func transactionRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repository := t.TempDir()
	runTransactionGit(t, repository, "init", "-q")
	runTransactionGit(t, repository, "config", "user.email", "fixture@example.invalid")
	runTransactionGit(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTransactionGit(t, repository, "add", "app.go")
	runTransactionGit(t, repository, "commit", "-qm", "base")
	base := strings.TrimSpace(runTransactionGit(t, repository, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repository, "app.go"), []byte("package app\nfunc Run() bool { return true }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTransactionGit(t, repository, "add", "app.go")
	runTransactionGit(t, repository, "commit", "-qm", "target")
	target := strings.TrimSpace(runTransactionGit(t, repository, "rev-parse", "HEAD"))
	return repository, base, target
}

func runTransactionGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repository}, arguments...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func transactionCodex(t *testing.T, precreateResult bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
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
printf '%s\n' 'No findings.' > "$output"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":3}}'
`
	if precreateResult {
		script += "printf '%s\\n' occupied > \"$(dirname \"$output\")/review-result.json\"\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func transactionFailingCodex(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
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
printf '%s\n' 'No findings.' > "$output"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":3}}'
exit 7
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
