package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/internal/reviewrunner"
)

func TestRunnerRejectsContradictoryBudgetsAndStateArgumentsBeforeExecution(t *testing.T) {
	oldRun, oldCoordinated := runNativeReview, runCoordinatedReview
	calls := 0
	runNativeReview = func(context.Context, nativereview.TransactionOptions) (nativereview.TransactionResult, error) {
		calls++
		return nativereview.TransactionResult{}, nil
	}
	runCoordinatedReview = func(context.Context, reviewrunner.Options) (reviewrunner.Result, error) {
		calls++
		return reviewrunner.Result{}, nil
	}
	t.Cleanup(func() { runNativeReview, runCoordinatedReview = oldRun, oldCoordinated })
	for _, args := range [][]string{
		{"run-codex", "--total-timeout", "30m"},
		{"run-codex", "--finalize-reserve", "0s"},
		{"run-codex", "--runner-policy-version", "policy-v1"},
		{"run-codex", "--execution-fingerprint", "unknown"},
		{"run-codex", "--state-root", filepath.Join(t.TempDir(), "state"), "--output-root", filepath.Join(t.TempDir(), "evidence")},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || calls != 0 {
			t.Fatalf("%v: code=%d calls=%d stderr=%s", args, code, calls, stderr.String())
		}
	}
}

func TestRunnerPassesAttestedRequestAndKeepsManualRequiredNonGreen(t *testing.T) {
	old := runCoordinatedReview
	t.Cleanup(func() { runCoordinatedReview = old })
	state := filepath.Join(t.TempDir(), "state")
	calls := 0
	runCoordinatedReview = func(ctx context.Context, options reviewrunner.Options) (reviewrunner.Result, error) {
		calls++
		if options.StateRoot != state || options.RunnerPolicyVersion != "v1" || options.ExecutionFingerprint != "sha256:effective-context" || options.Current == nil {
			t.Fatalf("missing runner contract: %#v", options)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("coordinated request has no total deadline")
		}
		return reviewrunner.Result{Transaction: nativereview.TransactionResult{
			Plan: reviewplan.Decision{Status: reviewplan.StatusManualRequired, ProviderInvocations: 0},
		}, ExitCode: 5}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run-codex", "--state-root", state, "--runner-policy-version", "v1", "--execution-fingerprint", "sha256:effective-context", "--execution-profile", "production-ci"}, &stdout, &stderr)
	if code != 5 || calls != 1 || !bytes.Contains(stdout.Bytes(), []byte("MANUAL_REQUIRED")) {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
	}
}
