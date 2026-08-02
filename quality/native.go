package quality

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	NativeResultSchemaVersion = 3
	EvaluationRubricVersion   = "1.2.0"
)

type NativeCodeLocation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type NativeFinding struct {
	Title        string             `json:"title"`
	Body         string             `json:"body"`
	Priority     int                `json:"priority"`
	CodeLocation NativeCodeLocation `json:"code_location"`
}

type AdapterDrop struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type NativeExecution struct {
	Host            string        `json:"host"`
	ReviewMode      string        `json:"review_mode"`
	Model           string        `json:"model,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort"`
	ModelCalls      int           `json:"model_calls"`
	AdapterDrops    []AdapterDrop `json:"adapter_drops"`
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

func RenderNativeMarkdown(result NativeReviewResult) string {
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Code Quality Native Review")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "**Result:** `%s`  \n", result.Adjudication.SemanticResult)
	fmt.Fprintln(&output, "**Rollout:** `report_only`")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Scope")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Repository: `%s`\n", result.Request.Repository)
	fmt.Fprintf(&output, "- Base: `%s`\n", result.Request.BaseCommit)
	fmt.Fprintf(&output, "- Target: `%s`\n", result.Request.TargetCommit)
	if strings.TrimSpace(result.ReviewGoal) == "" {
		fmt.Fprintln(&output, "- Goal: not supplied")
	} else {
		fmt.Fprintf(&output, "- Goal: %s\n", result.ReviewGoal)
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Findings")
	fmt.Fprintln(&output)
	if result.Adjudication.SemanticResult == ResultManualReview && len(result.Findings) == 0 {
		fmt.Fprintln(&output, "Structured extraction is intentionally omitted. Inspect the frozen `native-review.txt` as the review authority.")
	} else if len(result.Findings) == 0 {
		fmt.Fprintln(&output, "No actionable finding remained.")
	} else {
		for _, finding := range result.Findings {
			fmt.Fprintf(&output, "### [P%d] %s\n\n", finding.Priority, finding.Title)
			fmt.Fprintf(&output, "%s\n\n", finding.Body)
			fmt.Fprintf(&output, "- Location: `%s:%d-%d`\n", finding.CodeLocation.Path, finding.CodeLocation.StartLine, finding.CodeLocation.EndLine)
		}
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Execution")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Mode: `%s`\n", result.Execution.ReviewMode)
	fmt.Fprintf(&output, "- Model calls: %d\n", result.Execution.ModelCalls)
	if len(result.Execution.AdapterDrops) > 0 {
		fmt.Fprintln(&output, "- Adapter exclusions:")
		for _, dropped := range result.Execution.AdapterDrops {
			fmt.Fprintf(&output, "  - Candidate %d: %s\n", dropped.Index, dropped.Reason)
		}
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Adjudication")
	fmt.Fprintln(&output)
	for _, reason := range result.Adjudication.Reasons {
		fmt.Fprintf(&output, "- %s\n", reason)
	}
	return output.String()
}
