package codexreview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const (
	maxNativeOutputBytes      = int64(10 << 20)
	processOutputDrainTimeout = 5 * time.Second
)

type capturePaths struct {
	finalMessage   string
	jsonl          string
	stderr         string
	freezeManifest string
	metrics        string
}

type reviewInvocation struct {
	executable string
	args       []string
	directory  string
	stdin      string
	paths      capturePaths
	extraFiles []*os.File
}

type processOutputWriter struct {
	io.Writer
}

type capturedNativeEvidence struct {
	finalMessage []byte
	processErr   error
}

type NativeRunMetrics struct {
	SchemaVersion    int    `json:"schema_version"`
	DurationMS       int64  `json:"duration_ms"`
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	UsageAvailable   bool   `json:"usage_available"`
	UsageError       string `json:"usage_error,omitempty"`
	ChangedFileCount int    `json:"changed_file_count"`
	TrustedDiffBytes int64  `json:"trusted_diff_bytes"`
}

type codexEvent struct {
	Type  string      `json:"type"`
	Usage *codexUsage `json:"usage,omitempty"`
}

type codexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func capturePathsFromSession(session reviewsession.NativeSession) capturePaths {
	artifacts := session.Artifacts()
	return capturePaths{
		finalMessage:   artifacts.FinalMessagePath(),
		jsonl:          artifacts.JSONLPath(),
		stderr:         artifacts.StderrPath(),
		freezeManifest: artifacts.FreezeManifestPath(),
		metrics:        artifacts.MetricsPath(),
	}
}

func captureNativeEvidence(ctx context.Context, options nativeRunOptions) (capturedNativeEvidence, error) {
	trustedDiff, err := options.Session.ReadTrustedDiff(maxNativeOutputBytes)
	if err != nil {
		return capturedNativeEvidence{}, fmt.Errorf("read trusted diff: %w", err)
	}
	invocation := buildReviewInvocation(options)
	started := time.Now()
	processErr := runCodexProcess(ctx, invocation)
	frozen, err := freezeNativeArtifacts(invocation.paths)
	if err != nil {
		return capturedNativeEvidence{}, err
	}
	metrics := collectRunMetrics(options.Session.Request(), time.Since(started), int64(len(trustedDiff)), frozen)
	if err := writeExclusiveJSON(invocation.paths.metrics, metrics); err != nil {
		return capturedNativeEvidence{}, fmt.Errorf("write native run metrics: %w", err)
	}
	return capturedNativeEvidence{finalMessage: frozen.FinalMessage, processErr: processErr}, nil
}

func runCodexProcess(ctx context.Context, invocation reviewInvocation) error {
	stdout, err := os.OpenFile(invocation.paths.jsonl, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stdout log: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(invocation.paths.stderr, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stderr log: %w", err)
	}
	defer stderr.Close()

	command := exec.CommandContext(ctx, invocation.executable, invocation.args...)
	command.Dir = invocation.directory
	command.Stdin = strings.NewReader(invocation.stdin)
	command.Stdout = processOutputWriter{Writer: stdout}
	command.Stderr = processOutputWriter{Writer: stderr}
	command.ExtraFiles = invocation.extraFiles
	command.WaitDelay = processOutputDrainTimeout
	if err := command.Run(); err != nil {
		return fmt.Errorf("codex process: %w", err)
	}
	return nil
}

func collectRunMetrics(request quality.ReviewRequest, duration time.Duration, trustedDiffBytes int64, frozen frozenNativeArtifacts) NativeRunMetrics {
	metrics := NativeRunMetrics{
		SchemaVersion: 1, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
	}
	if frozen.UsageError != nil {
		metrics.UsageError = frozen.UsageError.Error()
		return metrics
	}
	metrics.InputTokens = frozen.InputTokens
	metrics.OutputTokens = frozen.OutputTokens
	metrics.UsageAvailable = frozen.InputTokens != nil && frozen.OutputTokens != nil
	return metrics
}

func readCodexUsageFile(file *os.File) (*int64, *int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	return decodeCodexUsageReader(file)
}

func decodeCodexUsageReader(reader io.Reader) (*int64, *int64, error) {
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
			return nil, nil, fmt.Errorf("decode Codex JSONL event: %w", err)
		}
		if event.Type == "turn.completed" && event.Usage != nil {
			if event.Usage.InputTokens < 0 || event.Usage.OutputTokens < 0 {
				return nil, nil, errors.New("Codex usage tokens must be non-negative")
			}
			copy := *event.Usage
			latest = &copy
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan Codex JSONL events: %w", err)
	}
	if latest == nil {
		return nil, nil, errors.New("Codex JSONL has no turn.completed usage event")
	}
	if latest.InputTokens == 0 && latest.OutputTokens == 0 {
		return nil, nil, errors.New("Codex JSONL reported zero token usage; counters are unavailable")
	}
	inputTokens := latest.InputTokens
	outputTokens := latest.OutputTokens
	return &inputTokens, &outputTokens, nil
}
