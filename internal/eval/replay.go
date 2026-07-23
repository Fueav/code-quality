package eval

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type ReplayRecord struct {
	SchemaVersion int         `json:"schema_version"`
	PolicyVersion string      `json:"policy_version"`
	CaseID        string      `json:"case_id"`
	Host          string      `json:"host"`
	RunNumber     int         `json:"run_number"`
	Observed      Observed    `json:"observed"`
	HumanReview   HumanReview `json:"human_review"`
}

type Observed struct {
	SemanticResult      string   `json:"semantic_result"`
	RuleIDs             []string `json:"rule_ids"`
	Severity            *string  `json:"severity"`
	TriggerConfidence   *string  `json:"trigger_confidence"`
	EvidenceLevel       *string  `json:"evidence_level"`
	DuplicateRootCauses int      `json:"duplicate_root_causes"`
	AgentCount          int      `json:"agent_count"`
	VerifierCount       int      `json:"verifier_count"`
	InputTokens         *int     `json:"input_tokens"`
	OutputTokens        *int     `json:"output_tokens"`
	DurationMS          *int     `json:"duration_ms"`
}

type HumanReview struct {
	Status         string  `json:"status"`
	OverturnReason *string `json:"overturn_reason"`
}

type ReplayReport struct {
	SchemaVersion           int                `json:"schema_version"`
	PolicyVersion           string             `json:"policy_version"`
	Suite                   string             `json:"suite"`
	TotalRecords            int                `json:"total_records"`
	ValidRecords            int                `json:"valid_records"`
	InvalidRecords          int                `json:"invalid_records"`
	CasesCovered            int                `json:"cases_covered"`
	PositiveCasesDetected   int                `json:"positive_cases_detected"`
	MetricsAvailable        int                `json:"metrics_available"`
	AgentLimitRespected     bool               `json:"agent_limit_respected"`
	ReportOnlySmokeComplete bool               `json:"report_only_smoke_complete"`
	Hosts                   []string           `json:"hosts"`
	Cases                   []ReplayCaseReport `json:"cases"`
	Errors                  []string           `json:"errors"`
}

type ReplayCaseReport struct {
	CaseID         string   `json:"case_id"`
	Kind           string   `json:"kind"`
	RequiredRuns   int      `json:"required_runs"`
	ObservedRuns   int      `json:"observed_runs"`
	HumanConfirmed bool     `json:"human_confirmed"`
	SmokeMatched   bool     `json:"smoke_matched"`
	Errors         []string `json:"errors"`
}

func RecordFromResult(caseID, host string, runNumber int, result quality.ReviewResult, human HumanReview) ReplayRecord {
	ruleIDs := make([]string, 0, len(result.Findings))
	var severity, trigger, evidence *string
	for index, finding := range result.Findings {
		ruleIDs = append(ruleIDs, finding.Candidate.RuleID)
		if index == 0 {
			severity = stringPointer(finding.Candidate.Severity)
			trigger = stringPointer(finding.Candidate.TriggerConfidence)
			evidence = stringPointer(finding.Candidate.EvidenceLevel)
		}
	}
	sort.Strings(ruleIDs)
	return ReplayRecord{
		SchemaVersion: 1, PolicyVersion: result.PolicyVersion, CaseID: caseID, Host: host, RunNumber: runNumber,
		Observed: Observed{
			SemanticResult: result.Adjudication.SemanticResult,
			RuleIDs:        ruleIDs, Severity: severity, TriggerConfidence: trigger, EvidenceLevel: evidence,
			DuplicateRootCauses: duplicateRootCauses(result.Findings),
			AgentCount:          result.Execution.AgentCount, VerifierCount: result.Execution.VerifierCount,
			InputTokens: result.Execution.InputTokens, OutputTokens: result.Execution.OutputTokens, DurationMS: result.Execution.DurationMS,
		},
		HumanReview: human,
	}
}

func duplicateRootCauses(findings []quality.AdjudicatedFinding) int {
	seen := map[string]struct{}{}
	duplicates := 0
	for _, finding := range findings {
		candidate := finding.Candidate
		key := candidate.RuleID + "|" + strings.Join(candidate.AffectedCallPath, ">") + "|" + candidate.TriggerCondition
		if _, exists := seen[key]; exists {
			duplicates++
		}
		seen[key] = struct{}{}
	}
	return duplicates
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func LoadReplayRecords(directory string) ([]ReplayRecord, []string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, []string{err.Error()}
	}
	records := []ReplayRecord{}
	errorsFound := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		raw, err := readReplayFile(path)
		if err != nil {
			errorsFound = append(errorsFound, entry.Name()+": "+err.Error())
			continue
		}
		record, err := quality.DecodeStrict[ReplayRecord](bytes.NewReader(raw))
		if err != nil {
			errorsFound = append(errorsFound, entry.Name()+": "+err.Error())
			continue
		}
		records = append(records, record)
	}
	return records, unique(errorsFound)
}

const maxReplayBytes = int64(1 << 20)

func readReplayFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > maxReplayBytes {
		return nil, fmt.Errorf("invalid replay file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > maxReplayBytes {
		return nil, fmt.Errorf("replay file changed while it was being read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxReplayBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != after.Size() || int64(len(raw)) > maxReplayBytes {
		return nil, fmt.Errorf("replay file changed while it was being read")
	}
	return raw, nil
}

func RunReplay(manifest Manifest, policy quality.PolicyManifest, records []ReplayRecord, loadErrors []string) ReplayReport {
	report := ReplayReport{
		SchemaVersion: 1, PolicyVersion: policy.PolicyVersion, Suite: "host_session_replay",
		TotalRecords: len(records) + len(loadErrors), AgentLimitRespected: true,
		Cases: []ReplayCaseReport{}, Errors: append([]string{}, loadErrors...), Hosts: []string{},
	}
	caseByID := make(map[string]Case, len(manifest.Cases))
	for _, item := range manifest.Cases {
		caseByID[item.ID] = item
	}
	grouped := map[string][]ReplayRecord{}
	seen := map[string]struct{}{}
	hosts := map[string]struct{}{}
	for index, record := range records {
		errorsFound := validateReplayRecord(record, caseByID, policy)
		key := fmt.Sprintf("%s/%s/%d", record.Host, record.CaseID, record.RunNumber)
		if _, exists := seen[key]; exists {
			errorsFound = append(errorsFound, "replay identity is duplicated")
		}
		seen[key] = struct{}{}
		if record.Observed.AgentCount > policy.AgentLimit || record.Observed.VerifierCount > 1 || record.Observed.VerifierCount > record.Observed.AgentCount-1 {
			report.AgentLimitRespected = false
		}
		if len(errorsFound) > 0 {
			report.InvalidRecords++
			for _, message := range errorsFound {
				report.Errors = append(report.Errors, fmt.Sprintf("records[%d]: %s", index, message))
			}
			continue
		}
		report.ValidRecords++
		if record.Observed.InputTokens != nil && record.Observed.OutputTokens != nil && record.Observed.DurationMS != nil {
			report.MetricsAvailable++
		}
		hosts[record.Host] = struct{}{}
		grouped[record.CaseID] = append(grouped[record.CaseID], record)
	}

	caseIDs := make([]string, 0, len(manifest.Cases))
	for _, item := range manifest.Cases {
		caseIDs = append(caseIDs, item.ID)
	}
	sort.Strings(caseIDs)
	for _, caseID := range caseIDs {
		item := caseByID[caseID]
		runs := grouped[caseID]
		caseReport := evaluateReplayCase(item, runs)
		report.Cases = append(report.Cases, caseReport)
		if len(runs) > 0 {
			report.CasesCovered++
		}
		if item.Kind == "positive" && caseReport.ObservedRuns == caseReport.RequiredRuns && caseReport.SmokeMatched {
			report.PositiveCasesDetected++
		}
	}
	for host := range hosts {
		report.Hosts = append(report.Hosts, host)
	}
	sort.Strings(report.Hosts)
	report.Errors = unique(report.Errors)
	report.ReportOnlySmokeComplete = report.InvalidRecords == 0 && report.CasesCovered == len(manifest.Cases) && report.PositiveCasesDetected == countKind(manifest, "positive") && report.MetricsAvailable == len(manifest.Cases) && report.AgentLimitRespected && allReplayCasesSmokeReady(report.Cases) && hostsQualified(report.Hosts)
	return report
}

func validateReplayRecord(record ReplayRecord, cases map[string]Case, policy quality.PolicyManifest) []string {
	var errorsFound []string
	item, exists := cases[record.CaseID]
	if record.SchemaVersion != 1 || record.PolicyVersion != policy.PolicyVersion {
		errorsFound = append(errorsFound, "schema or policy version is invalid")
	}
	if !exists {
		errorsFound = append(errorsFound, "case_id is unknown")
	}
	if record.Host != "claude-code" && record.Host != "codex" {
		errorsFound = append(errorsFound, "host is invalid")
	}
	if record.RunNumber != 1 {
		errorsFound = append(errorsFound, "report-only smoke run_number must be 1")
	}
	if !validResult(record.Observed.SemanticResult) || hasBlank(record.Observed.RuleIDs) || hasDuplicates(record.Observed.RuleIDs) {
		errorsFound = append(errorsFound, "observed verdict or rule ids are invalid")
	}
	if record.Observed.DuplicateRootCauses < 0 || record.Observed.AgentCount < 1 || record.Observed.AgentCount > policy.AgentLimit || record.Observed.VerifierCount < 0 || record.Observed.VerifierCount > 1 || record.Observed.VerifierCount > record.Observed.AgentCount-1 || negative(record.Observed.InputTokens) || negative(record.Observed.OutputTokens) || negative(record.Observed.DurationMS) {
		errorsFound = append(errorsFound, "observed execution metrics are invalid")
	}
	if !validAxis(record.Observed.Severity, "S1", "S2", "S3") || !validAxis(record.Observed.TriggerConfidence, "T1", "T2", "T3") || !validAxis(record.Observed.EvidenceLevel, "E1", "E2", "E3") {
		errorsFound = append(errorsFound, "observed S/T/E axes are invalid")
	}
	if record.HumanReview.Status != "pending" && record.HumanReview.Status != "confirmed" && record.HumanReview.Status != "overturned" {
		errorsFound = append(errorsFound, "human review status is invalid")
	}
	if record.HumanReview.Status != "overturned" && record.HumanReview.OverturnReason != nil {
		errorsFound = append(errorsFound, "only overturned human review may have an overturn reason")
	}
	if record.HumanReview.Status == "overturned" && (record.HumanReview.OverturnReason == nil || strings.TrimSpace(*record.HumanReview.OverturnReason) == "") {
		errorsFound = append(errorsFound, "overturned human review requires a reason")
	}
	if exists && item.Kind == "counterexample" && (record.Observed.Severity != nil || record.Observed.TriggerConfidence != nil || record.Observed.EvidenceLevel != nil) && len(record.Observed.RuleIDs) == 0 {
		errorsFound = append(errorsFound, "counterexample axes require an observed finding")
	}
	return unique(errorsFound)
}

func evaluateReplayCase(item Case, runs []ReplayRecord) ReplayCaseReport {
	required := 1
	result := ReplayCaseReport{
		CaseID: item.ID, Kind: item.Kind, RequiredRuns: required, ObservedRuns: len(runs),
		HumanConfirmed: len(runs) > 0, SmokeMatched: len(runs) > 0, Errors: []string{},
	}
	for _, run := range runs {
		if run.HumanReview.Status != "confirmed" {
			result.HumanConfirmed = false
		}
		if !matchesSmokeExpectation(item, run) || run.Observed.DuplicateRootCauses != 0 {
			result.SmokeMatched = false
		}
	}
	if len(runs) != required {
		result.Errors = append(result.Errors, fmt.Sprintf("requires exactly %d run(s), found %d", required, len(runs)))
	}
	if len(runs) > 0 && !result.SmokeMatched {
		result.Errors = append(result.Errors, "observed result violates the coarse report-only smoke expectation or has duplicate roots")
	}
	return result
}

func matchesSmokeExpectation(item Case, record ReplayRecord) bool {
	switch item.Kind {
	case "positive":
		return record.Observed.SemanticResult == quality.ResultBlock && len(record.Observed.RuleIDs) > 0 && record.Observed.VerifierCount == 1
	case "counterexample", "insufficient":
		return record.Observed.SemanticResult == quality.ResultPass || record.Observed.SemanticResult == quality.ResultManualReview
	default:
		return false
	}
}

func hostsQualified(hosts []string) bool {
	return len(hosts) == 1 && hosts[0] == "codex"
}

func allReplayCasesSmokeReady(cases []ReplayCaseReport) bool {
	for _, item := range cases {
		if item.ObservedRuns != item.RequiredRuns || !item.SmokeMatched {
			return false
		}
	}
	return true
}

func countKind(manifest Manifest, kind string) int {
	count := 0
	for _, item := range manifest.Cases {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func validResult(value string) bool {
	return value == quality.ResultPass || value == quality.ResultManualReview || value == quality.ResultBlock || value == quality.ResultIncomplete
}

func hasDuplicates(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func negative(value *int) bool { return value != nil && *value < 0 }

func validAxis(value *string, allowed ...string) bool {
	if value == nil {
		return true
	}
	for _, candidate := range allowed {
		if *value == candidate {
			return true
		}
	}
	return false
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
