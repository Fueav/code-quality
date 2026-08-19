package nativereview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Fueav/code-quality/quality"
)

func TestRestrictedResumeReusesFrozenNativeReview(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "quota exceeded")
	initial := fixture.run(t)
	if initial.ExitCode != 1 || initial.Status.State != StateRestrictedRetryable {
		t.Fatalf("initial transaction = %#v", initial)
	}
	if initial.Status.NativeInvocationsThisRun != 1 || initial.Status.RestrictedInvocationsThisRun != 1 || initial.Status.ProviderInvocationsThisRun != 2 {
		t.Fatalf("initial invocation accounting = %#v", initial.Status)
	}
	for _, path := range []string{
		filepath.Join(initial.Status.SessionDir, "checkpoint.json"),
		filepath.Join(initial.Status.SessionDir, "output", "native-review-freeze.json"),
		filepath.Join(initial.Status.SessionDir, "output", "restricted-attempts", "0001", "attempt.json"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retained checkpoint artifact %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(initial.Status.SessionDir, "output", "review-result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retryable session published a formal result: %v", err)
	}

	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExitCode != 3 || resumed.Status.State != StatePublished || !resumed.Status.NativeReviewReused {
		t.Fatalf("resumed transaction = %#v", resumed)
	}
	if resumed.Status.NativeInvocationsThisRun != 0 || resumed.Status.RestrictedInvocationsThisRun != 1 || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("resume invocation accounting = %#v", resumed.Status)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted,restricted" {
		t.Fatalf("provider invocation ledger = %#v", got)
	}

	result := readTransactionResult(t, filepath.Join(initial.Status.SessionDir, "output", "review-result.json"))
	if result.Execution.ProviderInvocations != 3 || result.Execution.ProviderAttemptsTotal != 3 || result.Execution.NativeAttempts != 1 || result.Execution.RestrictedAttempts != 2 {
		t.Fatalf("published attempt accounting = %#v", result.Execution)
	}
	if result.Execution.AdoptedRestrictedAttempt == nil || *result.Execution.AdoptedRestrictedAttempt != 2 || !result.Execution.Resumed || result.Execution.ResumedSessionDigest == nil {
		t.Fatalf("published resume audit = %#v", result.Execution)
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != fixture.finding.ID {
		t.Fatalf("frozen finding identity/order changed: %#v", result.Findings)
	}

	if err := os.Remove(fixture.providerPath); err != nil {
		t.Fatal(err)
	}
	idempotent, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if idempotent.Status.State != StatePublished || idempotent.Status.ProviderInvocationsThisRun != 0 || idempotent.ExitCode != 3 {
		t.Fatalf("idempotent resume = %#v", idempotent)
	}
}

func TestRestrictedResumeRejectsTamperingBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		path func(string) string
	}{
		{name: "trusted diff", path: func(root string) string { return filepath.Join(root, "input", "trusted.diff") }},
		{name: "native freeze", path: func(root string) string { return filepath.Join(root, "output", "native-review-freeze.json") }},
		{name: "restricted policy", path: func(root string) string { return filepath.Join(root, "input", "restricted-adjudication-policy.md") }},
		{name: "restricted schema", path: func(root string) string {
			return filepath.Join(root, "input", "restricted-adjudication-output.schema.json")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRestrictedResumeFixture(t, 1, "request timed out")
			initial := fixture.run(t)
			before := len(readInvocationLedger(t, fixture.counterPath))
			path := test.path(initial.Status.SessionDir)
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tampered\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
				SessionDir: initial.Status.SessionDir,
				Provider:   NewCodexProvider(fixture.providerPath),
				AcquireLease: func() (io.Closer, *os.File, error) {
					return &recordingLease{}, nil, nil
				},
			})
			if err == nil || resumed.Status.ProviderInvocationsThisRun != 0 {
				t.Fatalf("tampered resume = %#v, err = %v", resumed, err)
			}
			after := len(readInvocationLedger(t, fixture.counterPath))
			if after != before {
				t.Fatalf("tampered resume invoked Provider: before=%d after=%d", before, after)
			}
		})
	}
}

func TestSecondRestrictedFailureRequiresManualAndStopsFurtherCalls(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 2, "provider capacity unavailable")
	initial := fixture.run(t)
	resumed, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ExitCode != 5 || resumed.Status.State != StateManualRequired || resumed.Status.ProviderInvocationsThisRun != 1 {
		t.Fatalf("second restricted failure = %#v", resumed)
	}
	before := len(readInvocationLedger(t, fixture.counterPath))
	again, err := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ExitCode != 5 || again.Status.State != StateManualRequired || again.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("manual-required retry = %#v", again)
	}
	if after := len(readInvocationLedger(t, fixture.counterPath)); after != before {
		t.Fatalf("manual-required retry invoked Provider: before=%d after=%d", before, after)
	}
}

func TestConcurrentRestrictedResumeAllowsOneProviderInvocation(t *testing.T) {
	fixture := newRestrictedResumeFixture(t, 1, "rate limit exceeded")
	initial := fixture.run(t)
	t.Setenv("FAKE_RESTRICTED_SLEEP", "1")
	type response struct {
		result TransactionResult
		err    error
	}
	firstDone := make(chan response, 1)
	go func() {
		result, err := ResumeRestricted(context.Background(), ResumeOptions{
			SessionDir: initial.Status.SessionDir,
			Provider:   NewCodexProvider(fixture.providerPath),
			AcquireLease: func() (io.Closer, *os.File, error) {
				return &recordingLease{}, nil, nil
			},
		})
		firstDone <- response{result: result, err: err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for len(readInvocationLedger(t, fixture.counterPath)) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	second, secondErr := ResumeRestricted(context.Background(), ResumeOptions{
		SessionDir: initial.Status.SessionDir,
		Provider:   NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) {
			return &recordingLease{}, nil, nil
		},
	})
	first := <-firstDone
	if first.err != nil || first.result.Status.State != StatePublished {
		t.Fatalf("first resume = %#v, err = %v", first.result, first.err)
	}
	if !errors.Is(secondErr, ErrRestrictedResumeActive) || second.Status.ProviderInvocationsThisRun != 0 {
		t.Fatalf("concurrent resume = %#v, err = %v", second, secondErr)
	}
	if got := readInvocationLedger(t, fixture.counterPath); strings.Join(got, ",") != "native,restricted,restricted" {
		t.Fatalf("concurrent Provider ledger = %#v", got)
	}
}

type restrictedResumeFixture struct {
	repository   string
	base         string
	target       string
	providerPath string
	counterPath  string
	outputRoot   string
	finding      quality.NativeFinding
}

func newRestrictedResumeFixture(t *testing.T, failures int, failureText string) restrictedResumeFixture {
	t.Helper()
	repository, base, target := transactionRepository(t)
	directory := t.TempDir()
	nativeResponsePath := filepath.Join(directory, "native.json")
	restrictedResponsePath := filepath.Join(directory, "restricted.json")
	counterPath := filepath.Join(directory, "invocations.log")
	if err := os.WriteFile(counterPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	finding, err := quality.IdentifyNativeFinding(quality.NativeFinding{
		Priority: 1, Title: "Reachable duplicate payment", CodeLocation: quality.NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
		Reason: "The changed path can submit the same payment twice.", Suggestion: "Make the operation idempotent.",
	})
	if err != nil {
		t.Fatal(err)
	}
	native := `{"findings":[{"priority":1,"title":"Reachable duplicate payment","code_location":{"path":"app.go","start_line":2,"end_line":2},"reason":"The changed path can submit the same payment twice.","suggestion":"Make the operation idempotent."}]}`
	restricted := fmt.Sprintf(`{"adjudications":[{"finding_id":%q,"validity":"SUPPORTED","severity":"S3","trigger_confidence":"T3","evidence_level":"E2","introduced_or_worsened_by_change":true,"trigger_condition_is_concrete":true,"causal_chain_is_complete":true,"finding_is_not_style_preference":true,"recommended_disposition":"BLOCK","evidence_refs":[{"path":"app.go","start_line":2,"end_line":2,"support":"The committed target contains the reachable payment path."}],"uncertainties":[],"reason":"The exact target proves the duplicate payment."}]}`, finding.ID)
	if err := os.WriteFile(nativeResponsePath, []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restrictedResponsePath, []byte(restricted), 0o600); err != nil {
		t.Fatal(err)
	}
	providerPath := filepath.Join(directory, "codex")
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
case "$output" in
  */restricted-attempts/*)
    printf '%s\n' restricted >> "$FAKE_INVOCATION_COUNTER"
    count=$(grep -c '^restricted$' "$FAKE_INVOCATION_COUNTER")
    if [ "$count" -le "$FAKE_RESTRICTED_FAILURES" ]; then
      printf '%s\n' "$FAKE_RESTRICTED_FAILURE" >&2
      printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":8,"output_tokens":1}}'
      exit 75
    fi
    if [ -n "${FAKE_RESTRICTED_SLEEP:-}" ]; then sleep "$FAKE_RESTRICTED_SLEEP"; fi
    cp "$FAKE_RESTRICTED_RESPONSE" "$output"
    ;;
  *)
    printf '%s\n' native >> "$FAKE_INVOCATION_COUNTER"
    cp "$FAKE_NATIVE_RESPONSE" "$output"
    ;;
esac
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":20,"output_tokens":4}}'
`
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NATIVE_RESPONSE", nativeResponsePath)
	t.Setenv("FAKE_RESTRICTED_RESPONSE", restrictedResponsePath)
	t.Setenv("FAKE_INVOCATION_COUNTER", counterPath)
	t.Setenv("FAKE_RESTRICTED_FAILURES", fmt.Sprintf("%d", failures))
	t.Setenv("FAKE_RESTRICTED_FAILURE", failureText)
	return restrictedResumeFixture{
		repository: repository, base: base, target: target, providerPath: providerPath,
		counterPath: counterPath, outputRoot: filepath.Join(t.TempDir(), "sessions"), finding: finding,
	}
}

func (fixture restrictedResumeFixture) run(t *testing.T) TransactionResult {
	t.Helper()
	result, err := RunTransaction(context.Background(), TransactionOptions{
		RepositoryPath: fixture.repository, Base: fixture.base, Target: fixture.target, DiffReason: "resume-test",
		OutputRoot: fixture.outputRoot, Provider: NewCodexProvider(fixture.providerPath),
		AcquireLease: func() (io.Closer, *os.File, error) { return &recordingLease{}, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readInvocationLedger(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}
