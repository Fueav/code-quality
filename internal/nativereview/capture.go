package nativereview

import (
	"context"
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
	executable        string
	args              []string
	directory         string
	stdin             string
	paths             capturePaths
	extraFiles        []*os.File
	stage             string
	attempt           int
	heartbeatInterval time.Duration
	progress          io.Writer
}

type processOutputWriter struct {
	io.Writer
}

type capturedNativeEvidence struct {
	finalMessage []byte
	processErr   error
}

type NativeRunMetrics struct {
	SchemaVersion              int    `json:"schema_version"`
	Stage                      string `json:"stage"`
	Attempt                    int    `json:"attempt"`
	DurationMS                 int64  `json:"duration_ms"`
	InputTokens                *int64 `json:"input_tokens"`
	OutputTokens               *int64 `json:"output_tokens"`
	CachedInputTokens          *int64 `json:"cached_input_tokens"`
	CachedInputTokensAvailable bool   `json:"cached_input_tokens_available"`
	CachedInputTokensError     string `json:"cached_input_tokens_error,omitempty"`
	UsageAvailable             bool   `json:"usage_available"`
	UsageError                 string `json:"usage_error,omitempty"`
	ChangedFileCount           int    `json:"changed_file_count"`
	TrustedDiffBytes           int64  `json:"trusted_diff_bytes"`
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
	processErr := runNativeProcess(ctx, invocation)
	materializeErr := materializeProviderFinalMessage(options.Provider, invocation.paths)
	frozen, err := freezeNativeArtifacts(invocation.paths, options.Provider)
	if err != nil {
		return capturedNativeEvidence{}, err
	}
	processErr = errors.Join(processErr, materializeErr, frozen.ProtocolError)
	metrics := collectRunMetrics(options.Session.Request(), time.Since(started), int64(len(trustedDiff)), frozen)
	if err := writeExclusiveJSON(invocation.paths.metrics, metrics); err != nil {
		return capturedNativeEvidence{}, fmt.Errorf("write native run metrics: %w", err)
	}
	return capturedNativeEvidence{finalMessage: frozen.FinalMessage, processErr: processErr}, nil
}

func runNativeProcess(ctx context.Context, invocation reviewInvocation) error {
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
	if err := command.Start(); err != nil {
		return fmt.Errorf("native review process: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var ticker *time.Ticker
	var heartbeats <-chan time.Time
	if invocation.progress != nil && invocation.heartbeatInterval > 0 {
		ticker = time.NewTicker(invocation.heartbeatInterval)
		heartbeats = ticker.C
		defer ticker.Stop()
	}
	started := time.Now()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("native review process: %w", err)
			}
			return nil
		case <-heartbeats:
			fmt.Fprintf(invocation.progress, "quality-review: heartbeat stage=%s attempt=%d elapsed=%s\n", invocation.stage, invocation.attempt, time.Since(started).Round(time.Second))
		}
	}
}

// runCodexProcess remains as a narrow compatibility shim for existing tests
// and callers inside this package. New code uses the provider-neutral name.
func runCodexProcess(ctx context.Context, invocation reviewInvocation) error {
	return runNativeProcess(ctx, invocation)
}

func materializeProviderFinalMessage(provider Provider, paths capturePaths) error {
	if !provider.finalMessageFromTranscript() {
		return nil
	}
	file, err := os.Open(paths.jsonl)
	if err != nil {
		return fmt.Errorf("open provider transcript: %w", err)
	}
	transcript, decodeErr := provider.decodeTranscript(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode provider transcript: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close provider transcript: %w", closeErr)
	}
	if err := writeExclusiveFile(paths.finalMessage, transcript.FinalMessage); err != nil {
		return fmt.Errorf("materialize provider final message: %w", err)
	}
	return nil
}

func collectRunMetrics(request quality.ReviewRequest, duration time.Duration, trustedDiffBytes int64, frozen frozenNativeArtifacts) NativeRunMetrics {
	return collectStageRunMetrics(request, "NATIVE", 1, duration, trustedDiffBytes, frozen)
}

func collectStageRunMetrics(request quality.ReviewRequest, stage string, attempt int, duration time.Duration, trustedDiffBytes int64, frozen frozenNativeArtifacts) NativeRunMetrics {
	metrics := NativeRunMetrics{
		SchemaVersion: 2, Stage: stage, Attempt: attempt, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
	}
	if frozen.UsageError != nil {
		metrics.UsageError = frozen.UsageError.Error()
		return metrics
	}
	metrics.InputTokens = frozen.InputTokens
	metrics.OutputTokens = frozen.OutputTokens
	metrics.CachedInputTokens = frozen.CachedInputTokens
	metrics.UsageAvailable = frozen.InputTokens != nil && frozen.OutputTokens != nil
	if frozen.CachedInputTokens != nil {
		metrics.CachedInputTokensAvailable = true
	} else {
		metrics.CachedInputTokensError = "Provider did not report cached input tokens"
	}
	return metrics
}

func readProviderTranscriptFile(file *os.File, provider Provider) (decodedTranscript, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return decodedTranscript{}, err
	}
	return provider.decodeTranscript(file)
}

func decodeCodexUsageReader(reader io.Reader) (*int64, *int64, error) {
	transcript, err := NewCodexProvider("").decodeTranscript(reader)
	if err != nil {
		return nil, nil, err
	}
	return transcript.InputTokens, transcript.OutputTokens, transcript.UsageError
}
