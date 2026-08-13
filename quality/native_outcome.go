package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// NativeOutcomeOptions contains only the facts that are fixed before the
// native provider process runs. Classification consumes the frozen final message
// separately so callers cannot substitute a parsed or rewritten finding set.
type NativeOutcomeOptions struct {
	Request          ReviewRequest
	ReviewGoal       string
	Host             string
	ExecutionProfile string
	Model            string
	ReasoningEffort  string
}

// NativeOutcome is the validated runtime outcome of one ordinary provider review.
// Its wire representation is NativeReviewResult schema v7.
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
	if options.ExecutionProfile == "" {
		options.ExecutionProfile = ExecutionProfilePersonal
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
			Host: options.Host, ReviewMode: "native_review", ExecutionProfile: options.ExecutionProfile, Model: options.Model,
			ReasoningEffort: options.ReasoningEffort, ProviderInvocations: 1, AdapterDrops: []AdapterDrop{},
		},
		Adjudication: Adjudication{
			SemanticResult: ResultError,
			RolloutMode:    "release_gate",
			CIAction:       "hold_release",
			Reasons:        []string{},
		},
	}
	switch {
	case processErr != nil:
		result.Adjudication.Reasons = []string{"native review failed: " + processErr.Error()}
	case strings.TrimSpace(string(finalMessage)) == "":
		result.Adjudication.Reasons = []string{"native review output is missing or empty"}
	default:
		findings, decodeErr := decodeNativeFindings(finalMessage)
		if decodeErr != nil {
			result.Adjudication.Reasons = []string{"native review structured output is invalid: " + decodeErr.Error()}
			break
		}
		result.Findings = findings
		blockingFindings := nativeBlockingFindingCount(findings)
		if blockingFindings == 0 {
			result.Adjudication.SemanticResult = ResultPass
			result.Adjudication.CIAction = "continue_release"
			if len(findings) == 0 {
				result.Adjudication.Reasons = []string{"no P0/P1 blocking issue was reported"}
			} else {
				result.Adjudication.Reasons = []string{fmt.Sprintf("no P0/P1 blocking issue was reported; %d advisory issue(s) were retained", len(findings))}
			}
		} else {
			result.Adjudication.SemanticResult = ResultBlock
			result.Adjudication.Reasons = []string{fmt.Sprintf("%d P0/P1 blocking issue(s) must be fixed before release", blockingFindings)}
		}
	}
	if problems := ValidateNativeResult(result); len(problems) > 0 {
		result.Findings = []NativeFinding{}
		result.Adjudication.SemanticResult = ResultError
		result.Adjudication.CIAction = "hold_release"
		result.Adjudication.Reasons = []string{"native review structured output failed validation: " + strings.Join(problems, "; ")}
		if fallbackProblems := ValidateNativeResult(result); len(fallbackProblems) > 0 {
			return NativeOutcome{}, fmt.Errorf("native review error outcome is invalid: %s", strings.Join(fallbackProblems, "; "))
		}
	}
	return NativeOutcome{result: result}, nil
}

// Result returns a detached copy of the schema-v7 wire representation.
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

func (outcome NativeOutcome) Summary() NativeReleaseSummary {
	return SummarizeNativeResult(outcome.result)
}

func (outcome NativeOutcome) SemanticResult() string {
	return outcome.result.Adjudication.SemanticResult
}

func (outcome NativeOutcome) ProviderInvocations() int {
	return outcome.result.Execution.ProviderInvocations
}

type nativeFindingEnvelope struct {
	Findings []NativeFinding `json:"findings"`
}

func decodeNativeFindings(raw []byte) ([]NativeFinding, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}
	encodedFindings, exists := shape["findings"]
	if !exists || len(shape) != 1 || bytes.Equal(bytes.TrimSpace(encodedFindings), []byte("null")) {
		return nil, errors.New("root must contain only a non-null findings array")
	}
	envelope, err := DecodeStrict[nativeFindingEnvelope](bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if envelope.Findings == nil {
		envelope.Findings = []NativeFinding{}
	}
	sort.SliceStable(envelope.Findings, func(i, j int) bool {
		left, right := envelope.Findings[i], envelope.Findings[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		if left.CodeLocation.Path != right.CodeLocation.Path {
			return left.CodeLocation.Path < right.CodeLocation.Path
		}
		if left.CodeLocation.StartLine != right.CodeLocation.StartLine {
			return left.CodeLocation.StartLine < right.CodeLocation.StartLine
		}
		return left.Title < right.Title
	})
	return envelope.Findings, nil
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
	if request.Change != nil {
		change := *request.Change
		request.Change = &change
	}
	return request
}
