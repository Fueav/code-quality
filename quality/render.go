package quality

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

func RenderMarkdown(result ReviewResult) string {
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Code Quality Review")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "**Result:** `%s`  \n", result.Adjudication.SemanticResult)
	fmt.Fprintf(&output, "**Rollout:** `%s`  \n", result.Adjudication.RolloutMode)
	fmt.Fprintf(&output, "**Policy:** `%s`\n", result.PolicyVersion)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Baseline")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Repository: `%s`\n", result.Request.Repository)
	fmt.Fprintf(&output, "- Target branch: `%s`\n", result.Request.TargetBranch)
	fmt.Fprintf(&output, "- Base commit: `%s`\n", result.Request.BaseCommit)
	fmt.Fprintf(&output, "- Target commit: `%s`\n", result.Request.TargetCommit)
	fmt.Fprintf(&output, "- Diff reason: %s\n", result.Request.DiffSelectionReason)
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Findings")
	fmt.Fprintln(&output)
	if len(result.Findings) == 0 {
		fmt.Fprintln(&output, "No reportable V1.2 finding.")
	} else {
		findings := append([]AdjudicatedFinding(nil), result.Findings...)
		sort.Slice(findings, func(i, j int) bool {
			return findings[i].Candidate.ID < findings[j].Candidate.ID
		})
		for _, finding := range findings {
			candidate := finding.Candidate
			fmt.Fprintf(&output, "### %s · %s\n\n", candidate.ID, candidate.RuleID)
			fmt.Fprintf(&output, "- Verdict: `%s`\n", finding.FinalVerdict)
			fmt.Fprintf(&output, "- Impact: %s\n", candidate.ProductionImpact)
			fmt.Fprintf(&output, "- Location: %s\n", renderLocations(candidate.CodeLocations))
			fmt.Fprintf(&output, "- Minimal fix: %s\n", candidate.MinimalFix)
		}
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Coverage And Uncertainty")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Uninspected scope: %s\n", renderList(result.UninspectedScope))
	fmt.Fprintf(&output, "- Missing context: %s\n", renderList(result.MissingContext))
	fmt.Fprintf(&output, "- Inspected context: %s\n", renderInspectedContext(result.InspectedContext))
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Execution")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Host / Skill: `%s / %s`\n", result.Execution.Host, result.Execution.SkillVersion)
	fmt.Fprintf(&output, "- Agents: %d (%d verifier)\n", result.Execution.AgentCount, result.Execution.VerifierCount)
	fmt.Fprintf(&output, "- Tokens: %s input / %s output\n", renderMetric(result.Execution.InputTokens), renderMetric(result.Execution.OutputTokens))
	fmt.Fprintf(&output, "- Duration: %s\n", renderDuration(result.Execution.DurationMS))
	fmt.Fprintf(&output, "- Retries: %s\n", renderMetric(result.Execution.RetryCount))
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Adjudication Reasons")
	fmt.Fprintln(&output)
	for _, reason := range result.Adjudication.Reasons {
		fmt.Fprintf(&output, "- %s\n", reason)
	}
	return output.String()
}

func renderMetric(value *int) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%d", *value)
}

func renderDuration(value *int) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%d ms", *value)
}

func renderLocations(locations []CodeLocation) string {
	values := make([]string, 0, len(locations))
	for _, location := range locations {
		values = append(values, fmt.Sprintf("`%s:%d`", location.Path, location.Line))
	}
	return strings.Join(values, ", ")
}

func renderInspectedContext(values []InspectedContext) string {
	if len(values) == 0 {
		return "none"
	}
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, fmt.Sprintf("`%s` (%s)", value.Path, value.Purpose))
	}
	return strings.Join(items, "; ")
}

func renderList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, "; ")
}
