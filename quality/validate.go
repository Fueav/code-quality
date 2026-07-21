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
	validSeverity     = set("S1", "S2", "S3")
	validTrigger      = set("T1", "T2", "T3")
	validEvidence     = set("E1", "E2", "E3")
	validProposal     = set("MANUAL_REVIEW", "BLOCK")
	validVerification = set("not_run", "confirmed", "refuted", "insufficient")
	validDimensions   = set("D1", "D2", "D3", "D4")
	validRuleStatus   = set("report_only", "block_eligible")
	requiredRules     = map[string]string{
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
	if policy.AgentLimit < 1 || policy.AgentLimit > 3 {
		errors = append(errors, "agent_limit must be between 1 and 3")
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

func ValidateModelReview(review ModelReview, policy PolicyManifest) []string {
	var errors []string
	if review.Execution.AgentCount < 1 || review.Execution.AgentCount > policy.AgentLimit {
		errors = append(errors, "execution.agent_count must be between 1 and the policy limit")
	}
	if review.Execution.VerifierCount < 0 || review.Execution.VerifierCount > 1 {
		errors = append(errors, "execution.verifier_count must be 0 or 1")
	}
	if review.Execution.VerifierCount > review.Execution.AgentCount-1 {
		errors = append(errors, "execution.verifier_count exceeds available agents")
	}
	if review.Execution.InputTokens < 0 || review.Execution.OutputTokens < 0 || review.Execution.DurationMS < 0 || review.Execution.RetryCount < 0 {
		errors = append(errors, "execution metrics must be non-negative")
	}
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
	knownRules := make(map[string]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		knownRules[rule.ID] = struct{}{}
	}
	seenFindings := map[string]struct{}{}
	for index, finding := range review.Findings {
		prefix := fmt.Sprintf("findings[%d]", index)
		if strings.TrimSpace(finding.ID) == "" {
			errors = append(errors, prefix+".id is required")
		} else if _, exists := seenFindings[finding.ID]; exists {
			errors = append(errors, prefix+".id is duplicated")
		} else {
			seenFindings[finding.ID] = struct{}{}
		}
		if _, ok := knownRules[finding.RuleID]; !ok {
			errors = append(errors, prefix+".rule_id is unknown")
		}
		if _, ok := validProposal[finding.ProposedVerdict]; !ok {
			errors = append(errors, prefix+".proposed_verdict is invalid")
		}
		if _, ok := validSeverity[finding.Severity]; !ok {
			errors = append(errors, prefix+".severity is invalid")
		}
		if _, ok := validTrigger[finding.TriggerConfidence]; !ok {
			errors = append(errors, prefix+".trigger_confidence is invalid")
		}
		if _, ok := validEvidence[finding.EvidenceLevel]; !ok {
			errors = append(errors, prefix+".evidence_level is invalid")
		}
		if _, ok := validVerification[finding.VerifierResult]; !ok {
			errors = append(errors, prefix+".verifier_result is invalid")
		}
		if len(finding.CodeLocations) == 0 {
			errors = append(errors, prefix+".code_locations is required")
		}
		for _, location := range finding.CodeLocations {
			if !isCleanRelativePath(location.Path) || location.Line < 1 {
				errors = append(errors, prefix+".code_locations contains an invalid location")
			}
		}
		if hasBlank(finding.AffectedCallPath) {
			errors = append(errors, prefix+".affected_call_path is required")
		}
		if strings.TrimSpace(finding.TriggerCondition) == "" {
			errors = append(errors, prefix+".trigger_condition is required")
		}
		if hasBlank(finding.CausalChain) {
			errors = append(errors, prefix+".causal_chain is required")
		}
		if strings.TrimSpace(finding.ProductionImpact) == "" {
			errors = append(errors, prefix+".production_impact is required")
		}
		if hasBlank(finding.VerificationPerformed) {
			errors = append(errors, prefix+".verification_performed is required")
		}
		if strings.TrimSpace(finding.MinimalFix) == "" {
			errors = append(errors, prefix+".minimal_fix is required")
		}
	}
	if containsBlank(review.UninspectedScope) {
		errors = append(errors, "uninspected_scope contains an empty value")
	}
	if containsBlank(review.MissingContext) {
		errors = append(errors, "missing_context contains an empty value")
	}
	return uniqueSorted(errors)
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
