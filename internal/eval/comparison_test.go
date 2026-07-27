package eval

import (
	"reflect"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestCompareFindingsProducesThreePartitionsAndJudgmentRows(t *testing.T) {
	product := FindingSet{
		SchemaVersion: 1,
		Source:        "code-quality",
		Findings: []ComparisonFinding{
			comparisonFinding("P-001", "shared-auth", "D4", "auth.go", 42, "Disabled accounts can be reactivated."),
			comparisonFinding("P-002", "product-pagination", "D2", "list.go", 18, "Pagination can repeat rows."),
		},
	}
	baseline := FindingSet{
		SchemaVersion: 1,
		Source:        "external-baseline",
		Findings: []ComparisonFinding{
			comparisonFinding("B-001", "shared-auth", "D4", "auth.go", 42, "The callback overrides disabled state."),
			comparisonFinding("B-002", "baseline-timeout", "D3", "worker.go", 77, "The worker has no timeout."),
		},
	}

	report, err := CompareFindings(product, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if report.PrimaryMetric != "finding_comparison" || len(report.ProductOnly) != 1 || len(report.BaselineOnly) != 1 || len(report.Shared) != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.ProductOnly[0].Description != "Pagination can repeat rows." || report.BaselineOnly[0].CodeLocations[0].Path != "worker.go" {
		t.Fatalf("locations or descriptions were not preserved: %#v", report)
	}
	shared := report.Shared[0]
	if shared.ComparisonKey != "shared-auth" || shared.Product.ID != "P-001" || shared.Baseline.ID != "B-001" {
		t.Fatalf("shared = %#v", shared)
	}
	if len(report.HumanJudgmentTemplate) != 3 {
		t.Fatalf("judgment rows = %#v", report.HumanJudgmentTemplate)
	}
	for _, row := range report.HumanJudgmentTemplate {
		if row.Judgment != "" || !reflect.DeepEqual(row.Options, []string{"adopted", "noise", "uncertain"}) {
			t.Fatalf("judgment row = %#v", row)
		}
	}
}

func TestCompareFindingsRejectsAmbiguousOrIncompleteInput(t *testing.T) {
	valid := comparisonFinding("P-001", "same-key", "D1", "app.go", 3, "A material behavior changes.")
	for name, product := range map[string]FindingSet{
		"duplicate comparison key": {SchemaVersion: 1, Source: "product", Findings: []ComparisonFinding{valid, comparisonFinding("P-002", "same-key", "D2", "other.go", 4, "Another issue.")}},
		"missing description":      {SchemaVersion: 1, Source: "product", Findings: []ComparisonFinding{comparisonFinding("P-001", "key", "D1", "app.go", 3, "")}},
		"invalid dimension":        {SchemaVersion: 1, Source: "product", Findings: []ComparisonFinding{comparisonFinding("P-001", "key", "D9", "app.go", 3, "Issue.")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CompareFindings(product, FindingSet{SchemaVersion: 1, Source: "baseline", Findings: []ComparisonFinding{}}); err == nil {
				t.Fatal("expected comparison input to be rejected")
			}
		})
	}
}

func comparisonFinding(id, key, dimension, path string, line int, description string) ComparisonFinding {
	return ComparisonFinding{
		ID:            id,
		ComparisonKey: key,
		Dimension:     dimension,
		CodeLocations: []quality.CodeLocation{{Path: path, Line: line}},
		Description:   description,
	}
}
