package quality

const SkillVersion = "0.4.2"

// ReviewRequest is the trusted review baseline produced by the runner.
type ReviewRequest struct {
	Repository          string   `json:"repository"`
	TargetBranch        string   `json:"target_branch"`
	BaseCommit          string   `json:"base_commit"`
	TargetCommit        string   `json:"target_commit"`
	DiffSelectionReason string   `json:"diff_selection_reason"`
	ChangedFiles        []string `json:"changed_files"`
	AffectedEntries     []string `json:"affected_entries"`
}

type InactiveRuleFamily struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type CodeLocation struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

type Finding struct {
	ID               string         `json:"id"`
	RuleID           string         `json:"rule_id"`
	CodeLocations    []CodeLocation `json:"code_locations"`
	ProductionImpact string         `json:"production_impact"`
	MinimalFix       string         `json:"minimal_fix"`
}

type InspectedContext struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
}

type Execution struct {
	Host          string `json:"host"`
	SkillVersion  string `json:"skill_version"`
	AgentCount    int    `json:"agent_count"`
	VerifierCount int    `json:"verifier_count"`
	InputTokens   *int   `json:"input_tokens"`
	OutputTokens  *int   `json:"output_tokens"`
	DurationMS    *int   `json:"duration_ms"`
	RetryCount    *int   `json:"retry_count"`
}

type ModelReview struct {
	ActivatedRuleFamilies []string             `json:"activated_rule_families"`
	InactiveRuleFamilies  []InactiveRuleFamily `json:"inactive_rule_families"`
	Findings              []Finding            `json:"findings"`
	UninspectedScope      []string             `json:"uninspected_scope"`
	MissingContext        []string             `json:"missing_context"`
	InspectedContext      []InspectedContext   `json:"inspected_context"`
	Execution             Execution            `json:"-"`
}

type AdjudicatedFinding struct {
	Candidate    Finding `json:"candidate"`
	FinalVerdict string  `json:"final_verdict"`
}

type Adjudication struct {
	SemanticResult string   `json:"semantic_result"`
	RolloutMode    string   `json:"rollout_mode"`
	CIAction       string   `json:"ci_action"`
	Reasons        []string `json:"reasons"`
}

type ReviewResult struct {
	SchemaVersion         int                  `json:"schema_version"`
	PolicyVersion         string               `json:"policy_version"`
	Request               ReviewRequest        `json:"request"`
	ActivatedRuleFamilies []string             `json:"activated_rule_families"`
	InactiveRuleFamilies  []InactiveRuleFamily `json:"inactive_rule_families"`
	Findings              []AdjudicatedFinding `json:"findings"`
	Execution             Execution            `json:"execution"`
	UninspectedScope      []string             `json:"uninspected_scope"`
	MissingContext        []string             `json:"missing_context"`
	InspectedContext      []InspectedContext   `json:"inspected_context"`
	Adjudication          Adjudication         `json:"adjudication"`
}

type PolicyManifest struct {
	SchemaVersion int          `json:"schema_version"`
	PolicyVersion string       `json:"policy_version"`
	Rubric        string       `json:"rubric"`
	AgentLimit    int          `json:"agent_limit"`
	Rules         []PolicyRule `json:"rules"`
}

type PolicyRule struct {
	ID        string `json:"id"`
	Dimension string `json:"dimension"`
	Status    string `json:"status"`
}
