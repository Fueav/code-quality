package eval

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestDeterministicMatrixCoversEveryRuleAndBoundary(t *testing.T) {
	manifest, err := LoadManifest(filepath.Join("..", "..", "evals", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy := testPolicy()
	if errors := ValidateManifest(manifest, policy); len(errors) > 0 {
		t.Fatalf("manifest errors = %#v", errors)
	}
	report := RunDeterministic(manifest, policy)
	if report.TotalCases != 60 || report.PassedCases != 60 || report.FailedCases != 0 {
		t.Fatalf("report counts = %#v", report)
	}
	if report.RulesCovered != 20 || !report.MatrixComplete || report.StableSevereCases != 20 || report.SevereRepetitions != 3 || !report.AllRulesReportOnly {
		t.Fatalf("report contract = %#v", report)
	}
	for _, item := range report.Cases {
		if !item.Passed || !item.Stable {
			t.Fatalf("case failed: %#v", item)
		}
		if item.Kind == "positive" && item.Repetitions != 3 {
			t.Fatalf("positive case repetition = %#v", item)
		}
	}
}

func TestValidateManifestRejectsBrokenMatrix(t *testing.T) {
	manifest, err := LoadManifest(filepath.Join("..", "..", "evals", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Cases = manifest.Cases[:len(manifest.Cases)-1]
	manifest.Cases[0].Dimension = "D4"
	manifest.Cases[1].ID = manifest.Cases[0].ID
	errors := ValidateManifest(manifest, testPolicy())
	for _, expected := range []string{"exactly 60 cases", "dimension does not match policy", "id is duplicated", "CHG-002 must define exactly one insufficient"} {
		if !containsError(errors, expected) {
			t.Fatalf("errors = %#v, want %q", errors, expected)
		}
	}
}

func TestValidateManifestRejectsNonCanonicalCaseID(t *testing.T) {
	manifest, err := LoadManifest(filepath.Join("..", "..", "evals", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Cases[0].ID = "renamed-positive-case"
	if errors := ValidateManifest(manifest, testPolicy()); !containsError(errors, "id must match rule_id and kind") {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestDeterministicEvalReportsNonReportOnlyPolicy(t *testing.T) {
	manifest, err := LoadManifest(filepath.Join("..", "..", "evals", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy := testPolicy()
	policy.Rules[0].Status = "block_eligible"
	report := RunDeterministic(manifest, policy)
	if report.AllRulesReportOnly {
		t.Fatal("non-report-only policy was accepted for the V1 pilot")
	}
}

func testPolicy() quality.PolicyManifest {
	ids := []string{
		"DES-001", "DES-002", "DES-003", "DES-004", "DES-005",
		"COR-001", "COR-002", "COR-003", "COR-004", "COR-005",
		"REL-001", "REL-002", "REL-003", "REL-004", "REL-005",
		"SEC-001", "SEC-002", "SEC-003", "CHG-001", "CHG-002",
	}
	dimensions := map[string]string{}
	for _, id := range ids[:5] {
		dimensions[id] = "D1"
	}
	for _, id := range ids[5:10] {
		dimensions[id] = "D2"
	}
	for _, id := range ids[10:15] {
		dimensions[id] = "D3"
	}
	for _, id := range ids[15:] {
		dimensions[id] = "D4"
	}
	rules := make([]quality.PolicyRule, 0, len(ids))
	for _, id := range ids {
		rules = append(rules, quality.PolicyRule{ID: id, Dimension: dimensions[id], Status: "report_only"})
	}
	return quality.PolicyManifest{SchemaVersion: 1, PolicyVersion: "1.1.1", Rubric: "policy/v1.1/rubric.md", AgentLimit: 2, Rules: rules}
}

func containsError(errors []string, substring string) bool {
	for _, value := range errors {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}
