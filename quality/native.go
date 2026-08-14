package quality

import (
	"bytes"
	"fmt"
)

const (
	NativeResultSchemaVersion = 8
	EvaluationRubricVersion   = "1.2.0"
	ReviewScopeFull           = "FULL"
	ReviewScopeIncremental    = "INCREMENTAL"
	ResolutionResolved        = "RESOLVED"
	ResolutionUnresolved      = "UNRESOLVED"
)

type NativeCodeLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type NativeFinding struct {
	ID           string             `json:"id,omitempty"`
	Title        string             `json:"title"`
	Priority     int                `json:"priority"`
	CodeLocation NativeCodeLocation `json:"code_location"`
	Reason       string             `json:"reason"`
	Suggestion   string             `json:"suggestion"`
}

type PreviousFindingResolution struct {
	FindingID      string         `json:"finding_id"`
	Status         string         `json:"status"`
	Reason         string         `json:"reason"`
	CurrentFinding *NativeFinding `json:"current_finding"`
}

type AdapterDrop struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type NativeExecution struct {
	Host                string        `json:"host"`
	ReviewMode          string        `json:"review_mode"`
	ExecutionProfile    string        `json:"execution_profile"`
	Model               string        `json:"model"`
	ReasoningEffort     string        `json:"reasoning_effort"`
	ProviderInvocations int           `json:"provider_invocations"`
	AdapterDrops        []AdapterDrop `json:"adapter_drops"`
}

type NativeReviewResult struct {
	ReviewIdentity
	SchemaVersion              int                         `json:"schema_version"`
	EvaluationRubricVersion    string                      `json:"evaluation_rubric_version"`
	Request                    ReviewRequest               `json:"request"`
	ReviewGoal                 string                      `json:"review_goal,omitempty"`
	Findings                   []NativeFinding             `json:"findings"`
	PreviousBlockingFindings   []NativeFinding             `json:"previous_blocking_findings"`
	PreviousFindingResolutions []PreviousFindingResolution `json:"previous_finding_resolutions"`
	NewFindings                []NativeFinding             `json:"new_findings"`
	Execution                  NativeExecution             `json:"execution"`
	Adjudication               Adjudication                `json:"adjudication"`
}

type NativeReleaseSummary struct {
	SchemaVersion              int                  `json:"schema_version"`
	Result                     string               `json:"result"`
	Release                    string               `json:"release"`
	ReviewScope                string               `json:"review_scope"`
	ReviewKey                  string               `json:"review_key"`
	CurrentHead                string               `json:"current_head"`
	BlockingIssues             int                  `json:"blocking_issues"`
	AdvisoryIssues             int                  `json:"advisory_issues"`
	ResolvedPreviousFindings   int                  `json:"resolved_previous_findings"`
	UnresolvedPreviousFindings int                  `json:"unresolved_previous_findings"`
	NewFindingCount            int                  `json:"new_findings"`
	Issues                     []NativeSummaryIssue `json:"issues"`
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
	blockingIssues := 0
	advisoryIssues := 0
	issues := make([]NativeSummaryIssue, 0, len(result.Findings))
	resolvedPrevious := 0
	unresolvedPrevious := 0
	for _, resolution := range result.PreviousFindingResolutions {
		switch resolution.Status {
		case ResolutionResolved:
			resolvedPrevious++
		case ResolutionUnresolved:
			unresolvedPrevious++
		}
	}
	for _, finding := range result.Findings {
		if isBlockingNativePriority(finding.Priority) {
			blockingIssues++
		} else if isAdvisoryNativePriority(finding.Priority) {
			advisoryIssues++
		}
		issues = append(issues, NativeSummaryIssue{
			Priority: fmt.Sprintf("P%d", finding.Priority), Title: finding.Title,
			Location: fmt.Sprintf("%s:%d-%d", finding.CodeLocation.Path, finding.CodeLocation.StartLine, finding.CodeLocation.EndLine),
			Reason:   finding.Reason, Suggestion: finding.Suggestion,
		})
	}
	return NativeReleaseSummary{
		SchemaVersion: 3, Result: result.Adjudication.SemanticResult, Release: release,
		ReviewScope: result.ReviewScope, ReviewKey: result.ReviewKey, CurrentHead: result.CurrentHead,
		BlockingIssues: blockingIssues, AdvisoryIssues: advisoryIssues,
		ResolvedPreviousFindings: resolvedPrevious, UnresolvedPreviousFindings: unresolvedPrevious,
		NewFindingCount: len(result.NewFindings), Issues: issues,
	}
}

func RenderNativeMarkdown(result NativeReviewResult) string {
	summary := SummarizeNativeResult(result)
	var output bytes.Buffer
	icon := "⚠️"
	decision := "Review failed — do not release"
	switch summary.Result {
	case ResultPass:
		icon, decision = "✅", "No P0/P1 blocking issue — continue the release process"
	case ResultBlock:
		icon, decision = "❌", "Blocking issue found — do not release"
	}
	fmt.Fprintf(&output, "# %s AI Code Review: %s\n", icon, summary.Result)
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "Release: `%s`  \n", summary.Release)
	fmt.Fprintf(&output, "Decision: %s  \n", decision)
	fmt.Fprintf(&output, "Scope: `%s`  \n", summary.ReviewScope)
	fmt.Fprintf(&output, "Review key: `%s`  \n", summary.ReviewKey)
	fmt.Fprintf(&output, "Current head: `%s`  \n", summary.CurrentHead)
	fmt.Fprintf(&output, "Blocking issues: %d\n", summary.BlockingIssues)
	fmt.Fprintf(&output, "Advisory issues: %d\n", summary.AdvisoryIssues)
	if summary.ReviewScope == ReviewScopeIncremental {
		fmt.Fprintf(&output, "Previous findings resolved: %d  \n", summary.ResolvedPreviousFindings)
		fmt.Fprintf(&output, "Previous findings unresolved: %d  \n", summary.UnresolvedPreviousFindings)
		fmt.Fprintf(&output, "New findings: %d\n", summary.NewFindingCount)
	}
	fmt.Fprintln(&output)
	if summary.BlockingIssues > 0 {
		fmt.Fprintln(&output, "## Issues to fix before release")
		fmt.Fprintln(&output)
		for _, finding := range result.Findings {
			if isBlockingNativePriority(finding.Priority) {
				renderNativeFinding(&output, finding)
			}
		}
	}
	if summary.AdvisoryIssues > 0 {
		fmt.Fprintln(&output, "## Advisories")
		fmt.Fprintln(&output)
		for _, finding := range result.Findings {
			if isAdvisoryNativePriority(finding.Priority) {
				renderNativeFinding(&output, finding)
			}
		}
	}
	if summary.Result == ResultError && len(result.Adjudication.Reasons) > 0 {
		fmt.Fprintln(&output, "## Why the review failed")
		fmt.Fprintln(&output)
		fmt.Fprintf(&output, "%s\n", result.Adjudication.Reasons[0])
	}
	return output.String()
}

func isBlockingNativePriority(priority int) bool {
	return priority == 0 || priority == 1
}

func isAdvisoryNativePriority(priority int) bool {
	return priority == 2 || priority == 3
}

func nativeBlockingFindingCount(findings []NativeFinding) int {
	count := 0
	for _, finding := range findings {
		if isBlockingNativePriority(finding.Priority) {
			count++
		}
	}
	return count
}

func renderNativeFinding(output *bytes.Buffer, finding NativeFinding) {
	fmt.Fprintf(output, "### [P%d] %s\n\n", finding.Priority, finding.Title)
	fmt.Fprintf(output, "- **Location:** `%s:%d-%d`\n", finding.CodeLocation.Path, finding.CodeLocation.StartLine, finding.CodeLocation.EndLine)
	fmt.Fprintf(output, "- **Reason:** %s\n", finding.Reason)
	fmt.Fprintf(output, "- **Suggestion:** %s\n\n", finding.Suggestion)
}
