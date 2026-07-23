package quality

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

var (
	validDimensions = set("D1", "D2", "D3", "D4")
	validRuleStatus = set("report_only", "block_eligible")
	requiredRules   = map[string]string{
		"DES-001": "D1", "DES-002": "D1", "DES-003": "D1", "DES-004": "D1", "DES-005": "D1",
		"COR-001": "D2", "COR-002": "D2", "COR-003": "D2", "COR-004": "D2", "COR-005": "D2",
		"REL-001": "D3", "REL-002": "D3", "REL-003": "D3", "REL-004": "D3", "REL-005": "D3",
		"SEC-001": "D4", "SEC-002": "D4", "SEC-003": "D4", "CHG-001": "D4", "CHG-002": "D4",
	}
)

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func DecodeStrict[T any](reader io.Reader) (T, error) {
	var value T
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if decoder.More() {
		return value, fmt.Errorf("multiple JSON values are not allowed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("multiple JSON values are not allowed")
		}
		return value, err
	}
	return value, nil
}

func ValidatePolicy(policy PolicyManifest) []string {
	var errors []string
	if policy.SchemaVersion != 1 {
		errors = append(errors, "policy schema_version must be 1")
	}
	if strings.TrimSpace(policy.PolicyVersion) == "" {
		errors = append(errors, "policy_version is required")
	}
	if strings.TrimSpace(policy.Rubric) == "" || filepath.IsAbs(policy.Rubric) || !isCleanRelativePath(policy.Rubric) {
		errors = append(errors, "rubric must be a clean relative path")
	}
	if policy.AgentLimit != 2 {
		errors = append(errors, "agent_limit must be 2 for the V1 runtime contract")
	}
	if len(policy.Rules) != 20 {
		errors = append(errors, "policy must define exactly 20 rules")
	}
	seen := map[string]struct{}{}
	for index, rule := range policy.Rules {
		prefix := fmt.Sprintf("rules[%d]", index)
		if strings.TrimSpace(rule.ID) == "" {
			errors = append(errors, prefix+".id is required")
		} else if _, exists := seen[rule.ID]; exists {
			errors = append(errors, prefix+".id is duplicated")
		} else {
			seen[rule.ID] = struct{}{}
		}
		if _, ok := validDimensions[rule.Dimension]; !ok {
			errors = append(errors, prefix+".dimension is invalid")
		}
		if expectedDimension, ok := requiredRules[rule.ID]; !ok || expectedDimension != rule.Dimension {
			errors = append(errors, prefix+" does not match the V1.1 rule contract")
		}
		if _, ok := validRuleStatus[rule.Status]; !ok {
			errors = append(errors, prefix+".status is invalid")
		}
	}
	return uniqueSorted(errors)
}

func ValidateRequest(request ReviewRequest) []string {
	var errors []string
	for name, value := range map[string]string{
		"repository":            request.Repository,
		"target_branch":         request.TargetBranch,
		"base_commit":           request.BaseCommit,
		"target_commit":         request.TargetCommit,
		"diff_selection_reason": request.DiffSelectionReason,
	} {
		if strings.TrimSpace(value) == "" {
			errors = append(errors, name+" is required")
		}
	}
	if hasBlankOrDuplicate(request.ChangedFiles) {
		errors = append(errors, "changed_files must contain unique non-empty paths")
	}
	for _, path := range request.ChangedFiles {
		if !isCleanRelativePath(path) {
			errors = append(errors, "changed_files contains an invalid repository path")
		}
	}
	if hasBlankOrDuplicate(request.AffectedEntries) {
		errors = append(errors, "affected_entries must contain unique non-empty values")
	}
	return uniqueSorted(errors)
}

// ValidateModelReviewStructure checks only report-level structure. These are
// fatal: any error means the whole review is INCOMPLETE. Per-finding field
// quality is checked separately by ValidateFinding and is forgiving.
func ValidateModelReviewStructure(review ModelReview, policy PolicyManifest) []string {
	var errors []string
	errors = append(errors, validateExecution(review.Execution, policy, false)...)
	if hasBlankOrDuplicate(review.ActivatedRuleFamilies) {
		errors = append(errors, "activated_rule_families must be unique and non-empty")
	}
	seenFamilies := map[string]string{}
	for _, family := range review.ActivatedRuleFamilies {
		if _, ok := validDimensions[family]; !ok {
			errors = append(errors, "activated_rule_families contains an unknown dimension")
		}
		seenFamilies[family] = "active"
	}
	seenInactive := map[string]struct{}{}
	for index, family := range review.InactiveRuleFamilies {
		if strings.TrimSpace(family.ID) == "" || strings.TrimSpace(family.Reason) == "" {
			errors = append(errors, fmt.Sprintf("inactive_rule_families[%d] is incomplete", index))
		}
		if _, exists := seenInactive[family.ID]; exists {
			errors = append(errors, "inactive_rule_families contains duplicate ids")
		}
		if _, ok := validDimensions[family.ID]; !ok {
			errors = append(errors, "inactive_rule_families contains an unknown dimension")
		}
		if state, exists := seenFamilies[family.ID]; exists {
			errors = append(errors, "rule family is both active and inactive: "+state)
		}
		seenInactive[family.ID] = struct{}{}
		seenFamilies[family.ID] = "inactive"
	}
	if len(seenFamilies) != len(validDimensions) {
		errors = append(errors, "every V1.1 dimension must be active or have an inactive reason")
	}
	seenFindings := map[string]struct{}{}
	for index, finding := range review.Findings {
		if strings.TrimSpace(finding.ID) == "" {
			errors = append(errors, fmt.Sprintf("findings[%d].id is required", index))
		} else if _, exists := seenFindings[finding.ID]; exists {
			errors = append(errors, fmt.Sprintf("findings[%d].id is duplicated", index))
		} else {
			seenFindings[finding.ID] = struct{}{}
		}
	}
	if containsBlank(review.UninspectedScope) {
		errors = append(errors, "uninspected_scope contains an empty value")
	}
	if containsBlank(review.MissingContext) {
		errors = append(errors, "missing_context contains an empty value")
	}
	seenContext := map[string]struct{}{}
	for index, context := range review.InspectedContext {
		if !isCleanRelativePath(context.Path) || strings.TrimSpace(context.Purpose) == "" {
			errors = append(errors, fmt.Sprintf("inspected_context[%d] is invalid", index))
		}
		if _, exists := seenContext[context.Path]; exists {
			errors = append(errors, "inspected_context contains duplicate paths")
		}
		seenContext[context.Path] = struct{}{}
	}
	return uniqueSorted(errors)
}

// ValidateFinding returns the reasons a single finding is not reportable. An
// empty result means the finding is kept; a non-empty result means the
// adjudicator drops just this finding (it never fails the whole review).
func ValidateFinding(finding Finding, policy PolicyManifest) []string {
	var problems []string
	known := false
	for _, rule := range policy.Rules {
		if rule.ID == finding.RuleID {
			known = true
			break
		}
	}
	if !known {
		problems = append(problems, "rule_id is unknown")
	}
	if len(finding.CodeLocations) == 0 {
		problems = append(problems, "code_locations is required")
	}
	for _, location := range finding.CodeLocations {
		if !isCleanRelativePath(location.Path) || location.Line < 1 {
			problems = append(problems, "code_locations contains an invalid location")
		}
	}
	if strings.TrimSpace(finding.ProductionImpact) == "" {
		problems = append(problems, "production_impact is required")
	}
	if strings.TrimSpace(finding.MinimalFix) == "" {
		problems = append(problems, "minimal_fix is required")
	}
	return problems
}

func validateExecution(execution Execution, policy PolicyManifest, allowAbsent bool) []string {
	var errors []string
	if allowAbsent && execution.Host == "" && execution.SkillVersion == "" && execution.AgentCount == 0 && execution.VerifierCount == 0 && execution.InputTokens == nil && execution.OutputTokens == nil && execution.DurationMS == nil && execution.RetryCount == nil {
		return errors
	}
	if execution.Host != "claude-code" && execution.Host != "codex" {
		errors = append(errors, "execution.host must be claude-code or codex")
	}
	if execution.SkillVersion != SkillVersion {
		errors = append(errors, "execution.skill_version must match the CLI skill version")
	}
	if execution.AgentCount < 1 || execution.AgentCount > policy.AgentLimit {
		errors = append(errors, "execution.agent_count must be between 1 and the policy limit")
	}
	if execution.VerifierCount < 0 || execution.VerifierCount > 1 {
		errors = append(errors, "execution.verifier_count must be 0 or 1")
	}
	if execution.VerifierCount > execution.AgentCount-1 {
		errors = append(errors, "execution.verifier_count exceeds available agents")
	}
	if negativeMetric(execution.InputTokens) || negativeMetric(execution.OutputTokens) || negativeMetric(execution.DurationMS) || negativeMetric(execution.RetryCount) {
		errors = append(errors, "execution metrics must be non-negative when available")
	}
	return errors
}

func negativeMetric(value *int) bool {
	return value != nil && *value < 0
}

func isCleanRelativePath(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func containsBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func hasBlank(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func hasBlankOrDuplicate(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func uniqueSorted(values []string) []string {
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
