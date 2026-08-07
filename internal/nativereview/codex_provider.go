package nativereview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

type codexProvider struct {
	binary string
}

func NewCodexProvider(binary string) Provider {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}
	return codexProvider{binary: binary}
}

func (provider codexProvider) Host() string                     { return "codex" }
func (provider codexProvider) defaultModel() string             { return "gpt-5.6-sol" }
func (provider codexProvider) defaultReasoningEffort() string   { return "max" }
func (provider codexProvider) finalMessageFromTranscript() bool { return false }

func (provider codexProvider) validateReasoningEffort(value string) error {
	valid := map[string]bool{
		"minimal": true, "low": true, "medium": true, "high": true,
		"xhigh": true, "max": true, "ultra": true,
	}
	if !valid[value] {
		return fmt.Errorf("unsupported Codex reasoning effort %q", value)
	}
	return nil
}

func (provider codexProvider) buildInvocation(options providerInvocationOptions) reviewInvocation {
	artifacts := options.Session.Artifacts()
	args := []string{"exec"}
	if options.ExecutionProfile == quality.ExecutionProfileProductionCI {
		args = append(args, "--sandbox", "read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--disable", "hooks")
	} else {
		args = append(args, "--sandbox", "workspace-write", "--config", "sandbox_workspace_write.network_access=true")
	}
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", options.Model)
	}
	args = append(args,
		"--config", "model_reasoning_effort="+strconv.Quote(options.ReasoningEffort),
		"--json",
		"--output-last-message", artifacts.FinalMessagePath(),
		"-",
	)
	invocation := reviewInvocation{
		executable: provider.binary,
		args:       args,
		directory:  options.Session.RepositoryDirectory(),
		stdin:      buildReviewPrompt(options.Session.Request(), options.Goal, options.ExecutionProfile == quality.ExecutionProfileProductionCI),
		paths:      capturePathsFromSession(options.Session),
	}
	if options.LeaseFile != nil {
		invocation.extraFiles = append(invocation.extraFiles, options.LeaseFile)
	}
	return invocation
}

type codexEvent struct {
	Type  string      `json:"type"`
	Usage *codexUsage `json:"usage,omitempty"`
}

type codexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (provider codexProvider) decodeTranscript(reader io.Reader) (decodedTranscript, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(maxNativeOutputBytes))
	var latest *codexUsage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return decodedTranscript{}, fmt.Errorf("decode Codex JSONL event: %w", err)
		}
		if event.Type == "turn.completed" && event.Usage != nil {
			copy := *event.Usage
			latest = &copy
		}
	}
	if err := scanner.Err(); err != nil {
		return decodedTranscript{}, fmt.Errorf("scan Codex JSONL events: %w", err)
	}
	transcript := decodedTranscript{}
	if latest == nil {
		transcript.UsageError = errors.New("Codex JSONL has no turn.completed usage event")
		return transcript, nil
	}
	if latest.InputTokens < 0 || latest.OutputTokens < 0 {
		transcript.UsageError = errors.New("Codex usage tokens must be non-negative")
		return transcript, nil
	}
	if latest.InputTokens == 0 && latest.OutputTokens == 0 {
		transcript.UsageError = errors.New("Codex JSONL reported zero token usage; counters are unavailable")
		return transcript, nil
	}
	inputTokens := latest.InputTokens
	outputTokens := latest.OutputTokens
	transcript.InputTokens = &inputTokens
	transcript.OutputTokens = &outputTokens
	return transcript, nil
}
