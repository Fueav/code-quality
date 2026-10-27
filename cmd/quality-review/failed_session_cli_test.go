package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewrunner"
)

func TestFailedRequestExposesDurableStatusWithoutSuccessfulSummary(t *testing.T) {
	for _, mode := range []string{"native", "coordinated", "resume"} {
		for _, state := range []nativereview.SessionState{nativereview.StateTerminalError, nativereview.StatePublished} {
			t.Run(mode+"/"+string(state), func(t *testing.T) {
				oldNative, oldCoordinated, oldResume := runNativeReview, runCoordinatedReview, resumeRestrictedReview
				t.Cleanup(func() {
					runNativeReview, runCoordinatedReview, resumeRestrictedReview = oldNative, oldCoordinated, oldResume
				})
				session := filepath.Join(t.TempDir(), "session")
				transaction := nativereview.TransactionResult{
					Status: nativereview.SessionStatus{SchemaVersion: 1, State: state, SessionDir: session, NativeAttempts: 1},
				}
				runNativeReview = func(context.Context, nativereview.TransactionOptions) (nativereview.TransactionResult, error) {
					return transaction, context.DeadlineExceeded
				}
				runCoordinatedReview = func(context.Context, reviewrunner.Options) (reviewrunner.Result, error) {
					return reviewrunner.Result{Transaction: transaction, ExitCode: 1}, context.DeadlineExceeded
				}
				resumeRestrictedReview = func(context.Context, nativereview.ResumeOptions) (nativereview.TransactionResult, error) {
					return transaction, context.DeadlineExceeded
				}
				args := []string{"run-codex"}
				if mode == "coordinated" {
					args = append(args, "--state-root", filepath.Join(t.TempDir(), "state"))
				} else if mode == "resume" {
					args = []string{"resume-restricted", "--session", session}
				}
				var stdout, stderr bytes.Buffer
				if code := run(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), context.DeadlineExceeded.Error()) {
					t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				var status nativereview.SessionStatus
				if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || status.State != state || status.SessionDir != session || status.NativeAttempts != 1 {
					t.Fatalf("status=%#v err=%v stdout=%s", status, err, stdout.String())
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(stdout.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"result", "adjudication", "evidence_dir", "runner"} {
					if _, ok := fields[field]; ok {
						t.Fatalf("failed request exposed successful summary field %q", field)
					}
				}
			})
		}
	}
}

func TestFailureBeforeSessionDoesNotInventStatus(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := writeTransactionFailure(&stdout, &stderr, nativereview.TransactionResult{}, context.Canceled); code != 1 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
