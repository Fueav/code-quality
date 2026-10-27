package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestValidateCurrentNativeResultAndRejectUnpublishedOrTamperedResults(t *testing.T) {
	request := quality.ReviewRequest{
		Repository: "fixture/repo", TargetBranch: "main", BaseCommit: strings.Repeat("a", 40),
		TargetCommit: strings.Repeat("b", 40), DiffSelectionReason: "test",
		ChangedFiles: []string{"app.go"}, AffectedEntries: []string{},
	}
	for _, test := range []struct {
		name     string
		response string
		mutate   func(*quality.NativeReviewResult)
		want     int
	}{
		{name: "published pass", response: `{"findings":[]}`, want: 0},
		{name: "unadjudicated blocker", response: `{"findings":[{"title":"Duplicate payment","priority":1,"code_location":{"path":"app.go","start_line":1,"end_line":1},"reason":"The operation charges twice.","suggestion":"Preserve idempotency."}]}`, want: 1},
		{name: "tampered identity", response: `{"findings":[]}`, mutate: func(result *quality.NativeReviewResult) { result.CurrentHead = strings.Repeat("c", 40) }, want: 1},
		{name: "unknown version", response: `{"findings":[]}`, mutate: func(result *quality.NativeReviewResult) { result.SchemaVersion++ }, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := quality.ClassifyFrozenNativeReview(quality.NativeOutcomeOptions{Request: request}, []byte(test.response), nil)
			if err != nil {
				t.Fatal(err)
			}
			result := outcome.Result()
			if test.mutate != nil {
				test.mutate(&result)
			}
			path := filepath.Join(t.TempDir(), "result.json")
			writeJSON(t, path, result)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"validate", path}, &stdout, &stderr); code != test.want {
				t.Fatalf("validate code=%d want=%d stderr=%s", code, test.want, stderr.String())
			}
		})
	}
}
