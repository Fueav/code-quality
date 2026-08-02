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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const (
	maxNativeOutputBytes      = int64(10 << 20)
	processOutputDrainTimeout = 5 * time.Second
)

type Options struct {
	Prepared                reviewsession.Prepared
	Request                 quality.ReviewRequest
	Goal                    string
	Model                   string
	ReasoningEffort         string
	EvaluationRubricVersion string
	CodexBinary             string
	Executor                Executor
}

type Invocation struct {
	Executable string
	Args       []string
	Dir        string
	Stdin      string
	OutputPath string
	StdoutPath string
	StderrPath string
}

type Executor interface {
	Run(context.Context, Invocation) error
}

type ProcessExecutor struct{}

type processOutputWriter struct {
	io.Writer
}

func (ProcessExecutor) Run(ctx context.Context, invocation Invocation) error {
	stdout, err := os.OpenFile(invocation.StdoutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stdout log: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(invocation.StderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open command stderr log: %w", err)
	}
	defer stderr.Close()

	command := exec.CommandContext(ctx, invocation.Executable, invocation.Args...)
	command.Dir = invocation.Dir
	command.Stdin = strings.NewReader(invocation.Stdin)
	command.Stdout = processOutputWriter{Writer: stdout}
	command.Stderr = processOutputWriter{Writer: stderr}
	command.WaitDelay = processOutputDrainTimeout
	if err := command.Run(); err != nil {
		return fmt.Errorf("codex process: %w", err)
	}
	return nil
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

func Run(ctx context.Context, options Options) (quality.NativeReviewResult, error) {
	if err := normalizeOptions(&options); err != nil {
		return quality.NativeReviewResult{}, err
	}
	trustedDiff, err := reviewsession.ReadRegularFile(options.Prepared.DiffPath, maxNativeOutputBytes)
	if err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("read trusted diff: %w", err)
	}
	result := newResult(options)
	executor := options.Executor
	if executor == nil {
		executor = ProcessExecutor{}
	}

	result.Execution.ModelCalls = 1
	started := time.Now()
	runErr := executor.Run(ctx, buildReviewInvocation(options))
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	frozen, err := freezeNativeArtifacts(layout)
	if err != nil {
		return quality.NativeReviewResult{}, err
	}
	metrics := collectRunMetrics(options, time.Since(started), int64(len(trustedDiff)), frozen)
	if err := writeExclusiveJSON(reviewsession.NewLayout(options.Prepared.SessionDir).NativeMetricsPath, metrics); err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("write native run metrics: %w", err)
	}
	if runErr != nil {
		markIncomplete(&result, "native review failed: "+runErr.Error())
		return result, nil
	}
	if strings.TrimSpace(string(frozen.FinalMessage)) == "" {
		markIncomplete(&result, "native review output is missing or empty")
		return result, nil
	}
	if isExplicitNoFindingsDocument(string(frozen.FinalMessage)) {
		markPass(&result, "native review reported no actionable findings")
		return result, nil
	}
	markManualReview(&result)
	return result, nil
}

func normalizeOptions(options *Options) error {
	if errors := quality.ValidateRequest(options.Request); len(errors) > 0 {
		return fmt.Errorf("review request is invalid: %s", strings.Join(errors, "; "))
	}
	for name, value := range map[string]string{
		"session":           options.Prepared.SessionDir,
		"repository":        options.Prepared.RepositoryDir,
		"diff":              options.Prepared.DiffPath,
		"native review log": options.Prepared.NativeReviewPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}
	options.Goal = strings.TrimSpace(options.Goal)
	if len(options.Goal) > 4000 {
		return errors.New("review goal exceeds 4000 bytes")
	}
	if options.Model == "" {
		options.Model = "gpt-5.6-sol"
	}
	if options.ReasoningEffort == "" {
		options.ReasoningEffort = "max"
	}
	validEffort := map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true}
	if !validEffort[options.ReasoningEffort] {
		return fmt.Errorf("unsupported reasoning effort %q", options.ReasoningEffort)
	}
	if options.EvaluationRubricVersion == "" {
		options.EvaluationRubricVersion = quality.EvaluationRubricVersion
	}
	if options.CodexBinary == "" {
		options.CodexBinary = "codex"
	}
	return nil
}

func newResult(options Options) quality.NativeReviewResult {
	return quality.NativeReviewResult{
		SchemaVersion:           quality.NativeResultSchemaVersion,
		EvaluationRubricVersion: options.EvaluationRubricVersion,
		Request:                 options.Request,
		ReviewGoal:              options.Goal,
		Findings:                []quality.NativeFinding{},
		Execution: quality.NativeExecution{
			Host: "codex", ReviewMode: "native_review", Model: options.Model,
			ReasoningEffort: options.ReasoningEffort, AdapterDrops: []quality.AdapterDrop{},
		},
		Adjudication: quality.Adjudication{
			SemanticResult: quality.ResultIncomplete,
			RolloutMode:    "report_only", CIAction: "publish_report", Reasons: []string{},
		},
	}
}

func buildReviewInvocation(options Options) Invocation {
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	args := []string{
		"exec", "--sandbox", "workspace-write",
		"--config", "sandbox_workspace_write.network_access=true",
	}
	args = appendModelOptions(args, options)
	args = append(args,
		"--json",
		"--output-last-message", options.Prepared.NativeReviewPath,
		"-",
	)
	return Invocation{
		Executable: options.CodexBinary, Args: args, Dir: options.Prepared.RepositoryDir,
		Stdin: buildReviewPrompt(options), OutputPath: options.Prepared.NativeReviewPath,
		StdoutPath: layout.NativeStdoutPath, StderrPath: layout.NativeStderrPath,
	}
}

func collectRunMetrics(options Options, duration time.Duration, trustedDiffBytes int64, frozen frozenNativeArtifacts) NativeRunMetrics {
	metrics := NativeRunMetrics{
		SchemaVersion: 1, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(options.Request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
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

func buildReviewPrompt(options Options) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the changes introduced by %s relative to %s for actionable defects.\n", options.Request.TargetCommit, options.Request.BaseCommit)
	if options.Goal != "" {
		fmt.Fprintf(&prompt, "User-supplied context: %s\n", strconv.Quote(options.Goal))
	}
	return prompt.String()
}

func appendModelOptions(args []string, options Options) []string {
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", options.Model)
	}
	return append(args, "--config", "model_reasoning_effort="+strconv.Quote(options.ReasoningEffort))
}

func isExplicitNoFindingsDocument(raw string) bool {
	switch strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n")) {
	case "No findings.", "No actionable findings.", "No actionable defects found.":
		return true
	default:
		return false
	}
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	resolved := filepath.VolumeName(path) + string(filepath.Separator)
	pending := pathComponents(path)
	symlinkTraversals := 0
	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, component)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			for _, unresolved := range pending {
				if unresolved == ".." {
					return "", errors.New("parent traversal follows a missing path component")
				}
			}
			resolved = candidate
			for _, unresolved := range pending {
				if unresolved != "." {
					resolved = filepath.Join(resolved, unresolved)
				}
			}
			pending = nil
			continue
		}
		if err != nil {
			return "", err
		}
		actualComponent, err := canonicalComponentName(resolved, component, info)
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(resolved, actualComponent)
		if info.Mode()&os.ModeSymlink == 0 {
			if len(pending) > 0 && !info.IsDir() {
				return "", errors.New("non-directory path component")
			}
			resolved = candidate
			continue
		}
		symlinkTraversals++
		if symlinkTraversals > 255 {
			return "", errors.New("too many symlink traversals")
		}
		target, err := os.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = filepath.VolumeName(target) + string(filepath.Separator)
		}
		pending = append(pathComponents(target), pending...)
	}
	return filepath.Clean(resolved), nil
}

func canonicalComponentName(parent, requested string, requestedInfo os.FileInfo) (string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", err
	}
	for pass := 0; pass < 3; pass++ {
		for _, entry := range entries {
			name := entry.Name()
			if pass == 0 && name != requested {
				continue
			}
			if pass == 1 && (name == requested || !strings.EqualFold(name, requested)) {
				continue
			}
			if pass == 2 && (name == requested || strings.EqualFold(name, requested)) {
				continue
			}
			entryInfo, err := os.Lstat(filepath.Join(parent, name))
			if err == nil && os.SameFile(requestedInfo, entryInfo) {
				return name, nil
			}
		}
	}
	return "", errors.New("path component changed during canonicalization")
}

func pathComponents(path string) []string {
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	return strings.FieldsFunc(path, func(character rune) bool {
		return character == filepath.Separator
	})
}

func markPass(result *quality.NativeReviewResult, reason string) {
	result.Adjudication.SemanticResult = quality.ResultPass
	result.Adjudication.Reasons = []string{reason}
}

func markManualReview(result *quality.NativeReviewResult) {
	result.Findings = []quality.NativeFinding{}
	result.Adjudication.SemanticResult = quality.ResultManualReview
	result.Adjudication.Reasons = []string{
		"native review produced nonempty output that is not an exact no-findings sentinel; inspect frozen native-review.txt",
	}
}

func markIncomplete(result *quality.NativeReviewResult, reason string) {
	result.Findings = []quality.NativeFinding{}
	result.Adjudication.SemanticResult = quality.ResultIncomplete
	result.Adjudication.Reasons = []string{reason}
}

func Publish(prepared reviewsession.Prepared, result quality.NativeReviewResult) error {
	if problems := quality.ValidateNativeResult(result); len(problems) > 0 {
		return fmt.Errorf("native review result is invalid: %s", strings.Join(problems, "; "))
	}
	if err := writeExclusiveJSON(prepared.ResultPath, result); err != nil {
		return fmt.Errorf("write native review result: %w", err)
	}
	if err := writeExclusiveFile(prepared.MarkdownPath, []byte(quality.RenderNativeMarkdown(result))); err != nil {
		return fmt.Errorf("write native review markdown: %w", err)
	}
	return nil
}

func writeExclusiveFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeExclusiveJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := quality.EncodeJSON(file, value); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
