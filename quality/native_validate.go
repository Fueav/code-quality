package quality

import (
	"fmt"
	"strings"
)

func ValidateNativeResult(result NativeReviewResult) []string {
	var problems []string
	if result.SchemaVersion != NativeResultSchemaVersion {
		problems = append(problems, fmt.Sprintf("native result schema_version must be %d", NativeResultSchemaVersion))
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
		if strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Reason) == "" || strings.TrimSpace(finding.Suggestion) == "" {
			problems = append(problems, prefix+" title, reason, and suggestion are required")
		}
		if len(finding.Title) > 160 || len(finding.Reason) > 1000 || len(finding.Suggestion) > 1000 ||
			strings.ContainsAny(finding.Title+finding.Reason+finding.Suggestion, "\r\n") {
			problems = append(problems, prefix+" text must be concise single-line content")
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
	if (result.Execution.Host != "codex" && result.Execution.Host != "claude-code") || result.Execution.ReviewMode != "native_review" {
		problems = append(problems, "execution must use a supported native_review provider")
	}
	if strings.TrimSpace(result.Execution.ReasoningEffort) == "" {
		problems = append(problems, "execution.reasoning_effort is required")
	}
	if result.Execution.ExecutionProfile != ExecutionProfilePersonal && result.Execution.ExecutionProfile != ExecutionProfileProductionCI {
		problems = append(problems, "execution.execution_profile must be personal or production-ci")
	}
	if result.Execution.ProviderInvocations != 1 {
		problems = append(problems, "execution.provider_invocations must be exactly 1")
	}
	for index, dropped := range result.Execution.AdapterDrops {
		if dropped.Index < 0 || strings.TrimSpace(dropped.Reason) == "" {
			problems = append(problems, fmt.Sprintf("execution.adapter_drops[%d] is invalid", index))
		}
	}
	if result.Adjudication.RolloutMode != "release_gate" {
		problems = append(problems, "adjudication rollout_mode must be release_gate")
	}
	if len(result.Adjudication.Reasons) == 0 || containsBlank(result.Adjudication.Reasons) {
		problems = append(problems, "adjudication reasons are required")
	}
	switch result.Adjudication.SemanticResult {
	case ResultPass:
		if nativeBlockingFindingCount(result.Findings) != 0 {
			problems = append(problems, "PASS result cannot contain P0/P1 findings")
		}
		if result.Adjudication.CIAction != "continue_release" {
			problems = append(problems, "PASS must continue_release")
		}
	case ResultBlock:
		if nativeBlockingFindingCount(result.Findings) == 0 {
			problems = append(problems, "BLOCK must contain at least one P0/P1 finding")
		}
		if result.Adjudication.CIAction != "hold_release" {
			problems = append(problems, "BLOCK must hold_release")
		}
	case ResultError:
		if len(result.Findings) != 0 {
			problems = append(problems, "ERROR result cannot contain findings")
		}
		if result.Adjudication.CIAction != "hold_release" {
			problems = append(problems, "ERROR must hold_release")
		}
	default:
		problems = append(problems, "native adjudication semantic_result is invalid")
	}
	return uniqueSorted(problems)
}
