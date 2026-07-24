package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestRecordFromResultUsesAuthoritativeFields(t *testing.T) {
	result := replayResult(quality.ResultManualReview, "DES-003", 1, 0)
	record := RecordFromResult("DES-003-positive", "claude-code", 1, result, HumanReview{Status: "pending"})
	if record.Observed.SemanticResult != quality.ResultManualReview || len(record.Observed.RuleIDs) != 1 || record.Observed.RuleIDs[0] != "DES-003" {
		t.Fatalf("record = %#v", record)
	}
	if record.Observed.Severity != nil || record.Observed.TriggerConfidence != nil || record.Observed.EvidenceLevel != nil || record.Observed.AgentCount != 1 || record.Observed.VerifierCount != 0 {
		t.Fatalf("observed = %#v", record.Observed)
	}
}

func TestReplayReportOnlySmokeDoesNotRequireHumanConfirmationOrExactRule(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	policy := testPolicy()
	record := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.1", CaseID: "DES-003-positive", Host: "claude-code", RunNumber: 1,
		Observed: Observed{
			SemanticResult: quality.ResultManualReview, RuleIDs: []string{"DES-004"},
			Severity: stringPointer("S3"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
			AgentCount: 1, VerifierCount: 0,
		},
		HumanReview: HumanReview{Status: "pending"},
	}
	report := RunReplay(manifest, policy, []ReplayRecord{record}, nil)
	if report.ReportOnlySmokeComplete || report.CasesCovered != 1 || report.PositiveCasesDetected != 1 || report.ValidRecords != 1 {
		t.Fatalf("report = %#v", report)
	}
	caseReport := findReplayCase(t, report, "DES-003-positive")
	if caseReport.HumanConfirmed || !caseReport.SmokeMatched || caseReport.ObservedRuns != 1 || caseReport.RequiredRuns != 1 {
		t.Fatalf("case report = %#v", caseReport)
	}
}

func TestReplayReportOnlySmokeCompletesWithOnePendingRunPerCase(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	metric := 1
	records := make([]ReplayRecord, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		observed := Observed{
			SemanticResult: quality.ResultPass,
			RuleIDs:        []string{},
			AgentCount:     1,
		}
		if item.Kind == "positive" {
			observed = Observed{
				SemanticResult:    quality.ResultManualReview,
				RuleIDs:           []string{item.RuleID},
				Severity:          stringPointer("S3"),
				TriggerConfidence: stringPointer("T3"),
				EvidenceLevel:     stringPointer("E2"),
				AgentCount:        1,
				VerifierCount:     0,
			}
		} else if item.Kind == "counterexample" {
			observed.SemanticResult = quality.ResultManualReview
			observed.RuleIDs = []string{item.RuleID}
			observed.Severity = stringPointer("S3")
			observed.TriggerConfidence = stringPointer("T2")
			observed.EvidenceLevel = stringPointer("E1")
		}
		records = append(records, ReplayRecord{
			SchemaVersion: 1,
			PolicyVersion: "1.1.1",
			CaseID:        item.ID,
			Host:          "codex",
			RunNumber:     1,
			Observed:      observed,
			HumanReview:   HumanReview{Status: "pending"},
		})
		records[len(records)-1].Observed.InputTokens = &metric
		records[len(records)-1].Observed.OutputTokens = &metric
		records[len(records)-1].Observed.DurationMS = &metric
	}
	report := RunReplay(manifest, testPolicy(), records, nil)
	if !report.ReportOnlySmokeComplete || report.ValidRecords != 60 || report.CasesCovered != 60 || report.PositiveCasesDetected != 20 {
		t.Fatalf("report = %#v", report)
	}
	records[0].Observed.InputTokens = nil
	if RunReplay(manifest, testPolicy(), records, nil).ReportOnlySmokeComplete {
		t.Fatal("report-only smoke must retain metrics for every run")
	}
}

func TestReplayQualificationUsesOnlyLocalCodex(t *testing.T) {
	if hostsQualified([]string{"claude-code"}) || !hostsQualified([]string{"codex"}) || hostsQualified([]string{"claude-code", "codex"}) {
		t.Fatal("host qualification must use only Codex")
	}
}

func TestReplaySmokeUsesCoarseRiskExpectations(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	positive := findCase(t, manifest, "DES-003-positive")
	positiveRecord := ReplayRecord{Observed: Observed{
		SemanticResult: quality.ResultManualReview,
		RuleIDs:        []string{"DES-003"},
		Severity:       stringPointer("S3"), TriggerConfidence: stringPointer("T3"), EvidenceLevel: stringPointer("E2"),
	}}
	if !matchesSmokeExpectation(positive, positiveRecord) {
		t.Fatal("report-only positive replay should match without a verifier")
	}
	positiveRecord.Observed.RuleIDs = []string{"DES-004"}
	positiveRecord.Observed.Severity = stringPointer("S2")
	positiveRecord.Observed.TriggerConfidence = stringPointer("T2")
	positiveRecord.Observed.EvidenceLevel = stringPointer("E1")
	if !matchesSmokeExpectation(positive, positiveRecord) {
		t.Fatal("positive replay should match without exact rule or S/T/E grading")
	}

	counterexample := findCase(t, manifest, "DES-003-counterexample")
	counterexampleRecord := ReplayRecord{Observed: Observed{
		SemanticResult: quality.ResultManualReview,
		RuleIDs:        []string{"DES-003"},
		VerifierCount:  1,
	}}
	if !matchesSmokeExpectation(counterexample, counterexampleRecord) {
		t.Fatal("counterexample smoke should accept a non-blocking result")
	}

	insufficient := findCase(t, manifest, "DES-003-insufficient")
	if !matchesSmokeExpectation(insufficient, ReplayRecord{Observed: Observed{SemanticResult: quality.ResultPass}}) {
		t.Fatal("insufficient-evidence smoke should accept PASS when it does not claim high risk")
	}
}

func TestReplayRejectsDuplicateIdentityAndAgentOverflow(t *testing.T) {
	manifest, err := LoadManifest("../../evals/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	record := ReplayRecord{
		SchemaVersion: 1, PolicyVersion: "1.1.1", CaseID: "DES-003-positive", Host: "codex", RunNumber: 1,
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
		SchemaVersion: 1, PolicyVersion: "1.1.1", CaseID: "DES-003-counterexample", Host: "claude-code", RunNumber: 1,
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
		SchemaVersion: 1, PolicyVersion: "1.1.1", CaseID: "DES-003-positive", Host: "codex", RunNumber: 1,
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

func replayResult(verdict, ruleID string, agents, verifiers int) quality.ReviewResult {
	result := quality.ReviewResult{
		SchemaVersion: 1, PolicyVersion: "1.1.1",
		Execution:    quality.Execution{Host: "claude-code", SkillVersion: "0.1.1", AgentCount: agents, VerifierCount: verifiers},
		Adjudication: quality.Adjudication{SemanticResult: verdict, RolloutMode: "report_only", CIAction: "publish_report", Reasons: []string{"fixture"}},
		Findings:     []quality.AdjudicatedFinding{},
	}
	if ruleID != "" {
		result.Findings = []quality.AdjudicatedFinding{{Candidate: quality.Finding{
			RuleID:        ruleID,
			CodeLocations: []quality.CodeLocation{{Path: "app.go", Line: 3}},
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
