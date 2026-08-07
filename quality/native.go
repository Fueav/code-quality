package quality

import (
	"bytes"
	"fmt"
)

const (
	NativeResultSchemaVersion = 6
	EvaluationRubricVersion   = "1.2.0"
)

type NativeCodeLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type NativeFinding struct {
	Title        string             `json:"title"`
	Priority     int                `json:"priority"`
	CodeLocation NativeCodeLocation `json:"code_location"`
	Reason       string             `json:"reason"`
	Suggestion   string             `json:"suggestion"`
}

type AdapterDrop struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type NativeExecution struct {
	Host                string        `json:"host"`
	ReviewMode          string        `json:"review_mode"`
	ExecutionProfile    string        `json:"execution_profile"`
	Model               string        `json:"model,omitempty"`
	ReasoningEffort     string        `json:"reasoning_effort"`
	ProviderInvocations int           `json:"provider_invocations"`
	AdapterDrops        []AdapterDrop `json:"adapter_drops"`
}

type NativeReviewResult struct {
	SchemaVersion           int             `json:"schema_version"`
	EvaluationRubricVersion string          `json:"evaluation_rubric_version"`
	Request                 ReviewRequest   `json:"request"`
	ReviewGoal              string          `json:"review_goal,omitempty"`
	Findings                []NativeFinding `json:"findings"`
	Execution               NativeExecution `json:"execution"`
	Adjudication            Adjudication    `json:"adjudication"`
}

type NativeReleaseSummary struct {
	SchemaVersion  int                  `json:"schema_version"`
	Result         string               `json:"result"`
	Release        string               `json:"release"`
	BlockingIssues int                  `json:"blocking_issues"`
	Issues         []NativeSummaryIssue `json:"issues"`
}

type NativeSummaryIssue struct {
	Priority   string `json:"priority"`
	Title      string `json:"title"`
	Location   string `json:"location"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
}

func SummarizeNativeResult(result NativeReviewResult) NativeReleaseSummary {
	release := "HOLD"
	if result.Adjudication.SemanticResult == ResultPass {
		release = "CONTINUE"
	}
	issues := make([]NativeSummaryIssue, 0, len(result.Findings))
	for _, finding := range result.Findings {
		issues = append(issues, NativeSummaryIssue{
			Priority: fmt.Sprintf("P%d", finding.Priority), Title: finding.Title,
			Location: fmt.Sprintf("%s:%d-%d", finding.CodeLocation.Path, finding.CodeLocation.StartLine, finding.CodeLocation.EndLine),
			Reason:   finding.Reason, Suggestion: finding.Suggestion,
		})
	}
	return NativeReleaseSummary{
		SchemaVersion: 1, Result: result.Adjudication.SemanticResult, Release: release,
		BlockingIssues: len(issues), Issues: issues,
	}
}

func RenderNativeMarkdown(result NativeReviewResult) string {
	summary := SummarizeNativeResult(result)
	var output bytes.Buffer
	icon := "⚠️"
	decision := "Review failed — do not release"
	switch summary.Result {
	case ResultPass:
		icon, decision = "✅", "No blocking issue — continue the release process"
	case ResultBlock:
		icon, decision = "❌", "Blocking issue found — do not release"
	}
	fmt.Fprintf(&output, "# %s AI Code Review: %s\n", icon, summary.Result)
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "Release: `%s`  \n", summary.Release)
	fmt.Fprintf(&output, "Decision: %s  \n", decision)
	fmt.Fprintf(&output, "Blocking issues: %d\n", summary.BlockingIssues)
	fmt.Fprintln(&output)
	if len(result.Findings) > 0 {
		fmt.Fprintln(&output, "## Issues to fix")
		fmt.Fprintln(&output)
		for _, finding := range result.Findings {
			fmt.Fprintf(&output, "### [P%d] %s\n\n", finding.Priority, finding.Title)
			fmt.Fprintf(&output, "- **Location:** `%s:%d-%d`\n", finding.CodeLocation.Path, finding.CodeLocation.StartLine, finding.CodeLocation.EndLine)
			fmt.Fprintf(&output, "- **Reason:** %s\n", finding.Reason)
			fmt.Fprintf(&output, "- **Suggestion:** %s\n\n", finding.Suggestion)
		}
	}
	if summary.Result == ResultError && len(result.Adjudication.Reasons) > 0 {
		fmt.Fprintln(&output, "## Why the review failed")
		fmt.Fprintln(&output)
		fmt.Fprintf(&output, "%s\n", result.Adjudication.Reasons[0])
	}
	return output.String()
}
