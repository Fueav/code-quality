package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestRecordFromResultUsesAuthoritativeFields(t *testing.T) {
	result := replayResult(quality.ResultBlock, "DES-003", "S3", "T3", "E2", 2, 1)
	record := RecordFromResult("DES-003-positive", "claude-code", 1, result, HumanReview{Status: "pending"})
	if record.Observed.SemanticResult != quality.ResultBlock || len(record.Observed.RuleIDs) != 1 || record.Observed.RuleIDs[0] != "DES-003" {
		t.Fatalf("record = %#v", record)
	}
	if pointerValue(record.Observed.Severity) != "S3" || record.Observed.AgentCount != 2 || record.Observed.VerifierCount != 1 {
		t.Fatalf("observed = %#v", record.Observed)
	}
}

func TestReplayQualificationRequiresFullStableHumanConfirmedMatrix(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	policy := testPolicy()
	record := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.0", CaseID: "DES-003-positive", Host: "claude-code", RunNumber: 1,
		Observed: Observed{
			SemanticResult: quality.ResultBlock, RuleIDs: []string{"DES-003"},
			Severity: stringPointer("S3"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
			AgentCount: 2, VerifierCount: 1,
		},
		HumanReview: HumanReview{Status: "pending"},
	}
	report := RunReplay(manifest, policy, []ReplayRecord{record}, nil)
	if report.QualificationComplete || report.CasesCovered != 1 || report.SevereCasesStable != 0 || report.ValidRecords != 1 {
		t.Fatalf("report = %#v", report)
	}
	caseReport := findReplayCase(t, report, "DES-003-positive")
	if caseReport.HumanConfirmed || caseReport.ObservedRuns != 1 || caseReport.RequiredRuns != 3 {
		t.Fatalf("case report = %#v", caseReport)
	}
}

func TestReplayQualificationUsesOnlyLocalCodex(t *testing.T) {
	if hostsQualified([]string{"claude-code"}) || !hostsQualified([]string{"codex"}) || hostsQualified([]string{"claude-code", "codex"}) {
		t.Fatal("host qualification must use only Codex")
	}
}

func TestReplayExpectedMatchRequiresConfiguredVerifierOutcome(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	positive := findCase(t, manifest, "DES-003-positive")
	positiveRecord := ReplayRecord{Observed: Observed{
		SemanticResult: quality.ResultBlock,
		RuleIDs:        []string{"DES-003"},
		Severity:       stringPointer("S3"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
	}}
	if matchesExpected(positive, positiveRecord) {
		t.Fatal("positive replay without its required verifier must not match")
	}
	positiveRecord.Observed.VerifierCount = 1
	if !matchesExpected(positive, positiveRecord) {
		t.Fatal("positive replay with one verifier should match")
	}

	counterexample := findCase(t, manifest, "DES-003-counterexample")
	counterexampleRecord := ReplayRecord{Observed: Observed{
		SemanticResult: quality.ResultPass,
		RuleIDs:        []string{},
		VerifierCount:  1,
	}}
	if matchesExpected(counterexample, counterexampleRecord) {
		t.Fatal("counterexample replay with an unexpected verifier must not match")
	}
}

func TestReplayRejectsDuplicateIdentityAndAgentOverflow(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	record := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.0", CaseID: "DES-003-positive", Host: "codex", RunNumber: 1,
		Observed: Observed{
			SemanticResult: quality.ResultBlock, RuleIDs: []string{"DES-003"},
			Severity: stringPointer("S3"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
			AgentCount: 3, VerifierCount: 1,
		},
		HumanReview: HumanReview{Status: "confirmed"},
	}
	report := RunReplay(manifest, testPolicy(), []ReplayRecord{record, record}, nil)
	if report.InvalidRecords == 0 || report.AgentLimitRespected || !containsError(report.Errors, "duplicated") {
		t.Fatalf("report = %#v", report)
	}
}

func TestReplayHumanReviewUsesThreeStates(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	base := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.0", CaseID: "DES-003-counterexample", Host: "claude-code", RunNumber: 1,
		Observed: Observed{SemanticResult: quality.ResultPass, RuleIDs: []string{}, AgentCount: 1},
	}
	for name, review := range map[string]HumanReview{
		"pending":    {Status: "pending"},
		"confirmed":  {Status: "confirmed"},
		"overturned": {Status: "overturned", OverturnReason: stringPointer("The expected hard bound was absent.")},
	} {
		t.Run(name, func(t *testing.T) {
			record := base
			record.HumanReview = review
			report := RunReplay(manifest, testPolicy(), []ReplayRecord{record}, nil)
			if report.InvalidRecords != 0 {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestReplayRejectsInvalidExecutionAndAxesWithoutCountingMetrics(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	metric := 1
	record := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.0", CaseID: "DES-003-positive", Host: "codex", RunNumber: 1,
		Observed: Observed{
			SemanticResult: quality.ResultBlock, RuleIDs: []string{"DES-003"},
			Severity: stringPointer("S4"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
			AgentCount: 2, VerifierCount: -1, InputTokens: &metric, OutputTokens: &metric, DurationMS: &metric,
		},
		HumanReview: HumanReview{Status: "confirmed"},
	}
	report := RunReplay(manifest, testPolicy(), []ReplayRecord{record}, nil)
	if report.InvalidRecords != 1 || report.ValidRecords != 0 || report.MetricsAvailable != 0 || !containsError(report.Errors, "S/T/E") || !containsError(report.Errors, "execution metrics") {
		t.Fatalf("report = %#v", report)
	}
}

func TestLoadReplayRecordsRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "record.json")); err != nil {
		t.Fatal(err)
	}
	records, errors := LoadReplayRecords(directory)
	if len(records) != 0 || !containsError(errors, "invalid replay file") {
		t.Fatalf("records = %#v, errors = %#v", records, errors)
	}
}

func replayResult(verdict, ruleID, severity, trigger, evidence string, agents, verifiers int) quality.ReviewResult {
	result := quality.ReviewResult{
		SchemaVersion: 1, PolicyVersion: "1.1.0",
		Execution:    quality.Execution{Host: "claude-code", SkillVersion: "0.1.0", AgentCount: agents, VerifierCount: verifiers},
		Adjudication: quality.Adjudication{SemanticResult: verdict, RolloutMode: "report_only", CIAction: "publish_report", Reasons: []string{"fixture"}},
		Findings:     []quality.AdjudicatedFinding{},
	}
	if ruleID != "" {
		result.Findings = []quality.AdjudicatedFinding{{Candidate: quality.Finding{
			RuleID: ruleID, Severity: severity, TriggerConfidence: trigger, EvidenceLevel: evidence,
			AffectedCallPath: []string{"Entry", "Changed"}, TriggerCondition: "fixture",
		}}}
	}
	return result
}

func findReplayCase(t *testing.T, report ReplayReport, id string) ReplayCaseReport {
	t.Helper()
	for _, item := range report.Cases {
		if item.CaseID == id {
			return item
		}
	}
	t.Fatalf("case %s not found", id)
	return ReplayCaseReport{}
}

func findCase(t *testing.T, manifest Manifest, id string) Case {
	t.Helper()
	for _, item := range manifest.Cases {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("case %s not found", id)
	return Case{}
}
