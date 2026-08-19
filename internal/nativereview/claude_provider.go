package nativereview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type claudeProvider struct {
	binary string
}

func NewClaudeProvider(binary string) Provider {
	if strings.TrimSpace(binary) == "" {
		binary = "claude"
	}
	return claudeProvider{binary: binary}
}

func (provider claudeProvider) Host() string                     { return "claude-code" }
func (provider claudeProvider) defaultModel() string             { return "opus" }
func (provider claudeProvider) defaultReasoningEffort() string   { return "max" }
func (provider claudeProvider) finalMessageFromTranscript() bool { return true }

func (provider claudeProvider) validateReasoningEffort(value string) error {
	valid := map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	if !valid[value] {
		return fmt.Errorf("unsupported Claude reasoning effort %q", value)
	}
	return nil
}

func (provider claudeProvider) buildInvocation(options providerInvocationOptions) reviewInvocation {
	prompt := buildPlanReviewPrompt(options.Plan, options.Goal, true)
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
	}
	if options.ExecutionProfile == quality.ExecutionProfileProductionCI {
		args = append(args, "--permission-mode", "plan", "--safe-mode", "--strict-mcp-config")
	} else {
		args = append(args, "--permission-mode", "auto")
	}
	args = append(args, "--model", options.Model, "--effort", options.ReasoningEffort, "--json-schema", string(options.OutputSchema), prompt)
	invocation := reviewInvocation{
		executable: provider.binary,
		args:       args,
		directory:  options.Session.RepositoryDirectory(),
		paths:      capturePathsFromSession(options.Session),
	}
	if options.LeaseFile != nil {
		invocation.extraFiles = append(invocation.extraFiles, options.LeaseFile)
	}
	if options.SessionLockFile != nil {
		invocation.extraFiles = append(invocation.extraFiles, options.SessionLockFile)
	}
	return invocation
}

func (provider claudeProvider) buildRestrictedInvocation(options restrictedInvocationOptions) reviewInvocation {
	prompt := buildRestrictedAdjudicationPrompt(options.Plan, options.Findings)
	paths := options.CapturePaths
	if paths.finalMessage == "" {
		paths = restrictedCapturePathsFromSession(options.Session)
	}
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--permission-mode", "plan", "--safe-mode", "--strict-mcp-config",
		"--system-prompt", string(options.Policy),
		"--model", options.Model, "--effort", options.ReasoningEffort,
		"--json-schema", string(options.OutputSchema), prompt,
	}
	invocation := reviewInvocation{
		executable: provider.binary,
		args:       args,
		directory:  options.Session.RepositoryDirectory(),
		paths:      paths,
	}
	if options.LeaseFile != nil {
		invocation.extraFiles = append(invocation.extraFiles, options.LeaseFile)
	}
	if options.SessionLockFile != nil {
		invocation.extraFiles = append(invocation.extraFiles, options.SessionLockFile)
	}
	return invocation
}

type claudeEvent struct {
	Type    string       `json:"type"`
	Subtype string       `json:"subtype"`
	IsError bool         `json:"is_error"`
	Result  string       `json:"result"`
	Usage   *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	InputTokens          int64  `json:"input_tokens"`
	OutputTokens         int64  `json:"output_tokens"`
	CacheReadInputTokens *int64 `json:"cache_read_input_tokens,omitempty"`
}

func (provider claudeProvider) decodeTranscript(reader io.Reader) (decodedTranscript, error) {
	return decodeClaudeTranscript(reader)
}

func decodeClaudeTranscript(reader io.Reader) (decodedTranscript, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(maxNativeOutputBytes))
	resultCount := 0
	var resultEvent claudeEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event claudeEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return decodedTranscript{}, fmt.Errorf("decode Claude JSONL event: %w", err)
		}
		if event.Type != "result" {
			continue
		}
		resultCount++
		if resultCount > 1 {
			return decodedTranscript{}, errors.New("Claude JSONL has multiple result events")
		}
		resultEvent = event
	}
	if err := scanner.Err(); err != nil {
		return decodedTranscript{}, fmt.Errorf("scan Claude JSONL events: %w", err)
	}
	if resultCount == 0 {
		return decodedTranscript{}, errors.New("Claude JSONL has no result event")
	}
	if resultEvent.IsError || resultEvent.Subtype != "success" {
		return decodedTranscript{}, fmt.Errorf("Claude result event is not successful: subtype %q", resultEvent.Subtype)
	}
	transcript := decodedTranscript{FinalMessage: []byte(resultEvent.Result)}
	if resultEvent.Usage == nil {
		transcript.UsageError = errors.New("Claude result event has no usage counters")
		return transcript, nil
	}
	if resultEvent.Usage.InputTokens < 0 || resultEvent.Usage.OutputTokens < 0 ||
		(resultEvent.Usage.CacheReadInputTokens != nil && *resultEvent.Usage.CacheReadInputTokens < 0) {
		transcript.UsageError = errors.New("Claude usage tokens must be non-negative")
		return transcript, nil
	}
	if resultEvent.Usage.InputTokens == 0 && resultEvent.Usage.OutputTokens == 0 {
		transcript.UsageError = errors.New("Claude JSONL reported zero token usage; counters are unavailable")
		return transcript, nil
	}
	inputTokens := resultEvent.Usage.InputTokens
	outputTokens := resultEvent.Usage.OutputTokens
	transcript.InputTokens = &inputTokens
	transcript.OutputTokens = &outputTokens
	if resultEvent.Usage.CacheReadInputTokens != nil {
		cachedInputTokens := *resultEvent.Usage.CacheReadInputTokens
		transcript.CachedInputTokens = &cachedInputTokens
	}
	return transcript, nil
}
