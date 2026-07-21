package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	reviewsession "github.com/Fueav/code-quality/internal/session"
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

func TestBareCommandRequiresHostSessionSkill(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "active Claude Code or Codex session") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPrepareCreatesCommittedReviewSession(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, stderr := prepareSession(t, repo, base, target)
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
	if prepared.Status != "READY_FOR_MAIN_REVIEW" || prepared.SessionDir == "" {
		t.Fatalf("prepared = %#v", prepared)
	}
	contents, err := os.ReadFile(filepath.Join(prepared.RepositoryDir, "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "func Run") {
		t.Fatalf("snapshot = %q", contents)
	}
	if _, err := os.Lstat(filepath.Join(prepared.RepositoryDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("snapshot exposes .git: %v", err)
	}
	for _, path := range []string{prepared.RequestPath, prepared.DiffPath, prepared.RubricPath, prepared.ModelSchemaPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("trusted artifact %s is invalid: %v", path, err)
		}
	}
}

func TestFinalizeWithoutPotentialBlockUsesOneAgent(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	review := integrationMainReview()
	review.Findings[0].Severity = "S2"
	review.Findings[0].TriggerConfidence = "T2"
	review.Findings[0].ProposedVerdict = "MANUAL_REVIEW"
	writeJSON(t, prepared.MainReviewPath, review)

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.SemanticResult != quality.ResultManualReview {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if result.Execution.AgentCount != 1 || result.Execution.VerifierCount != 0 {
		t.Fatalf("execution = %#v", result.Execution)
	}
	if result.Execution.InputTokens != nil || result.Execution.DurationMS != nil {
		t.Fatalf("unavailable host metrics were fabricated: %#v", result.Execution)
	}
	markdown, err := os.ReadFile(finalized.MarkdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "Tokens: unavailable input / unavailable output") {
		t.Fatalf("markdown = %s", markdown)
	}
}

func TestFinalizeRequestsOneBatchVerifierAndBlocksWhenConfirmed(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationMainReview())

	first := finalizeSession(t, prepared.SessionDir)
	if first.Status != "NEEDS_VERIFIER" || first.VerifierRequestPath == "" || first.VerifierReviewPath == "" {
		t.Fatalf("first finalize = %#v", first)
	}
	request := readJSON[quality.VerifierRequest](t, first.VerifierRequestPath)
	if len(request.Candidates) != 1 || request.Candidates[0].ID != "F-001" {
		t.Fatalf("verifier request = %#v", request)
	}
	writeJSON(t, first.VerifierReviewPath, quality.VerifierReview{Decisions: []quality.VerifierDecision{{
		FindingID: "F-001", Result: "confirmed",
		VerificationSummary: "Confirmed the production entry, concrete trigger, and complete chain.",
		Uncertainties:       []string{},
	}}})

	second := finalizeSession(t, prepared.SessionDir)
	if second.Status != "COMPLETE" || second.SemanticResult != quality.ResultBlock {
		t.Fatalf("second finalize = %#v", second)
	}
	result := readJSON[quality.ReviewResult](t, second.ResultPath)
	if result.Execution.AgentCount != 2 || result.Execution.VerifierCount != 1 || len(result.Findings) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Findings[0].Candidate.VerifierResult != "confirmed" {
		t.Fatalf("verifier result was not merged: %#v", result.Findings[0])
	}
}

func TestFinalizeCanRecordUnavailableVerifierWithoutForgingBlock(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationMainReview())
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"finalize", "--session", prepared.SessionDir,
		"--verifier-unavailable", "host does not expose subagents",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var finalized reviewsession.Finalized
	if err := json.Unmarshal(stdout.Bytes(), &finalized); err != nil {
		t.Fatal(err)
	}
	if finalized.SemanticResult != quality.ResultManualReview {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if result.Execution.AgentCount != 1 || result.Findings[0].Candidate.VerifierResult != "not_run" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.MissingContext) != 1 || !strings.Contains(result.MissingContext[0], "host does not expose subagents") {
		t.Fatalf("missing context = %#v", result.MissingContext)
	}
}

func TestFinalizeMissingMainReviewProducesIncompleteReport(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "INCOMPLETE" || finalized.SemanticResult != quality.ResultIncomplete {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Adjudication.Reasons) != 1 || !strings.Contains(result.Adjudication.Reasons[0], "main review is missing") {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeDetectsModifiedTrustedInput(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	restoreFixturePermissions(prepared.SessionDir)
	if err := os.WriteFile(prepared.DiffPath, []byte("forged diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, prepared.MainReviewPath, integrationMainReview())
	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "INCOMPLETE" {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Adjudication.Reasons) != 1 || !strings.Contains(result.Adjudication.Reasons[0], "trusted review input was modified") {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeRejectsTamperedVerifierRequest(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationMainReview())
	first := finalizeSession(t, prepared.SessionDir)
	request := readJSON[quality.VerifierRequest](t, first.VerifierRequestPath)
	request.Candidates[0].ID = "F-forged"
	if err := os.Chmod(first.VerifierRequestPath, 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, first.VerifierRequestPath, request)
	second := finalizeSession(t, prepared.SessionDir)
	if second.Status != "INCOMPLETE" {
		t.Fatalf("finalized = %#v", second)
	}
	result := readJSON[quality.ReviewResult](t, second.ResultPath)
	if len(result.Adjudication.Reasons) != 1 || !strings.Contains(result.Adjudication.Reasons[0], "existing verifier request does not match") {
		t.Fatalf("result = %#v", result)
	}
}

func TestMalformedMainReviewProducesIncompleteReport(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	if err := os.WriteFile(prepared.MainReviewPath, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "INCOMPLETE" || finalized.SemanticResult != quality.ResultIncomplete {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Adjudication.Reasons) == 0 || result.Execution.AgentCount != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestAdjudicateAndRender(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	reviewPath := filepath.Join(dir, "review.json")
	resultPath := filepath.Join(dir, "result.json")
	writeJSON(t, requestPath, integrationRequest())
	writeJSON(t, reviewPath, integrationMainReview())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"adjudicate", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("adjudicate exit code = %d, stderr = %s", code, stderr.String())
	}
	if err := os.WriteFile(resultPath, stdout.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	result := readJSON[quality.ReviewResult](t, resultPath)
	if result.Adjudication.SemanticResult != quality.ResultManualReview {
		t.Fatalf("result = %s, want MANUAL_REVIEW", result.Adjudication.SemanticResult)
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
	if !strings.Contains(stdout.String(), "**Result:** `MANUAL_REVIEW`") {
		t.Fatalf("rendered report is incomplete:\n%s", stdout.String())
	}
}

func TestPrepareRejectsSymlinkOutputRoot(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	targetDir := t.TempDir()
	outputRoot := filepath.Join(t.TempDir(), "sessions")
	if err := os.Symlink(targetDir, outputRoot); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"prepare", "--repo", repo, "--base", base, "--target", target,
		"--diff-reason", "test_increment", "--output-root", outputRoot,
	}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "non-symlink directory") {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func prepareSession(t *testing.T, repo, base, target string) (reviewsession.Prepared, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"prepare", "--repo", repo, "--base", base, "--target", target,
		"--diff-reason", "test_increment", "--output-root", filepath.Join(t.TempDir(), "sessions"),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("prepare exit code = %d, stderr = %s", code, stderr.String())
	}
	var prepared reviewsession.Prepared
	if err := json.Unmarshal(stdout.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreFixturePermissions(prepared.SessionDir) })
	return prepared, stderr.String()
}

func finalizeSession(t *testing.T, directory string) reviewsession.Finalized {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"finalize", "--session", directory}, &stdout, &stderr); code != 0 {
		t.Fatalf("finalize exit code = %d, stderr = %s", code, stderr.String())
	}
	var finalized reviewsession.Finalized
	if err := json.Unmarshal(stdout.Bytes(), &finalized); err != nil {
		t.Fatal(err)
	}
	return finalized
}

func cliReviewFixture(t *testing.T) (string, string, string) {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n\nfunc Run() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "target")
	return repo, base, git("rev-parse", "HEAD")
}

func integrationRequest() quality.ReviewRequest {
	return quality.ReviewRequest{
		Repository: "example/service", TargetBranch: "main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		DiffSelectionReason: "pull_request",
		ChangedFiles:        []string{"app.go"}, AffectedEntries: []string{"Run"},
	}
}

func integrationMainReview() quality.ModelReview {
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
			CodeLocations:         []quality.CodeLocation{{Path: "app.go", Line: 3}},
			AffectedCallPath:      []string{"entry", "Run"},
			TriggerCondition:      "Every production record invokes the remote API.",
			CausalChain:           []string{"The loop visits every record.", "Each record performs one request."},
			ProductionImpact:      "The job cannot finish before the next schedule.",
			VerificationPerformed: []string{"Traced the production entry and configured record count."},
			MinimalFix:            "Restore bounded batch processing.",
			Uncertainties:         []string{},
			VerifierResult:        "not_run",
		}},
		UninspectedScope: []string{}, MissingContext: []string{},
	}
}

func restoreFixturePermissions(root string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		} else {
			_ = os.Chmod(path, 0o600)
		}
		return nil
	})
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
