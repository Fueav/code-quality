package quality

import (
	"fmt"
	"strings"
)

func ValidateNativeResult(result NativeReviewResult) []string {
	var problems []string
	if result.SchemaVersion != NativeResultSchemaVersion {
		problems = append(problems, "native result schema_version must be 3")
	}
	if strings.TrimSpace(result.EvaluationRubricVersion) == "" {
		problems = append(problems, "evaluation_rubric_version is required")
	}
	problems = append(problems, ValidateRequest(result.Request)...)
	if len(result.ReviewGoal) > 4000 {
		problems = append(problems, "review_goal exceeds 4000 bytes")
	}
	changed := map[string]struct{}{}
	for _, path := range result.Request.ChangedFiles {
		changed[path] = struct{}{}
	}
	for index, finding := range result.Findings {
		prefix := fmt.Sprintf("findings[%d]", index)
		if strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Body) == "" {
			problems = append(problems, prefix+" title and body are required")
		}
		if finding.Priority < 0 || finding.Priority > 3 {
			problems = append(problems, prefix+" priority is outside 0-3")
		}
		location := finding.CodeLocation
		if !isCleanRelativePath(location.Path) || location.StartLine < 1 || location.EndLine < location.StartLine {
			problems = append(problems, prefix+" code_location is invalid")
		} else if _, exists := changed[location.Path]; !exists {
			problems = append(problems, prefix+" code_location is not in a changed file")
		}
	}
	if result.Execution.Host != "codex" || result.Execution.ReviewMode != "native_review" {
		problems = append(problems, "execution must use codex native_review")
	}
	if strings.TrimSpace(result.Execution.ReasoningEffort) == "" {
		problems = append(problems, "execution.reasoning_effort is required")
	}
	if result.Execution.ModelCalls != 1 {
		problems = append(problems, "execution.model_calls must be exactly 1")
	}
	for index, dropped := range result.Execution.AdapterDrops {
		if dropped.Index < 0 || strings.TrimSpace(dropped.Reason) == "" {
			problems = append(problems, fmt.Sprintf("execution.adapter_drops[%d] is invalid", index))
		}
	}
	if result.Adjudication.RolloutMode != "report_only" || result.Adjudication.CIAction != "publish_report" {
		problems = append(problems, "adjudication must remain report_only and publish_report")
	}
	if len(result.Adjudication.Reasons) == 0 || containsBlank(result.Adjudication.Reasons) {
		problems = append(problems, "adjudication reasons are required")
	}
	switch result.Adjudication.SemanticResult {
	case ResultPass, ResultIncomplete:
		if len(result.Findings) != 0 {
			problems = append(problems, result.Adjudication.SemanticResult+" result cannot contain findings")
		}
	case ResultManualReview:
		if len(result.Findings) == 0 {
			problems = append(problems, "MANUAL_REVIEW result requires findings")
		}
	default:
		problems = append(problems, "native adjudication semantic_result is invalid")
	}
	return uniqueSorted(problems)
}
