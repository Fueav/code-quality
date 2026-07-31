package codexreview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

const maxNativeOutputBytes = int64(10 << 20)

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
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("codex process: %w", err)
	}
	return nil
}

type nativeEnvelope struct {
	Findings []nativeFinding `json:"findings"`
}

type nativeFinding struct {
	Title        string
	Body         string
	Priority     int
	CodeLocation nativeCodeLocation
}

type nativeCodeLocation struct {
	AbsoluteFilePath string
	LineRange        nativeLineRange
}

type nativeLineRange struct {
	Start int
	End   int
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
	metrics := collectRunMetrics(options, time.Since(started), int64(len(trustedDiff)))
	if err := writeExclusiveJSON(reviewsession.NewLayout(options.Prepared.SessionDir).NativeMetricsPath, metrics); err != nil {
		return quality.NativeReviewResult{}, fmt.Errorf("write native run metrics: %w", err)
	}
	if runErr != nil {
		markIncomplete(&result, "native review failed: "+runErr.Error())
		return result, nil
	}
	rawReview, err := readNativeOutput(options.Prepared.NativeReviewPath)
	if err != nil {
		markIncomplete(&result, "native review output is invalid: "+err.Error())
		return result, nil
	}
	findings, drops := adaptFindings(rawReview.Findings, options.Prepared.RepositoryDir, options.Request.ChangedFiles)
	result.Execution.AdapterDrops = drops
	if len(rawReview.Findings) > 0 && len(findings) == 0 {
		markIncomplete(&result, "native review returned candidates, but none mapped to the trusted changed-file scope")
		return result, nil
	}
	if len(findings) == 0 {
		markPass(&result, "native review reported no actionable findings")
		return result, nil
	}
	result.Findings = findings
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
	if options.ReasoningEffort == "" {
		options.ReasoningEffort = "high"
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
	args := []string{"exec", "--sandbox", "read-only", "--ignore-user-config", "--ignore-rules", "--ephemeral", "review"}
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

func collectRunMetrics(options Options, duration time.Duration, trustedDiffBytes int64) NativeRunMetrics {
	metrics := NativeRunMetrics{
		SchemaVersion: 1, DurationMS: duration.Milliseconds(),
		ChangedFileCount: len(options.Request.ChangedFiles), TrustedDiffBytes: trustedDiffBytes,
	}
	layout := reviewsession.NewLayout(options.Prepared.SessionDir)
	inputTokens, outputTokens, err := readCodexUsage(layout.NativeStdoutPath)
	if err != nil {
		metrics.UsageError = err.Error()
		return metrics
	}
	metrics.InputTokens = inputTokens
	metrics.OutputTokens = outputTokens
	metrics.UsageAvailable = inputTokens != nil && outputTokens != nil
	return metrics
}

func readCodexUsage(path string) (*int64, *int64, error) {
	raw, err := reviewsession.ReadRegularFile(path, maxNativeOutputBytes)
	if err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
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
	inputTokens := latest.InputTokens
	outputTokens := latest.OutputTokens
	return &inputTokens, &outputTokens, nil
}

func buildReviewPrompt(options Options) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Review the committed change %s..%s in the current repository.\n", options.Request.BaseCommit, options.Request.TargetCommit)
	fmt.Fprintf(&prompt, "Use `git diff --no-ext-diff --unified=6 %s %s --` as the exact review scope.\n", options.Request.BaseCommit, options.Request.TargetCommit)
	if options.Goal != "" {
		fmt.Fprintf(&prompt, "User-supplied optional focus: %s. This is not a review boundary; report actionable defects outside it too.\n", strconv.Quote(options.Goal))
	}
	prompt.WriteString("Do not modify files.\n")
	return prompt.String()
}

func appendModelOptions(args []string, options Options) []string {
	if strings.TrimSpace(options.Model) != "" {
		args = append(args, "--model", options.Model)
	}
	return append(args, "--config", "model_reasoning_effort="+strconv.Quote(options.ReasoningEffort))
}

func readNativeOutput(path string) (nativeEnvelope, error) {
	raw, err := reviewsession.ReadRegularFile(path, maxNativeOutputBytes)
	if err != nil {
		return nativeEnvelope{}, err
	}
	return parseNativeReviewText(string(raw))
}

func parseNativeReviewText(raw string) (nativeEnvelope, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	first := firstNonBlankLine(lines)
	if first < 0 {
		return nativeEnvelope{}, errors.New("native review output is empty")
	}
	if strings.TrimSpace(lines[first]) == "No findings." {
		for _, line := range lines[first+1:] {
			trimmed := strings.TrimSpace(line)
			if isNativeReviewHeading(trimmed) || strings.HasPrefix(trimmed, "- [P") {
				return nativeEnvelope{}, errors.New("native review contradicts its no-findings result")
			}
		}
		return nativeEnvelope{Findings: []nativeFinding{}}, nil
	}

	heading := -1
	for index := first; index < len(lines); index++ {
		if isNativeReviewHeading(lines[index]) {
			heading = index
			break
		}
	}
	if heading < 0 {
		for _, line := range lines[first:] {
			_, recognized, err := parseNativeFindingHeader(line)
			if err != nil {
				return nativeEnvelope{}, err
			}
			if recognized {
				return nativeEnvelope{}, errors.New("native finding appears without a review comment section")
			}
		}
		return nativeEnvelope{Findings: []nativeFinding{}}, nil
	}

	findings := []nativeFinding{}
	body := []string{}
	var current *nativeFinding
	trailingAssessment := false
	flush := func() error {
		if current == nil {
			return nil
		}
		current.Body = strings.TrimSpace(strings.Join(body, "\n"))
		if current.Body == "" {
			return fmt.Errorf("native finding %d has no body", len(findings))
		}
		findings = append(findings, *current)
		current = nil
		body = nil
		return nil
	}

	for _, line := range lines[heading+1:] {
		finding, recognized, err := parseNativeFindingHeader(line)
		if err != nil {
			return nativeEnvelope{}, err
		}
		if recognized {
			if trailingAssessment {
				return nativeEnvelope{}, errors.New("native finding appears after the trailing assessment")
			}
			if err := flush(); err != nil {
				return nativeEnvelope{}, err
			}
			current = &finding
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if current == nil {
			return nativeEnvelope{}, fmt.Errorf("unexpected text in native review comment section: %q", strings.TrimSpace(line))
		}
		if line[0] == ' ' || line[0] == '\t' {
			if trailingAssessment {
				continue
			}
			body = append(body, strings.TrimSpace(line))
			continue
		}
		if len(body) == 0 {
			return nativeEnvelope{}, fmt.Errorf("native finding %d has no body", len(findings))
		}
		trailingAssessment = true
	}
	if err := flush(); err != nil {
		return nativeEnvelope{}, err
	}
	if len(findings) == 0 {
		return nativeEnvelope{}, errors.New("native review comment section contains no findings")
	}
	return nativeEnvelope{Findings: findings}, nil
}

func isNativeReviewHeading(line string) bool {
	switch strings.TrimSpace(line) {
	case "Review comment:", "Review comments:", "Full review comment:", "Full review comments:":
		return true
	default:
		return false
	}
}

func firstNonBlankLine(lines []string) int {
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			return index
		}
	}
	return -1
}

func parseNativeFindingHeader(line string) (nativeFinding, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "- [P") {
		return nativeFinding{}, false, nil
	}
	if len(trimmed) < 7 || trimmed[5] != ']' || trimmed[6] != ' ' || trimmed[4] < '0' || trimmed[4] > '3' {
		return nativeFinding{}, true, fmt.Errorf("malformed native finding header: %q", trimmed)
	}
	rest := strings.TrimSpace(trimmed[7:])
	delimiter := strings.LastIndex(rest, " — ")
	if delimiter < 1 {
		return nativeFinding{}, true, fmt.Errorf("native finding header has no title/location delimiter: %q", trimmed)
	}
	title := strings.TrimSpace(rest[:delimiter])
	location := strings.TrimSpace(rest[delimiter+len(" — "):])
	path, start, end, err := parseNativeLocation(location)
	if err != nil {
		return nativeFinding{}, true, fmt.Errorf("native finding location %q is invalid: %w", location, err)
	}
	if title == "" {
		return nativeFinding{}, true, errors.New("native finding title is empty")
	}
	return nativeFinding{
		Title:    title,
		Priority: int(trimmed[4] - '0'),
		CodeLocation: nativeCodeLocation{
			AbsoluteFilePath: path,
			LineRange:        nativeLineRange{Start: start, End: end},
		},
	}, true, nil
}

func parseNativeLocation(raw string) (string, int, int, error) {
	separator := strings.LastIndex(raw, ":")
	if separator < 1 || separator == len(raw)-1 {
		return "", 0, 0, errors.New("expected absolute-path:start-end")
	}
	path := strings.TrimSpace(raw[:separator])
	lineRange := strings.TrimSpace(raw[separator+1:])
	startText, endText, hasEnd := strings.Cut(lineRange, "-")
	start, err := strconv.Atoi(startText)
	if err != nil || start < 1 {
		return "", 0, 0, errors.New("start line must be positive")
	}
	end := start
	if hasEnd {
		end, err = strconv.Atoi(endText)
		if err != nil || end < start {
			return "", 0, 0, errors.New("end line must be at least the start line")
		}
	}
	return path, start, end, nil
}

func adaptFindings(raw []nativeFinding, repository string, changedFiles []string) ([]quality.NativeFinding, []quality.AdapterDrop) {
	changed := make(map[string]struct{}, len(changedFiles))
	for _, path := range changedFiles {
		changed[filepath.ToSlash(path)] = struct{}{}
	}
	findings := make([]quality.NativeFinding, 0, len(raw))
	drops := []quality.AdapterDrop{}
	canonicalRepository, repositoryErr := canonicalPath(repository)
	for index, candidate := range raw {
		reason := validateNativeCandidate(candidate)
		relative := ""
		if reason == "" {
			if repositoryErr != nil {
				reason = "isolated checkout path cannot be canonicalized"
			}
		}
		if reason == "" {
			canonicalCandidate, err := canonicalPath(candidate.CodeLocation.AbsoluteFilePath)
			if err != nil {
				reason = "code location cannot be canonicalized"
			} else {
				relative, err = filepath.Rel(canonicalRepository, canonicalCandidate)
				if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					reason = "code location is outside the isolated checkout"
				} else {
					relative = filepath.ToSlash(relative)
					if _, exists := changed[relative]; !exists {
						reason = "code location is not in a changed file"
					}
				}
			}
		}
		if reason != "" {
			drops = append(drops, quality.AdapterDrop{Index: index, Reason: reason})
			continue
		}
		findings = append(findings, quality.NativeFinding{
			Title: strings.TrimSpace(candidate.Title), Body: strings.TrimSpace(candidate.Body),
			Priority: candidate.Priority,
			CodeLocation: quality.NativeCodeLocation{
				Path: relative, StartLine: candidate.CodeLocation.LineRange.Start, EndLine: candidate.CodeLocation.LineRange.End,
			},
		})
	}
	return findings, drops
}

func canonicalPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", errors.New("path must be absolute")
	}
	existing := clean
	suffix := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(existing)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
}

func validateNativeCandidate(candidate nativeFinding) string {
	if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Body) == "" {
		return "title and body are required"
	}
	if candidate.Priority < 0 || candidate.Priority > 3 {
		return "priority is outside 0-3"
	}
	if !filepath.IsAbs(candidate.CodeLocation.AbsoluteFilePath) {
		return "code location must be absolute"
	}
	if candidate.CodeLocation.LineRange.Start < 1 || candidate.CodeLocation.LineRange.End < candidate.CodeLocation.LineRange.Start {
		return "line range is invalid"
	}
	return ""
}

func markPass(result *quality.NativeReviewResult, reason string) {
	result.Adjudication.SemanticResult = quality.ResultPass
	result.Adjudication.Reasons = []string{reason}
}

func markManualReview(result *quality.NativeReviewResult) {
	result.Adjudication.SemanticResult = quality.ResultManualReview
	result.Adjudication.Reasons = []string{fmt.Sprintf("%d native finding(s) require manual review", len(result.Findings))}
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
