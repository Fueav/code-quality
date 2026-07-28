package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	evalrunner "github.com/Fueav/code-quality/internal/eval"
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
	if prepared.CheckoutMode != reviewsession.CheckoutModeWorktree {
		t.Fatalf("checkout mode = %q", prepared.CheckoutMode)
	}
	contents, err := os.ReadFile(filepath.Join(prepared.RepositoryDir, "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "func Run") {
		t.Fatalf("snapshot = %q", contents)
	}
	if info, err := os.Lstat(filepath.Join(prepared.RepositoryDir, ".git")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("repository is not a git worktree: %v", err)
	}
	for _, path := range []string{prepared.RequestPath, prepared.DiffPath, prepared.RubricPath, prepared.ModelSchemaPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("trusted artifact %s is invalid: %v", path, err)
		}
	}
}

func TestPrepareFallsBackToSharedCloneWhenGitMetadataIsReadOnly(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	before := snapshotGitMetadata(t, repo)
	restore := makeGitMetadataReadOnly(t, repo)
	prepared, stderr := prepareSession(t, repo, base, target)
	restore()
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
	if prepared.CheckoutMode != reviewsession.CheckoutModeClone {
		t.Fatalf("checkout mode = %q", prepared.CheckoutMode)
	}
	metadata := readJSON[reviewsession.Metadata](t, prepared.MetadataPath)
	if metadata.CheckoutMode != reviewsession.CheckoutModeClone {
		t.Fatalf("metadata checkout mode = %q", metadata.CheckoutMode)
	}
	if info, err := os.Lstat(filepath.Join(prepared.RepositoryDir, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("repository is not a shared clone: %v", err)
	}
	command := exec.Command("git", "-C", prepared.RepositoryDir, "rev-parse", "HEAD")
	if output, err := command.Output(); err != nil || strings.TrimSpace(string(output)) != target {
		t.Fatalf("clone HEAD = %q, err = %v", output, err)
	}
	if after := snapshotGitMetadata(t, repo); !reflect.DeepEqual(after, before) {
		t.Fatalf("main repository metadata changed:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestFinalizeCloneCheckoutRemovesOnlyRepository(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	restore := makeGitMetadataReadOnly(t, repo)
	prepared, _ := prepareSession(t, repo, base, target)
	restore()
	writeJSON(t, prepared.MainReviewPath, integrationReviewWithFiveFindings())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" {
		t.Fatalf("finalized = %#v", finalized)
	}
	if _, err := os.Lstat(prepared.RepositoryDir); !os.IsNotExist(err) {
		t.Fatalf("clone checkout was not removed: %v", err)
	}
	for _, path := range []string{prepared.SessionDir, finalized.ResultPath, finalized.MarkdownPath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preserved artifact %s is unavailable: %v", path, err)
		}
	}
}

func TestFinalizeReportOnlyManualFindingUsesOneAgent(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	review := integrationReviewWithFiveFindings()
	writeJSON(t, prepared.MainReviewPath, review)

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.SemanticResult != quality.ResultManualReview {
		t.Fatalf("finalized = %#v", finalized)
	}
	if _, err := os.Lstat(prepared.RepositoryDir); !os.IsNotExist(err) {
		t.Fatalf("worktree was not removed: %v", err)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if result.Execution.AgentCount != 1 || result.Execution.VerifierCount != 0 {
		t.Fatalf("execution = %#v", result.Execution)
	}
	if result.Execution.Host != "claude-code" || result.Execution.SkillVersion != quality.SkillVersion {
		t.Fatalf("trusted host metadata was not preserved: %#v", result.Execution)
	}
	if len(result.InspectedContext) != 1 || result.InspectedContext[0].Path != "app.go" {
		t.Fatalf("inspected context = %#v", result.InspectedContext)
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

func TestFinalizePreservesFiveIndependentFindings(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationReviewWithFiveFindings())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Findings) != 5 {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func TestFinalizeZeroFindingsRequiresOneRereview(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationEmptyReview())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "REREVIEW_REQUIRED" || !finalized.ReviewRequired || finalized.CompletedReviewRounds != 1 || finalized.MaximumReviewRounds != 2 {
		t.Fatalf("finalized = %#v", finalized)
	}
	if !reflect.DeepEqual(finalized.RereviewScope, []string{"D1", "D2", "D3", "D4"}) {
		t.Fatalf("rereview scope = %#v", finalized.RereviewScope)
	}
	if finalized.NextReviewPath == "" || finalized.NextReviewPath == prepared.MainReviewPath {
		t.Fatalf("next review path = %q", finalized.NextReviewPath)
	}
	if _, err := os.Lstat(prepared.RepositoryDir); err != nil {
		t.Fatalf("review worktree must remain available for rereview: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prepared.SessionDir, "output", "review-result.json")); !os.IsNotExist(err) {
		t.Fatalf("zero-finding first pass must not publish a final report: %v", err)
	}
}

func TestFinalizeRereviewsOnlyDimensionsWithoutFirstRoundFindings(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationReviewForRules("COR-001"))

	first := finalizeSession(t, prepared.SessionDir)
	if first.Status != "REREVIEW_REQUIRED" || !reflect.DeepEqual(first.RereviewScope, []string{"D1", "D3", "D4"}) {
		t.Fatalf("first finalize = %#v", first)
	}
	writeJSON(t, first.NextReviewPath, integrationReviewForRules("REL-002"))

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.CompletedReviewRounds != 2 {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Findings) != 2 || result.Execution.RetryCount == nil || *result.Execution.RetryCount != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeFourDimensionsCompleteWithoutRereview(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationReviewWithFiveFindings())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.CompletedReviewRounds != 1 || finalized.ReviewRequired {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if result.Execution.RetryCount == nil || *result.Execution.RetryCount != 0 {
		t.Fatalf("execution = %#v", result.Execution)
	}
}

func TestFinalizeRereviewFindingIsMergedAndAdjudicated(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationEmptyReview())
	first := finalizeSession(t, prepared.SessionDir)
	writeJSON(t, first.NextReviewPath, integrationMainReview())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.SemanticResult != quality.ResultManualReview || finalized.ReviewRequired || finalized.CompletedReviewRounds != 2 {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Findings) != 1 || result.Findings[0].Candidate.ID != "F-001" || result.Execution.RetryCount == nil || *result.Execution.RetryCount != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeTwoZeroFindingRoundsCompletesWithExplicitEvidence(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationEmptyReview())
	first := finalizeSession(t, prepared.SessionDir)
	writeJSON(t, first.NextReviewPath, integrationEmptyReview())

	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.SemanticResult != quality.ResultPass || finalized.CompletedReviewRounds != 2 || finalized.ReviewRequired {
		t.Fatalf("finalized = %#v", finalized)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Findings) != 0 || result.Execution.RetryCount == nil || *result.Execution.RetryCount != 1 || !strings.Contains(strings.Join(result.Adjudication.Reasons, " "), "two review rounds") {
		t.Fatalf("result = %#v", result)
	}
	markdown, err := os.ReadFile(finalized.MarkdownPath)
	if err != nil || !strings.Contains(string(markdown), "Retries: 1") || !strings.Contains(string(markdown), "two review rounds") {
		t.Fatalf("markdown = %s, err = %v", markdown, err)
	}
}

func TestFinalizeRereviewRequestIsIdempotentAndNeverCreatesThirdRound(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	writeJSON(t, prepared.MainReviewPath, integrationEmptyReview())

	first := finalizeSession(t, prepared.SessionDir)
	second := finalizeSession(t, prepared.SessionDir)
	if second.Status != "REREVIEW_REQUIRED" || second.NextReviewPath != first.NextReviewPath || second.CompletedReviewRounds != 1 || second.MaximumReviewRounds != 2 {
		t.Fatalf("first = %#v, second = %#v", first, second)
	}
	writeJSON(t, second.NextReviewPath, integrationEmptyReview())
	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "COMPLETE" || finalized.CompletedReviewRounds != 2 || finalized.NextReviewPath != "" {
		t.Fatalf("finalized = %#v", finalized)
	}
	if matches, err := filepath.Glob(filepath.Join(prepared.SessionDir, "output", "*review*.json")); err != nil || len(matches) != 3 {
		t.Fatalf("review artifacts = %#v, err = %v", matches, err)
	}
}

func TestFinalizeMissingMainReviewProducesIncompleteReport(t *testing.T) {
	repo, base, target := cliReviewFixture(t)
	prepared, _ := prepareSession(t, repo, base, target)
	finalized := finalizeSession(t, prepared.SessionDir)
	if finalized.Status != "INCOMPLETE" || finalized.SemanticResult != quality.ResultIncomplete {
		t.Fatalf("finalized = %#v", finalized)
	}
	if _, err := os.Lstat(prepared.RepositoryDir); !os.IsNotExist(err) {
		t.Fatalf("worktree was not removed: %v", err)
	}
	result := readJSON[quality.ReviewResult](t, finalized.ResultPath)
	if len(result.Adjudication.Reasons) != 1 || !strings.Contains(result.Adjudication.Reasons[0], "main review is missing") {
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
	if len(result.Adjudication.Reasons) == 0 || result.Execution.AgentCount != 1 || result.Execution.Host != "claude-code" || result.Execution.SkillVersion != quality.SkillVersion {
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
	if code := run([]string{"adjudicate", "--host", "claude-code", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
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

func TestCompareCommandUsesExternallySuppliedBaseline(t *testing.T) {
	directory := t.TempDir()
	productPath := filepath.Join(directory, "product.json")
	baselinePath := filepath.Join(directory, "baseline.json")
	writeJSON(t, productPath, evalrunner.FindingSet{
		SchemaVersion: 1, Source: "code-quality",
		Findings: []evalrunner.ComparisonFinding{{
			ID: "P-001", ComparisonKey: "shared", Dimension: "D1",
			CodeLocations: []quality.CodeLocation{{Path: "app.go", Line: 3}}, Description: "Product description.",
		}},
	})
	writeJSON(t, baselinePath, evalrunner.FindingSet{
		SchemaVersion: 1, Source: "host-review-export",
		Findings: []evalrunner.ComparisonFinding{{
			ID: "B-001", ComparisonKey: "shared", Dimension: "D1",
			CodeLocations: []quality.CodeLocation{{Path: "app.go", Line: 3}}, Description: "Baseline description.",
		}},
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"compare", "--product", productPath, "--baseline", baselinePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compare exit code = %d, stderr = %s", code, stderr.String())
	}
	var report evalrunner.ComparisonReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.BaselineSource != "host-review-export" || len(report.Shared) != 1 || report.Shared[0].Baseline.Description != "Baseline description." {
		t.Fatalf("report = %#v", report)
	}
}

func TestValidateRejectsMissingRequiredResultField(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	reviewPath := filepath.Join(dir, "review.json")
	resultPath := filepath.Join(dir, "result.json")
	writeJSON(t, requestPath, integrationRequest())
	writeJSON(t, reviewPath, integrationMainReview())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"adjudicate", "--host", "claude-code", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("adjudicate exit code = %d, stderr = %s", code, stderr.String())
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "inspected_context")
	writeJSON(t, resultPath, document)

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"validate", resultPath}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "inspected_context is required") {
		t.Fatalf("validate exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestValidateRejectsNullExecutionIdentityFields(t *testing.T) {
	policy, err := loadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(quality.IncompleteResult(integrationRequest(), policy, "trusted input was invalid"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"host", "skill_version", "agent_count", "verifier_count"} {
		t.Run(field, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			document["execution"].(map[string]any)[field] = nil
			path := filepath.Join(t.TempDir(), "review-result.json")
			writeJSON(t, path, document)
			var stdout, stderr bytes.Buffer
			if code := run([]string{"validate", path}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "cannot be null") {
				t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
			}
		})
	}
}

func TestReplayRecordRejectsHostMismatch(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	reviewPath := filepath.Join(dir, "review.json")
	resultPath := filepath.Join(dir, "result.json")
	writeJSON(t, requestPath, integrationRequest())
	writeJSON(t, reviewPath, integrationMainReview())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"adjudicate", "--host", "claude-code", requestPath, reviewPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("adjudicate exit code = %d, stderr = %s", code, stderr.String())
	}
	if err := os.WriteFile(resultPath, stdout.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{
		"replay", "record", "--case-id", "DES-003-insufficient", "--host", "codex", "--result", resultPath,
	}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "does not match result execution host claude-code") {
		t.Fatalf("replay exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestApplyReplayMetrics(t *testing.T) {
	record := evalrunner.ReplayRecord{}
	if err := applyReplayMetrics(&record, 120, 30, 4_500); err != nil {
		t.Fatal(err)
	}
	if record.Observed.InputTokens == nil || *record.Observed.InputTokens != 120 ||
		record.Observed.OutputTokens == nil || *record.Observed.OutputTokens != 30 ||
		record.Observed.DurationMS == nil || *record.Observed.DurationMS != 4_500 {
		t.Fatalf("metrics = %#v", record.Observed)
	}

	for name, values := range map[string][3]int{
		"partial":  {120, -1, 4_500},
		"negative": {-2, -1, -1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := applyReplayMetrics(&evalrunner.ReplayRecord{}, values[0], values[1], values[2]); err == nil {
				t.Fatal("expected replay metrics to be rejected")
			}
		})
	}

	if err := applyReplayMetrics(&evalrunner.ReplayRecord{}, -1, -1, -1); err != nil {
		t.Fatal(err)
	}
}

func TestReplayCommandsRejectIncompleteManifest(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "cases.json")
	manifestRaw, err := os.ReadFile(filepath.Join("..", "..", "evals", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["cases"] = manifest["cases"].([]any)[:3]
	writeJSON(t, manifestPath, manifest)

	recordsDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"replay", "summarize", "--cases", manifestPath, "--records", recordsDir,
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "must contain exactly 60 cases") {
		t.Fatalf("summarize exit code = %d, stderr = %s", code, stderr.String())
	}

	policy, err := loadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	review := integrationMainReview()
	review.Execution = quality.Execution{Host: "claude-code", SkillVersion: quality.SkillVersion, AgentCount: 1}
	result := quality.Adjudicate(integrationRequest(), review, policy)
	resultPath := filepath.Join(t.TempDir(), "result.json")
	writeJSON(t, resultPath, result)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"replay", "record", "--cases", manifestPath, "--case-id", "DES-003-insufficient",
		"--host", "claude-code", "--result", resultPath,
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "must contain exactly 60 cases") {
		t.Fatalf("record exit code = %d, stderr = %s", code, stderr.String())
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
		"prepare", "--host", "claude-code", "--repo", repo, "--base", base, "--target", target,
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
		"prepare", "--host", "claude-code", "--repo", repo, "--base", base, "--target", target,
		"--diff-reason", "test_increment", "--output-root", filepath.Join(t.TempDir(), "sessions"),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("prepare exit code = %d, stderr = %s", code, stderr.String())
	}
	var prepared reviewsession.Prepared
	if err := json.Unmarshal(stdout.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", prepared.RepositoryDir).Run()
		restoreFixturePermissions(prepared.SessionDir)
	})
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

func makeGitMetadataReadOnly(t *testing.T, repo string) func() {
	t.Helper()
	gitDir := filepath.Join(repo, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if err := os.Chmod(gitDir, info.Mode().Perm()); err != nil {
			t.Errorf("restore git metadata permissions: %v", err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(gitDir, 0o500); err != nil {
		t.Fatal(err)
	}
	return restore
}

func snapshotGitMetadata(t *testing.T, repo string) []string {
	t.Helper()
	root := filepath.Join(repo, ".git")
	entries := []string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, relative)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return entries
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
			ID:               "F-001",
			RuleID:           "DES-003",
			CodeLocations:    []quality.CodeLocation{{Path: "app.go", Line: 3}},
			ProductionImpact: "The job cannot finish before the next schedule.",
			MinimalFix:       "Restore bounded batch processing.",
		}},
		UninspectedScope: []string{}, MissingContext: []string{},
		InspectedContext: []quality.InspectedContext{{Path: "app.go", Purpose: "Trace the changed entry."}},
	}
}

func integrationEmptyReview() quality.ModelReview {
	review := integrationMainReview()
	review.Findings = []quality.Finding{}
	return review
}

func integrationReviewWithFiveFindings() quality.ModelReview {
	return integrationReviewForRules("DES-003", "COR-001", "COR-003", "REL-002", "SEC-001")
}

func integrationReviewForRules(ruleIDs ...string) quality.ModelReview {
	review := integrationEmptyReview()
	dimensions := map[string]string{
		"DES-003": "D1", "COR-001": "D2", "COR-003": "D2", "REL-002": "D3", "SEC-001": "D4",
	}
	active := map[string]bool{}
	review.Findings = []quality.Finding{}
	for index, ruleID := range ruleIDs {
		active[dimensions[ruleID]] = true
		review.Findings = append(review.Findings, quality.Finding{
			ID:               fmt.Sprintf("F-%03d", index+1),
			RuleID:           ruleID,
			CodeLocations:    []quality.CodeLocation{{Path: "app.go", Line: index + 1}},
			ProductionImpact: "A distinct production failure occurs.",
			MinimalFix:       "Fix this independent root cause.",
		})
	}
	review.ActivatedRuleFamilies = []string{}
	review.InactiveRuleFamilies = []quality.InactiveRuleFamily{}
	for _, dimension := range []string{"D1", "D2", "D3", "D4"} {
		if active[dimension] {
			review.ActivatedRuleFamilies = append(review.ActivatedRuleFamilies, dimension)
		} else {
			review.InactiveRuleFamilies = append(review.InactiveRuleFamilies, quality.InactiveRuleFamily{ID: dimension, Reason: "No first-round finding in this dimension."})
		}
	}
	return review
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
