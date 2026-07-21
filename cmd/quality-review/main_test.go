package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "quality-review dev" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestPrepareExplicitBaseline(t *testing.T) {
	repo := t.TempDir()
	git := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo}, arguments...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Quality Test", "GIT_AUTHOR_EMAIL=quality@example.test",
			"GIT_COMMITTER_NAME=Quality Test", "GIT_COMMITTER_EMAIL=quality@example.test",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "change")
	target := git("rev-parse", "HEAD")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"prepare", "--repo", repo, "--base", base,
		"--target", target, "--diff-reason", "test_increment",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var request quality.ReviewRequest
	if err := json.Unmarshal(stdout.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.BaseCommit != base || request.TargetCommit != target || strings.Join(request.ChangedFiles, ",") != "README.md" {
		t.Fatalf("request = %#v", request)
	}
}

func TestAdjudicateAndRender(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	reviewPath := filepath.Join(dir, "review.json")
	resultPath := filepath.Join(dir, "result.json")
	writeJSON(t, requestPath, integrationRequest())
	writeJSON(t, reviewPath, integrationReview())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"adjudicate", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("adjudicate exit code = %d, stderr = %s", code, stderr.String())
	}
	if err := os.WriteFile(resultPath, stdout.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var result quality.ReviewResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultBlock {
		t.Fatalf("result = %s, want BLOCK", result.Adjudication.SemanticResult)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"validate", resultPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("validate exit code = %d, stderr = %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"render", resultPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("render exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "**Result:** `BLOCK`") {
		t.Fatalf("rendered report is incomplete:\n%s", stdout.String())
	}
}

func TestMalformedModelReviewProducesIncompleteResult(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	reviewPath := filepath.Join(dir, "review.json")
	writeJSON(t, requestPath, integrationRequest())
	if err := os.WriteFile(reviewPath, []byte(`{"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"adjudicate", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var result quality.ReviewResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Adjudication.SemanticResult != quality.ResultIncomplete {
		t.Fatalf("result = %s, want INCOMPLETE", result.Adjudication.SemanticResult)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func integrationRequest() quality.ReviewRequest {
	return quality.ReviewRequest{
		Repository: "example/service", TargetBranch: "main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		DiffSelectionReason: "pull_request",
		ChangedFiles:        []string{"internal/worker.go"}, AffectedEntries: []string{"RunWorker"},
	}
}

func integrationReview() quality.ModelReview {
	return quality.ModelReview{
		ActivatedRuleFamilies: []string{"D1"},
		InactiveRuleFamilies: []quality.InactiveRuleFamily{
			{ID: "D2", Reason: "No business result change."},
			{ID: "D3", Reason: "No resource lifecycle change."},
			{ID: "D4", Reason: "No security or rollout change."},
		},
		Findings: []quality.Finding{{
			ID: "F-001", RuleID: "DES-003", ProposedVerdict: "BLOCK",
			Severity: "S3", TriggerConfidence: "T3", EvidenceLevel: "E2",
			IntroducedOrWorsenedByChange: true, FindingIsNotStylePreference: true,
			CodeLocations:         []quality.CodeLocation{{Path: "internal/worker.go", Line: 42}},
			AffectedCallPath:      []string{"cmd/service.main", "worker.RunWorker"},
			TriggerCondition:      "Every production record invokes the remote API.",
			CausalChain:           []string{"The loop visits every record.", "Each record performs one request."},
			ProductionImpact:      "The job cannot finish before the next schedule.",
			VerificationPerformed: []string{"Traced the production entry and configured record count."},
			MinimalFix:            "Restore bounded batch processing.", Uncertainties: []string{},
			VerifierResult: "confirmed",
		}},
		UninspectedScope: []string{}, MissingContext: []string{},
		Execution: quality.Execution{AgentCount: 2, VerifierCount: 1, InputTokens: 100, OutputTokens: 50, DurationMS: 500},
	}
}
