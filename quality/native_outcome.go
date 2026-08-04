package quality

import (
	"fmt"
	"io"
	"strings"
)

// NativeOutcomeOptions contains only the facts that are fixed before the
// native provider process runs. Classification consumes the frozen final message
// separately so callers cannot substitute a parsed or rewritten finding set.
type NativeOutcomeOptions struct {
	Request         ReviewRequest
	ReviewGoal      string
	Host            string
	Model           string
	ReasoningEffort string
}

// NativeOutcome is the validated runtime outcome of one ordinary provider review.
// Its wire representation is NativeReviewResult schema v4.
type NativeOutcome struct {
	result NativeReviewResult
}

// ClassifyFrozenNativeReview applies the thin deterministic three-state
// contract to the exact frozen final-message bytes from the provider process.
func ClassifyFrozenNativeReview(options NativeOutcomeOptions, finalMessage []byte, processErr error) (NativeOutcome, error) {
	options.ReviewGoal = strings.TrimSpace(options.ReviewGoal)
	if len(options.ReviewGoal) > 4000 {
		return NativeOutcome{}, fmt.Errorf("review goal exceeds 4000 bytes")
	}
	if options.Host == "" {
		options.Host = "codex"
	}
	if options.Model == "" {
		if options.Host == "claude-code" {
			options.Model = "opus"
		} else {
			options.Model = "gpt-5.6-sol"
		}
	}
	if options.ReasoningEffort == "" {
		options.ReasoningEffort = "max"
	}
	result := NativeReviewResult{
		SchemaVersion:           NativeResultSchemaVersion,
		EvaluationRubricVersion: EvaluationRubricVersion,
		Request:                 cloneReviewRequest(options.Request),
		ReviewGoal:              options.ReviewGoal,
		Findings:                []NativeFinding{},
		Execution: NativeExecution{
			Host: options.Host, ReviewMode: "native_review", Model: options.Model,
			ReasoningEffort: options.ReasoningEffort, ProviderInvocations: 1, AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultIncomplete,
			RolloutMode:    "report_only",
			CIAction:       "publish_report",
			Reasons:        []string{},
		},
	}
	switch {
	case processErr != nil:
		result.Adjudication.Reasons = []string{"native review failed: " + processErr.Error()}
	case strings.TrimSpace(string(finalMessage)) == "":
		result.Adjudication.Reasons = []string{"native review output is missing or empty"}
	case isExactNoFindingsDocument(string(finalMessage)):
		result.Adjudication.SemanticResult = ResultPass
		result.Adjudication.Reasons = []string{"native review reported no actionable findings"}
	default:
		result.Adjudication.SemanticResult = ResultManualReview
		result.Adjudication.Reasons = []string{
			"native review produced nonempty output that is not an exact no-findings sentinel; inspect frozen native-review.txt",
		}
	}
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		return NativeOutcome{}, fmt.Errorf("native review outcome is invalid: %s", strings.Join(problems, "; "))
	}
	return NativeOutcome{result: result}, nil
}

// Result returns a detached copy of the schema-v4 wire representation.
func (outcome NativeOutcome) Result() NativeReviewResult {
	return cloneNativeReviewResult(outcome.result)
}

// EncodeJSON writes the same validated fact used by Markdown rendering.
func (outcome NativeOutcome) EncodeJSON(writer io.Writer) error {
	if problems := ValidateNativeResult(outcome.result); len(problems) > 0 {
		return fmt.Errorf("native review outcome is invalid: %s", strings.Join(problems, "; "))
	}
	return EncodeJSON(writer, outcome.result)
}

// Markdown renders the same validated fact used by JSON encoding.
func (outcome NativeOutcome) Markdown() string {
	return RenderNativeMarkdown(outcome.result)
}

func (outcome NativeOutcome) SemanticResult() string {
	return outcome.result.Adjudication.SemanticResult
}

func (outcome NativeOutcome) ProviderInvocations() int {
	return outcome.result.Execution.ProviderInvocations
}

func isExactNoFindingsDocument(raw string) bool {
	switch strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n")) {
	case "No findings.", "No actionable findings.", "No actionable defects found.":
		return true
	default:
		return false
	}
}

func cloneNativeReviewResult(result NativeReviewResult) NativeReviewResult {
	result.Request = cloneReviewRequest(result.Request)
	result.Findings = append([]NativeFinding(nil), result.Findings...)
	result.Execution.AdapterDrops = append([]AdapterDrop(nil), result.Execution.AdapterDrops...)
	result.Adjudication.Reasons = append([]string(nil), result.Adjudication.Reasons...)
	return result
}

func cloneReviewRequest(request ReviewRequest) ReviewRequest {
	request.ChangedFiles = append([]string(nil), request.ChangedFiles...)
	request.AffectedEntries = append([]string(nil), request.AffectedEntries...)
	return request
}
