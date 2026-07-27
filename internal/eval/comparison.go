package eval

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type FindingSet struct {
	SchemaVersion int                 `json:"schema_version"`
	Source        string              `json:"source"`
	Findings      []ComparisonFinding `json:"findings"`
}

type ComparisonFinding struct {
	ID            string                 `json:"id"`
	ComparisonKey string                 `json:"comparison_key"`
	Dimension     string                 `json:"dimension"`
	CodeLocations []quality.CodeLocation `json:"code_locations"`
	Description   string                 `json:"description"`
}

type SharedFinding struct {
	ComparisonKey string            `json:"comparison_key"`
	Product       ComparisonFinding `json:"product"`
	Baseline      ComparisonFinding `json:"baseline"`
}

type HumanJudgmentRow struct {
	RowID             string   `json:"row_id"`
	Partition         string   `json:"partition"`
	ComparisonKey     string   `json:"comparison_key"`
	ProductFindingID  string   `json:"product_finding_id,omitempty"`
	BaselineFindingID string   `json:"baseline_finding_id,omitempty"`
	Judgment          string   `json:"judgment"`
	Options           []string `json:"options"`
}

type ComparisonReport struct {
	SchemaVersion         int                 `json:"schema_version"`
	PrimaryMetric         string              `json:"primary_metric"`
	ProductSource         string              `json:"product_source"`
	BaselineSource        string              `json:"baseline_source"`
	ProductOnly           []ComparisonFinding `json:"product_only"`
	BaselineOnly          []ComparisonFinding `json:"baseline_only"`
	Shared                []SharedFinding     `json:"shared"`
	HumanJudgmentTemplate []HumanJudgmentRow  `json:"human_judgment_template"`
}

func CompareFindings(product, baseline FindingSet) (ComparisonReport, error) {
	if err := validateFindingSet("product", product); err != nil {
		return ComparisonReport{}, err
	}
	if err := validateFindingSet("baseline", baseline); err != nil {
		return ComparisonReport{}, err
	}
	productByKey := findingsByKey(product.Findings)
	baselineByKey := findingsByKey(baseline.Findings)
	report := ComparisonReport{
		SchemaVersion: 1, PrimaryMetric: "finding_comparison",
		ProductSource: product.Source, BaselineSource: baseline.Source,
		ProductOnly: []ComparisonFinding{}, BaselineOnly: []ComparisonFinding{}, Shared: []SharedFinding{}, HumanJudgmentTemplate: []HumanJudgmentRow{},
	}
	keys := make([]string, 0, len(productByKey)+len(baselineByKey))
	seen := map[string]struct{}{}
	for key := range productByKey {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range baselineByKey {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		productFinding, inProduct := productByKey[key]
		baselineFinding, inBaseline := baselineByKey[key]
		switch {
		case inProduct && inBaseline:
			report.Shared = append(report.Shared, SharedFinding{ComparisonKey: key, Product: productFinding, Baseline: baselineFinding})
			report.HumanJudgmentTemplate = append(report.HumanJudgmentTemplate, judgmentRow("shared", key, productFinding.ID, baselineFinding.ID))
		case inProduct:
			report.ProductOnly = append(report.ProductOnly, productFinding)
			report.HumanJudgmentTemplate = append(report.HumanJudgmentTemplate, judgmentRow("product_only", key, productFinding.ID, ""))
		case inBaseline:
			report.BaselineOnly = append(report.BaselineOnly, baselineFinding)
			report.HumanJudgmentTemplate = append(report.HumanJudgmentTemplate, judgmentRow("baseline_only", key, "", baselineFinding.ID))
		}
	}
	return report, nil
}

func validateFindingSet(name string, set FindingSet) error {
	if set.SchemaVersion != 1 || strings.TrimSpace(set.Source) == "" || set.Findings == nil {
		return fmt.Errorf("%s finding set identity is invalid", name)
	}
	seenIDs := map[string]struct{}{}
	seenKeys := map[string]struct{}{}
	for index, finding := range set.Findings {
		if strings.TrimSpace(finding.ID) == "" || strings.TrimSpace(finding.ComparisonKey) == "" || strings.TrimSpace(finding.Description) == "" {
			return fmt.Errorf("%s findings[%d] is incomplete", name, index)
		}
		if finding.Dimension != "D1" && finding.Dimension != "D2" && finding.Dimension != "D3" && finding.Dimension != "D4" {
			return fmt.Errorf("%s findings[%d].dimension is invalid", name, index)
		}
		if len(finding.CodeLocations) == 0 {
			return fmt.Errorf("%s findings[%d].code_locations is required", name, index)
		}
		for _, location := range finding.CodeLocations {
			if location.Path == "" || filepath.IsAbs(location.Path) || filepath.Clean(location.Path) != location.Path || strings.HasPrefix(location.Path, ".."+string(filepath.Separator)) || location.Line < 1 {
				return fmt.Errorf("%s findings[%d] has an invalid code location", name, index)
			}
		}
		if _, exists := seenIDs[finding.ID]; exists {
			return fmt.Errorf("%s finding id %q is duplicated", name, finding.ID)
		}
		if _, exists := seenKeys[finding.ComparisonKey]; exists {
			return fmt.Errorf("%s comparison key %q is duplicated", name, finding.ComparisonKey)
		}
		seenIDs[finding.ID] = struct{}{}
		seenKeys[finding.ComparisonKey] = struct{}{}
	}
	return nil
}

func findingsByKey(findings []ComparisonFinding) map[string]ComparisonFinding {
	result := make(map[string]ComparisonFinding, len(findings))
	for _, finding := range findings {
		result[finding.ComparisonKey] = finding
	}
	return result
}

func judgmentRow(partition, key, productID, baselineID string) HumanJudgmentRow {
	return HumanJudgmentRow{
		RowID: partition + ":" + key, Partition: partition, ComparisonKey: key,
		ProductFindingID: productID, BaselineFindingID: baselineID,
		Judgment: "", Options: []string{"adopted", "noise", "uncertain"},
	}
}
