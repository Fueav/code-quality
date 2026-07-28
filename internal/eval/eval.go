package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

const SevereRepetitions = 3

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	PolicyVersion string `json:"policy_version"`
	SevereRepeats int    `json:"severe_repetitions"`
	Cases         []Case `json:"cases"`
}

type Case struct {
	ID        string   `json:"id"`
	RuleID    string   `json:"rule_id"`
	Dimension string   `json:"dimension"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Fixture   Fixture  `json:"fixture"`
	Expected  Expected `json:"expected"`
}

type Fixture struct {
	Language string   `json:"language"`
	Before   string   `json:"before"`
	After    string   `json:"after"`
	Facts    []string `json:"facts"`
}

type Expected struct {
	SemanticResult    string `json:"semantic_result"`
	FindingCount      int    `json:"finding_count"`
	Severity          string `json:"severity"`
	TriggerConfidence string `json:"trigger_confidence"`
	EvidenceLevel     string `json:"evidence_level"`
	VerifierResult    string `json:"verifier_result"`
}

type Report struct {
	SchemaVersion      int          `json:"schema_version"`
	PolicyVersion      string       `json:"policy_version"`
	Suite              string       `json:"suite"`
	TotalCases         int          `json:"total_cases"`
	PassedCases        int          `json:"passed_cases"`
	FailedCases        int          `json:"failed_cases"`
	RulesCovered       int          `json:"rules_covered"`
	MatrixComplete     bool         `json:"matrix_complete"`
	SevereRepetitions  int          `json:"severe_repetitions"`
	StableSevereCases  int          `json:"stable_severe_cases"`
	AllRulesReportOnly bool         `json:"all_rules_report_only"`
	Cases              []CaseReport `json:"cases"`
}

type CaseReport struct {
	ID             string   `json:"id"`
	RuleID         string   `json:"rule_id"`
	Kind           string   `json:"kind"`
	ExpectedResult string   `json:"expected_result"`
	ActualResult   string   `json:"actual_result"`
	Repetitions    int      `json:"repetitions"`
	Stable         bool     `json:"stable"`
	Passed         bool     `json:"passed"`
	Errors         []string `json:"errors"`
}

func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return quality.DecodeStrict[Manifest](bytes.NewReader(raw))
}

func ValidateManifest(manifest Manifest, policy quality.PolicyManifest) []string {
	var errors []string
	if manifest.SchemaVersion != 1 {
		errors = append(errors, "eval manifest schema_version must be 1")
	}
	if manifest.PolicyVersion != policy.PolicyVersion {
		errors = append(errors, "eval manifest policy_version does not match policy")
	}
	if manifest.SevereRepeats != SevereRepetitions {
		errors = append(errors, "eval manifest severe_repetitions must be 3")
	}
	if len(manifest.Cases) != len(policy.Rules)*3 {
		errors = append(errors, fmt.Sprintf("eval manifest must contain exactly %d cases", len(policy.Rules)*3))
	}
	known := make(map[string]string, len(policy.Rules))
	for _, rule := range policy.Rules {
		known[rule.ID] = rule.Dimension
	}
	seenIDs := map[string]struct{}{}
	matrix := map[string]map[string]int{}
	for index, item := range manifest.Cases {
		prefix := fmt.Sprintf("cases[%d]", index)
		if strings.TrimSpace(item.ID) == "" {
			errors = append(errors, prefix+".id is required")
		} else if _, exists := seenIDs[item.ID]; exists {
			errors = append(errors, prefix+".id is duplicated")
		} else {
			seenIDs[item.ID] = struct{}{}
		}
		if item.ID != item.RuleID+"-"+item.Kind {
			errors = append(errors, prefix+".id must match rule_id and kind")
		}
		dimension, exists := known[item.RuleID]
		if !exists {
			errors = append(errors, prefix+".rule_id is unknown")
		} else if item.Dimension != dimension {
			errors = append(errors, prefix+".dimension does not match policy")
		}
		if item.Kind != "positive" && item.Kind != "counterexample" && item.Kind != "insufficient" {
			errors = append(errors, prefix+".kind is invalid")
		}
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Fixture.Language) == "" || strings.TrimSpace(item.Fixture.Before) == "" || strings.TrimSpace(item.Fixture.After) == "" || len(item.Fixture.Facts) == 0 || hasBlank(item.Fixture.Facts) {
			errors = append(errors, prefix+" fixture is incomplete")
		}
		if _, exists := matrix[item.RuleID]; !exists {
			matrix[item.RuleID] = map[string]int{}
		}
		matrix[item.RuleID][item.Kind]++
		errors = append(errors, validateExpected(prefix, item)...)
	}
	for ruleID := range known {
		for _, kind := range []string{"positive", "counterexample", "insufficient"} {
			if matrix[ruleID][kind] != 1 {
				errors = append(errors, fmt.Sprintf("rule %s must define exactly one %s case", ruleID, kind))
			}
		}
	}
	return unique(errors)
}

func validateExpected(prefix string, item Case) []string {
	var errors []string
	expected := item.Expected
	switch item.Kind {
	case "positive", "insufficient":
		if expected.SemanticResult != quality.ResultManualReview || expected.FindingCount != 1 {
			errors = append(errors, prefix+" expectation must be MANUAL_REVIEW with exactly one finding")
		}
	case "counterexample":
		if expected.SemanticResult != quality.ResultPass || expected.FindingCount != 0 {
			errors = append(errors, prefix+" counterexample expectation must be PASS without findings")
		}
	}
	return errors
}

func RunDeterministic(manifest Manifest, policy quality.PolicyManifest) Report {
	report := Report{
		SchemaVersion:      1,
		PolicyVersion:      policy.PolicyVersion,
		Suite:              "deterministic_adjudication",
		TotalCases:         len(manifest.Cases),
		SevereRepetitions:  manifest.SevereRepeats,
		AllRulesReportOnly: allReportOnly(policy),
		Cases:              make([]CaseReport, 0, len(manifest.Cases)),
	}
	manifestErrors := ValidateManifest(manifest, policy)
	if len(manifestErrors) > 0 {
		report.FailedCases = len(manifest.Cases)
		report.Cases = append(report.Cases, CaseReport{ID: "manifest", Passed: false, Stable: false, Errors: manifestErrors})
		return report
	}

	rules := map[string]struct{}{}
	for _, item := range manifest.Cases {
		rules[item.RuleID] = struct{}{}
		caseReport := runCase(item, policy, manifest.SevereRepeats)
		report.Cases = append(report.Cases, caseReport)
		if caseReport.Passed {
			report.PassedCases++
		} else {
			report.FailedCases++
		}
		if item.Kind == "positive" && caseReport.Stable {
			report.StableSevereCases++
		}
	}
	report.RulesCovered = len(rules)
	report.MatrixComplete = report.TotalCases == len(policy.Rules)*3 && report.RulesCovered == len(policy.Rules)
	return report
}

func runCase(item Case, policy quality.PolicyManifest, repeats int) CaseReport {
	count := 1
	if item.Kind == "positive" {
		count = repeats
	}
	result := CaseReport{
		ID: item.ID, RuleID: item.RuleID, Kind: item.Kind,
		ExpectedResult: item.Expected.SemanticResult,
		Repetitions:    count, Stable: true, Passed: true, Errors: []string{},
	}
	var canonical []byte
	for repetition := 0; repetition < count; repetition++ {
		actual := adjudicateCase(item, policy)
		result.ActualResult = actual.Adjudication.SemanticResult
		encoded, err := json.Marshal(actual)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			result.Passed = false
			continue
		}
		if repetition == 0 {
			canonical = encoded
		} else if !bytes.Equal(canonical, encoded) {
			result.Stable = false
			result.Passed = false
			result.Errors = append(result.Errors, "deterministic result changed between repetitions")
		}
		if actual.Adjudication.SemanticResult != item.Expected.SemanticResult || len(actual.Findings) != item.Expected.FindingCount {
			result.Passed = false
			result.Errors = append(result.Errors, "actual verdict or finding count does not match expectation")
		}
		if validationErrors := quality.ValidateResult(actual, policy); len(validationErrors) > 0 {
			result.Passed = false
			result.Errors = append(result.Errors, validationErrors...)
		}
	}
	result.Errors = unique(result.Errors)
	return result
}

func adjudicateCase(item Case, policy quality.PolicyManifest) quality.ReviewResult {
	review := quality.ModelReview{
		ActivatedRuleFamilies: []string{item.Dimension},
		InactiveRuleFamilies:  inactiveFamilies(item.Dimension),
		Findings:              []quality.Finding{},
		UninspectedScope:      []string{},
		MissingContext:        []string{},
		InspectedContext:      []quality.InspectedContext{{Path: "app.go", Purpose: "Evaluate the changed production path."}},
		Execution:             quality.Execution{Host: "claude-code", SkillVersion: quality.SkillVersion, AgentCount: 1},
	}
	if item.Expected.FindingCount == 1 {
		review.Findings = []quality.Finding{{
			ID:               "F-001",
			RuleID:           item.RuleID,
			CodeLocations:    []quality.CodeLocation{{Path: "app.go", Line: 3}},
			ProductionImpact: "The case-specific V1.2 production-floor impact occurs.",
			MinimalFix:       "Restore the protected behavior described by the fixture.",
		}}
	}
	return quality.Adjudicate(evalRequest(item.ID), review, policy)
}

func evalRequest(id string) quality.ReviewRequest {
	return quality.ReviewRequest{
		Repository: "eval/" + id, TargetBranch: "main",
		BaseCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40),
		DiffSelectionReason: "deterministic_eval", ChangedFiles: []string{"app.go"},
		AffectedEntries: []string{"Entry"},
	}
}

func inactiveFamilies(active string) []quality.InactiveRuleFamily {
	result := []quality.InactiveRuleFamily{}
	for _, dimension := range []string{"D1", "D2", "D3", "D4"} {
		if dimension != active {
			result = append(result, quality.InactiveRuleFamily{ID: dimension, Reason: "The fixture does not change this dimension."})
		}
	}
	return result
}

func allReportOnly(policy quality.PolicyManifest) bool {
	for _, rule := range policy.Rules {
		if rule.Status != "report_only" {
			return false
		}
	}
	return true
}

func hasBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
